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
	"time"
)

// DefaultBaseURL is the host of every endpoint the provider calls.
const DefaultBaseURL = "https://api.mistral.ai"

// maxErrorBody bounds what an error keeps of a response body.
const maxErrorBody = 4 << 10

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
	// HTTPClient sends the requests; http.DefaultClient when nil.
	HTTPClient *http.Client
	// Retry is the retry policy; the zero value retries nothing.
	Retry RetryPolicy
}

// Client calls the Mistral API.
type Client struct {
	cfg  Config
	http *http.Client
}

// New returns a Client for cfg.
func New(cfg Config) *Client {
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Client{cfg: cfg, http: hc}
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

// send makes one attempt. sent reports whether the request was written to
// the connection, which decides whether a write may be replayed after a
// connection error.
func (cl *Client) send(ctx context.Context, c call, payload []byte) (*http.Response, bool, error) {
	var sent bool
	trace := &httptrace.ClientTrace{WroteRequest: func(httptrace.WroteRequestInfo) { sent = true }}
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
	req, err := http.NewRequestWithContext(ctx, c.method, u, body)
	if err != nil {
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

	resp, err := cl.http.Do(req)
	return resp, sent, err
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

// escapePath joins segments into a path, escaping each one: "/v1/agents" and
// an agent ID give "/v1/agents/<id>" whatever the ID holds.
func escapePath(segments ...string) string {
	var b strings.Builder
	for _, s := range segments {
		if strings.HasPrefix(s, "/") {
			b.WriteString(s)
			continue
		}
		b.WriteByte('/')
		b.WriteString(url.PathEscape(s))
	}
	return b.String()
}
