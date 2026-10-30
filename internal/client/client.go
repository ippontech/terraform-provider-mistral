// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package client is the hand-written HTTP client of the Mistral AI API: the
// transport (authentication, retries, errors, pagination, multipart uploads)
// and one file of typed calls per API area.
//
// It has no Terraform import: internal/services maps its types to Terraform
// state. The OpenAPI specification (https://docs.mistral.ai/openapi.yaml) is
// the reference for paths and payloads, read against the guides where they
// disagree; each typed call cites its page.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

// DefaultBaseURL is the host of every endpoint the provider calls.
const DefaultBaseURL = "https://api.mistral.ai"

// maxErrorBody bounds what an error keeps of a response body.
const maxErrorBody = 4 << 10

// responseHeaderTimeout bounds the wait for the headers of an answer to a
// call other than an upload, from the moment the request, body included, is
// written. Those calls answer within seconds, so 90 s leaves a wide margin
// for a slow gateway and only cuts an exchange that nothing will answer.
const responseHeaderTimeout = 90 * time.Second

// Config configures a Client.
type Config struct {
	// BaseURL is the API base URL, without a trailing slash.
	BaseURL string
	// APIKey is the standard key, for every endpoint outside /v1/admin.
	APIKey string
	// AdminAPIKey is the Admin API key, for /v1/admin.
	AdminAPIKey string
	// UserAgent is sent on every request.
	UserAgent string
	// HTTPClient sends the requests, uploads included; when nil, the clients
	// of newHTTPClients.
	HTTPClient *http.Client
	// Retry is the retry policy; the zero value retries nothing.
	Retry RetryPolicy
}

// Client calls the Mistral API.
type Client struct {
	cfg Config
	// http sends every call but the uploads.
	http *http.Client
	// upload sends the multipart calls.
	upload *http.Client
}

// New returns a Client for cfg.
func New(cfg Config) *Client {
	return newClient(cfg, responseHeaderTimeout)
}

// newClient is New with the bound on the wait for an answer as a parameter,
// so that a test can shorten it.
func newClient(cfg Config, headerTimeout time.Duration) *Client {
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	calls, uploads := cfg.HTTPClient, cfg.HTTPClient
	if calls == nil {
		calls, uploads = newHTTPClients(headerTimeout)
	}
	return &Client{cfg: cfg, http: calls, upload: uploads}
}

// newHTTPClients returns the HTTP clients of a Client configured without
// one. Both have the settings of http.DefaultTransport (proxy from the
// environment, dial and TLS handshake timeouts, HTTP/2); neither sets
// http.Client.Timeout, which would bound the whole exchange, upload
// included.
//
// calls gives up on an answer whose headers have not come headerTimeout
// after the request was written (the transport's ResponseHeaderTimeout).
// http.DefaultClient waits forever: a server or a proxy that accepts the
// connection and never answers would block a Terraform run until it is
// killed. A request that timed out was written, so the retry policy replays
// a read but not a create.
//
// uploads, for the multipart calls, has no such bound. Its timer would start
// once the body is written, but behind a TLS-inspecting or DLP proxy that
// buffers a whole upload before forwarding it, that is when the body reaches
// the proxy: the proxy's own transfer to the API would count in the bound,
// and a large document could fail. An upload is bounded by its context
// instead, the timeouts.create of mistral_library_document.
func newHTTPClients(headerTimeout time.Duration) (calls, uploads *http.Client) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	bounded := transport.Clone()
	bounded.ResponseHeaderTimeout = headerTimeout
	return &http.Client{Transport: bounded}, &http.Client{Transport: transport}
}

// audience selects the credential of a request.
type audience int

const (
	// workspaceKey is the standard key, scoped to one workspace.
	workspaceKey audience = iota + 1
	// adminKey is the Admin API key, tied to one organization.
	adminKey
)

// call is one API request.
type call struct {
	method string
	// path is the escaped path, built with escapePath.
	path  string
	query url.Values
	// body is encoded as JSON when not nil.
	body any
	// form is a multipart body, rebuilt for every attempt.
	form     *multipartForm
	audience audience
	// idempotent marks a write the API documents as idempotent (an upsert),
	// which is retried like a read.
	idempotent bool
	// status, when not nil, receives the status code of a successful answer,
	// for an endpoint whose 2xx codes mean different things (an upload
	// answers 201 for a new document and 200 for an existing one).
	status *int
}

// do sends c and decodes the JSON response into out, when out is not nil.
func (cl *Client) do(ctx context.Context, c call, out any) error {
	var payload []byte
	if c.body != nil {
		var err error
		if payload, err = json.Marshal(c.body); err != nil {
			return fmt.Errorf("mistral: encode %s %s: %w", c.method, c.path, err)
		}
	}

	for attempt := 0; ; attempt++ {
		resp, sent, err := cl.send(ctx, c, payload)
		decision := cl.cfg.Retry.decide(c, resp, sent, err, attempt)
		if decision.retry {
			if resp != nil {
				drain(resp)
			}
			if werr := wait(ctx, decision.delay); werr != nil {
				return werr
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("mistral: %s %s: %w", c.method, c.path, err)
		}
		return decode(c, resp, out)
	}
}

// send makes one attempt. sent reports whether the request counts as
// written to the connection, which decides whether a write may be replayed
// after a connection error.
func (cl *Client) send(ctx context.Context, c call, payload []byte) (*http.Response, bool, error) {
	// The transport calls WroteRequest from the goroutine that writes the
	// body, which can still be running when Do returns: a server may answer
	// an upload (a 413, say) before reading all of it. httptrace allows a
	// hook to run after the request has completed, hence the atomics.
	//
	// A write that failed on a reused connection, a keep-alive connection the
	// server closed in the meantime, did not deliver the request: it counts
	// as unsent, so that a create may be replayed. On a new connection, a
	// failed write is most often a server or proxy that refused the request
	// early (a 413 for a large upload) and closed the connection: net/http
	// then returns the broken write rather than the answer, and a replay
	// would only be refused again, so it counts as sent.
	var sent, reused atomic.Bool
	trace := &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) { reused.Store(info.Reused) },
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err == nil || !reused.Load() {
				sent.Store(true)
			}
		},
	}
	ctx = httptrace.WithClientTrace(ctx, trace)

	u := cl.cfg.BaseURL + c.path
	if len(c.query) > 0 {
		u += "?" + c.query.Encode()
	}
	var body io.Reader
	contentType := ""
	switch {
	case c.form != nil:
		r, ct, err := c.form.reader()
		if err != nil {
			return nil, false, &buildError{err}
		}
		body, contentType = r, ct
	case payload != nil:
		body, contentType = bytes.NewReader(payload), "application/json"
	}
	req, err := newRequest(ctx, c.method, c.path, u, body)
	if err != nil {
		// Nothing else closes a body no request was built with: close the
		// multipart pipe, so that its writer stops and closes the files.
		if closer, ok := body.(io.Closer); ok {
			_ = closer.Close()
		}
		return nil, false, &buildError{err}
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Accept", "application/json")
	if cl.cfg.UserAgent != "" {
		req.Header.Set("User-Agent", cl.cfg.UserAgent)
	}
	cl.authenticate(req, c.audience)

	hc := cl.http
	if c.form != nil {
		hc = cl.upload // newHTTPClients says why
	}
	resp, err := hc.Do(req)
	return resp, sent.Load(), err
}

// newRequest builds the request of a call, unless escapePath refused its
// path: that path would reach another object once a server or proxy resolves
// its dot segments.
func newRequest(ctx context.Context, method, path, u string, body io.Reader) (*http.Request, error) {
	if strings.Contains(path, invalidSegment) {
		return nil, errors.New(`a path segment is empty, ".", ".." or starts with "/"`)
	}
	return http.NewRequestWithContext(ctx, method, u, body)
}

// buildError is a failure to build a request, before any network activity:
// replaying it would fail the same way, so it is never retried.
type buildError struct{ err error }

func (e *buildError) Error() string { return e.err.Error() }
func (e *buildError) Unwrap() error { return e.err }

// authenticate sets the credential of a. Both keys go in a bearer header: the
// specification declares the Admin API key as "Admin-scoped API key passed as
// a bearer token", while the Admin API authentication guide shows an
// x-api-key header. This is the one place to change once a live call settles
// it (CLAUDE.md, "Credentials and the API").
func (cl *Client) authenticate(req *http.Request, a audience) {
	key := cl.cfg.APIKey
	if a == adminKey {
		key = cl.cfg.AdminAPIKey
	}
	req.Header.Set("Authorization", "Bearer "+key)
}

func decode(c call, resp *http.Response, out any) error {
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return newError(c, resp)
	}
	if c.status != nil {
		*c.status = resp.StatusCode
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("mistral: decode %s %s: %w", c.method, c.path, err)
	}
	return nil
}

func drain(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBody))
	_ = resp.Body.Close()
}

func wait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// invalidSegment marks a path escapePath refused. It reads like the marks
// fmt leaves on a bad verb, and send checks for it.
const invalidSegment = "%!(invalid path segment"

// escapePath joins a base path and segments into a path. The base, a
// constant such as "/v1/agents" or a path escapePath built, is written as it
// is; each segment is escaped, so that "/v1/agents" and an agent ID give
// "/v1/agents/<id>" whatever the ID holds.
//
// Escaping cannot keep every segment in its place, so some are refused: an
// empty one, which turns "/v1/agents/<id>" into "/v1/agents/", the list;
// "." and "..", which servers and proxies resolve as dot segments (RFC 3986,
// section 5.2.4), so that "/v1/agents/.." reaches "/v1"; and one with a
// leading "/", which only a base path passed in the wrong place has.
// escapePath then returns a path that ends with invalidSegment and names the
// segment, so that error messages show it, and send refuses that path with a
// buildError, never retried, before anything is sent. Returning an error
// instead would add one to each of the typed calls.
func escapePath(base string, segments ...string) string {
	var b strings.Builder
	b.WriteString(base)
	for _, s := range segments {
		if s == "" || s == "." || s == ".." || strings.HasPrefix(s, "/") {
			return fmt.Sprintf("%s/%s %q)", base, invalidSegment, s)
		}
		b.WriteByte('/')
		b.WriteString(url.PathEscape(s))
	}
	return b.String()
}
