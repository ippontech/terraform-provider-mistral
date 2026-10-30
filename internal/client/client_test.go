// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

// fastRetry is the production policy with millisecond delays.
var fastRetry = RetryPolicy{MaxRetries: 3, BaseDelay: time.Millisecond, MaxDelay: 4 * time.Millisecond, MaxRetryAfter: 50 * time.Millisecond}

func newTestClient(srv *sdktest.Server) *Client {
	return New(Config{
		BaseURL:     srv.URL,
		APIKey:      sdktest.TestAPIKey,
		AdminAPIKey: sdktest.TestAdminKey,
		UserAgent:   "terraform-provider-mistral/test",
		Retry:       fastRetry,
	})
}

func TestDoSendsTheCredentialOfTheAudience(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("GET /v1/models", http.StatusOK, map[string]any{"data": []any{}})
	srv.Respond("GET /v1/admin/roles", http.StatusOK, []any{})
	c := newTestClient(srv)

	if err := c.do(context.Background(), call{method: http.MethodGet, path: "/v1/models", audience: workspaceKey}, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.do(context.Background(), call{method: http.MethodGet, path: "/v1/admin/roles", audience: adminKey}, nil); err != nil {
		t.Fatal(err)
	}

	srv.AssertCredential("GET /v1/models", sdktest.APIKey)
	srv.AssertCredential("GET /v1/admin/roles", sdktest.AdminKey)
	for _, r := range srv.Requests("") {
		if got := r.Header.Get("User-Agent"); got != "terraform-provider-mistral/test" {
			t.Errorf("User-Agent = %q", got)
		}
		if got := r.Header.Get("Accept"); got != "application/json" {
			t.Errorf("Accept = %q", got)
		}
	}
}

func TestDoEncodesTheBodyAndQueryAndDecodesTheAnswer(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Handle("POST /v1/things", func(w http.ResponseWriter, r *http.Request) {
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q", ct)
		}
		sdktest.WriteJSON(w, http.StatusCreated, map[string]any{"id": "t1", "name": "x"})
	})
	c := newTestClient(srv)

	var out struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	err := c.do(context.Background(), call{
		method: http.MethodPost, path: "/v1/things", query: url.Values{"a": {"1 2"}},
		body: map[string]any{"name": "x"}, audience: workspaceKey,
	}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if out.ID != "t1" || out.Name != "x" {
		t.Errorf("decoded %+v", out)
	}
	srv.AssertJSONBodies("POST /v1/things", `{"name":"x"}`)
	srv.AssertQueries("POST /v1/things", "a=1+2")
}

func TestDoAcceptsEmptyAnswers(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("DELETE /v1/things/{id}", http.StatusNoContent, nil)
	srv.Handle("DELETE /v1/empty/{id}", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	c := newTestClient(srv)
	var out map[string]any

	for _, p := range []string{"/v1/things/1", "/v1/empty/1"} {
		if err := c.do(context.Background(), call{method: http.MethodDelete, path: p, audience: workspaceKey}, &out); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
}

func TestDoReportsAnUndecodableAnswer(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Handle("GET /v1/bad", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "{not json")
	})
	var out map[string]any
	err := newTestClient(srv).do(context.Background(), call{method: http.MethodGet, path: "/v1/bad", audience: workspaceKey}, &out)
	if err == nil || !strings.Contains(err.Error(), "decode GET /v1/bad") {
		t.Errorf("err = %v", err)
	}
}

func TestDoReportsAnUnencodableBody(t *testing.T) {
	t.Parallel()
	c := New(Config{BaseURL: "http://127.0.0.1:1"})
	err := c.do(context.Background(), call{method: http.MethodPost, path: "/v1/x", body: make(chan int)}, nil)
	if err == nil || !strings.Contains(err.Error(), "encode POST /v1/x") {
		t.Errorf("err = %v", err)
	}
}

func TestNewDefaults(t *testing.T) {
	t.Parallel()
	c := New(Config{BaseURL: "https://example.com/"})
	if c.cfg.BaseURL != "https://example.com" {
		t.Errorf("New = %+v", c.cfg)
	}
	if c := New(Config{}); c.cfg.BaseURL != DefaultBaseURL {
		t.Errorf("default base URL = %q", c.cfg.BaseURL)
	}
	own := &http.Client{}
	if c := New(Config{HTTPClient: own}); c.http != own || c.upload != own {
		t.Error("a configured HTTPClient must send every call, uploads included")
	}
}

// Without a configured HTTPClient, the client bounds the wait for the
// headers of an answer, except for uploads, on copies of
// http.DefaultTransport, and sets no overall timeout, which would cut a long
// upload.
func TestNewBoundsTheWaitForAnAnswer(t *testing.T) {
	t.Parallel()
	c := New(Config{})
	calls, uploads := copyOfDefaultTransport(t, c.http), copyOfDefaultTransport(t, c.upload)
	if got := calls.ResponseHeaderTimeout; got != responseHeaderTimeout || got < time.Minute {
		t.Errorf("ResponseHeaderTimeout = %v, want %v, and a minute at least so that a slow answer is not cut", got, responseHeaderTimeout)
	}
	if got := uploads.ResponseHeaderTimeout; got != 0 {
		t.Errorf("ResponseHeaderTimeout of the uploads = %v, want none", got)
	}
}

// copyOfDefaultTransport returns the transport of hc, after checking that
// it is a copy of http.DefaultTransport, proxy settings included, and that
// hc sets no overall timeout.
func copyOfDefaultTransport(t *testing.T, hc *http.Client) *http.Transport {
	t.Helper()
	transport, ok := hc.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport = %T, want *http.Transport", hc.Transport)
	}
	if transport == http.DefaultTransport || transport.Proxy == nil {
		t.Error("the transport must be a copy of http.DefaultTransport, proxy settings included")
	}
	if hc.Timeout != 0 {
		t.Errorf("http.Client.Timeout = %v, want none", hc.Timeout)
	}
	return transport
}

// A server that accepts the connection and never answers fails the call
// once the header timeout has passed on each attempt, instead of blocking
// it. A read is replayed after such a timeout, like after any connection
// error.
func TestAServerThatNeverAnswersTimesOut(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Handle("GET /v1/agents/{agent_id}", func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // until the client gives up and closes the connection
	})
	c := newClient(Config{BaseURL: srv.URL, Retry: fastRetry}, 20*time.Millisecond)
	// Counted at the transport: under load, an attempt can time out before
	// the fake has dispatched it to the handler.
	transport := &countingTransport{next: c.http.Transport}
	c.http.Transport = transport
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := c.GetAgent(ctx, "ag_1")
	if netErr, ok := errors.AsType[net.Error](err); !ok || !netErr.Timeout() {
		t.Errorf("err = %v, want a timeout", err)
	}
	if ctx.Err() != nil {
		t.Error("the call ran until the context ended, not until the header timeout")
	}
	if got := transport.n.Load(); got != 4 {
		t.Errorf("%d attempts, want 1 + MaxRetries", got)
	}
}

// An upload is not bounded by the header timeout, which a proxy that
// buffers the whole upload would make count its own transfer, while a create
// with a JSON body that answers as late is cut and, being a create, not
// replayed.
func TestAnUploadIsNotCutByTheHeaderTimeout(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	answerLate := func(body map[string]any) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(100 * time.Millisecond) // five times the header timeout below
			sdktest.WriteJSON(w, http.StatusCreated, body)
		}
	}
	srv.Handle("POST /v1/libraries/{library_id}/documents", answerLate(document))
	srv.Handle("POST /v1/libraries", answerLate(library))
	c := newClient(Config{BaseURL: srv.URL, Retry: fastRetry}, 20*time.Millisecond)
	ctx := context.Background()

	if _, created, err := c.UploadDocument(ctx, "lib_1", textFile("a.txt", "a")); err != nil || !created {
		t.Errorf("UploadDocument = %v, %v, want a new document", created, err)
	}
	_, err := c.CreateLibrary(ctx, CreateLibraryRequest{Name: "n", OwnerType: "Workspace"})
	if netErr, ok := errors.AsType[net.Error](err); !ok || !netErr.Timeout() {
		t.Errorf("CreateLibrary err = %v, want a timeout", err)
	}
	srv.AssertCalled("POST /v1/libraries/{library_id}/documents", 1)
	srv.AssertCalled("POST /v1/libraries", 1)
}

// failedWriteTransport fails its first attempt as a broken connection does:
// GotConn reports the connection, reused or new, the write of the request
// fails, WroteRequest reports the error, and RoundTrip returns it. Later
// attempts answer 201.
type failedWriteTransport struct {
	reused   bool
	attempts atomic.Int64
}

// RoundTrip fails the first attempt and answers the others.
func (tr *failedWriteTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Body != nil {
		_ = r.Body.Close()
	}
	if tr.attempts.Add(1) == 1 {
		err := errors.New("write tcp: broken pipe")
		trace := httptrace.ContextClientTrace(r.Context())
		trace.GotConn(httptrace.GotConnInfo{Reused: tr.reused})
		trace.WroteRequest(httptrace.WroteRequestInfo{Err: err})
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusCreated,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{}`)),
		Request:    r,
	}, nil
}

// A create whose write failed on a reused connection never reached the
// server whole: it is replayed. On a new connection, the failed write
// counts as sent, as a refusal mid-body does (see
// TestAnUploadRefusedMidBodyIsNotReplayed): the create is not replayed.
func TestACreateWhoseWriteFailedIsReplayedOnAReusedConnectionOnly(t *testing.T) {
	t.Parallel()
	for name, reused := range map[string]bool{"reused": true, "new": false} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			transport := &failedWriteTransport{reused: reused}
			c := New(Config{BaseURL: "https://api.example", HTTPClient: &http.Client{Transport: transport}, Retry: fastRetry})

			err := c.do(context.Background(), call{method: http.MethodPost, path: "/v1/libraries", body: map[string]any{}, audience: workspaceKey}, nil)
			want := int64(1)
			if reused {
				want = 2
			}
			if (err == nil) != reused {
				t.Errorf("err = %v, want an error only on a new connection", err)
			}
			if got := transport.attempts.Load(); got != want {
				t.Errorf("%d attempts, want %d", got, want)
			}
		})
	}
}

// rawServer serves HTTP/1.1 on a bare listener, so that a test can drop a
// connection in the middle of a request as a real server or proxy does,
// which httptest's server does not. handle gets each request with its body
// unread, numbered from 1 across connections, and answers it on c; the
// connection is closed, unread data and all, when handle returns false.
// rawServer returns the base URL and the count of accepted connections.
func rawServer(t *testing.T, handle func(c net.Conn, r *http.Request, n int64) (keepAlive bool)) (string, *atomic.Int64) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var (
		accepted, requests atomic.Int64
		mu                 sync.Mutex
		open               []net.Conn
	)
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range open {
			_ = c.Close()
		}
	})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			mu.Lock()
			open = append(open, c)
			mu.Unlock()
			go func() {
				defer func() { _ = c.Close() }()
				br := bufio.NewReader(c)
				for {
					r, err := http.ReadRequest(br)
					if err != nil || !handle(c, r, requests.Add(1)) {
						return
					}
				}
			}()
		}
	}()
	return "http://" + ln.Addr().String(), &accepted
}

// largeFile is a 32 MB document, more than the kernel buffers of a loopback
// connection hold, so that a server that stops reading breaks its write.
func largeFile() DocumentFile {
	return DocumentFile{Filename: "large.bin", Open: func() (io.ReadCloser, error) {
		return io.NopCloser(io.LimitReader(zeros{}, 32<<20)), nil
	}}
}

// zeros reads as an endless run of zero bytes.
type zeros struct{}

// Read fills p with zeros.
func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

// A server or proxy that refuses an upload before reading it (a 413 for a
// file too large) and closes the connection breaks the write of the body.
// net/http then returns the broken write in most runs, and the 413 when it
// read the answer first; a server that drops the connection without an
// answer always gives the broken write. On a new connection that write
// counts as sent: the upload is not sent three more times only to be
// refused again.
func TestAnUploadRefusedMidBodyIsNotReplayed(t *testing.T) {
	t.Parallel()
	answers := map[string]string{
		"refused with a 413":        "HTTP/1.1 413 Request Entity Too Large\r\nContent-Length: 0\r\nConnection: close\r\n\r\n",
		"dropped without an answer": "",
	}
	for name, answer := range answers {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			url, connections := rawServer(t, func(c net.Conn, _ *http.Request, _ int64) bool {
				_, _ = io.WriteString(c, answer)
				return false
			})
			c := New(Config{BaseURL: url, Retry: fastRetry})

			if _, _, err := c.UploadDocument(context.Background(), "lib_1", largeFile()); err == nil {
				t.Error("the upload succeeded")
			}
			if got := connections.Load(); got != 1 {
				t.Errorf("%d attempts, want 1", got)
			}
		})
	}
}

// An upload on a kept-alive connection that the server has closed in the
// meantime breaks while its body is written: the server never got it, so it
// is replayed, on a new connection.
func TestAnUploadOnAClosedKeepAliveConnectionIsReplayed(t *testing.T) {
	t.Parallel()
	url, connections := rawServer(t, func(c net.Conn, r *http.Request, n int64) bool {
		if n == 2 {
			return false // the second request finds the connection closed
		}
		_, _ = io.Copy(io.Discard, r.Body)
		_, _ = io.WriteString(c, "HTTP/1.1 201 Created\r\nContent-Type: application/json\r\nContent-Length: 2\r\n\r\n{}")
		return true
	})
	c := New(Config{BaseURL: url, Retry: fastRetry})
	ctx := context.Background()

	if _, _, err := c.UploadDocument(ctx, "lib_1", textFile("a.txt", "a")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.UploadDocument(ctx, "lib_1", largeFile()); err != nil {
		t.Errorf("err = %v, want the 201 of the replay", err)
	}
	if got := connections.Load(); got != 2 {
		t.Errorf("%d connections, want the kept-alive one and the replay's", got)
	}
}

// lateTraceTransport answers at once and reports the end of the write
// afterwards, from a goroutine of its own. net/http does the same when a
// server answers before reading the whole body, and the httptrace contract
// allows it: a hook may be called after the request has completed.
type lateTraceTransport struct {
	// reported is closed once the hook has run.
	reported chan struct{}
}

// RoundTrip answers 413 at once, then closes the body and calls WroteRequest
// from a goroutine of its own.
func (tr lateTraceTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	trace := httptrace.ContextClientTrace(r.Context())
	go func() {
		defer close(tr.reported)
		_ = r.Body.Close()
		trace.WroteRequest(httptrace.WroteRequestInfo{})
	}()
	return &http.Response{
		StatusCode: http.StatusRequestEntityTooLarge,
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(`{"detail":"file too large"}`)),
		Request:    r,
	}, nil
}

// A write that the transport reports after the answer is back is read
// without a data race: run under -race, as make test does, this test fails
// if send records the write in a plain variable.
func TestSendReadsALateWriteReportWithoutARace(t *testing.T) {
	t.Parallel()
	transport := lateTraceTransport{reported: make(chan struct{})}
	c := New(Config{BaseURL: "https://api.example", HTTPClient: &http.Client{Transport: transport}, Retry: fastRetry})

	err := c.do(context.Background(), call{method: http.MethodPost, path: "/v1/libraries/lib_1/documents", body: map[string]any{}, audience: workspaceKey}, nil)
	<-transport.reported
	if StatusCode(err) != http.StatusRequestEntityTooLarge {
		t.Errorf("err = %v, want the 413", err)
	}
}

func TestErrorsCarryTheAPIMessage(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		status int
		body   string
		want   string
	}{
		"fastapi string":     {401, `{"detail":"Invalid API Key"}`, "401 Unauthorized: Invalid API Key"},
		"fastapi validation": {422, `{"detail":[{"loc":["body","name"],"msg":"Field required","type":"missing"},{"msg":"bad"}]}`, "422 Unprocessable Entity: body.name: Field required; bad"},
		"observability":      {409, `{"detail":{"message":"Judge exists"}}`, "409 Conflict: Judge exists"},
		"gateway":            {404, `{"message":"no Route matched with those values","request_id":"r1"}`, "404 Not Found: no Route matched with those values"},
		"other object":       {400, `{"detail":{"code":3}}`, `400 Bad Request: {"code":3}`},
		"not json":           {502, `<html>bad gateway</html>`, "502 Bad Gateway: <html>bad gateway</html>"},
		"no message":         {500, `{}`, "500 Internal Server Error: {}"},
		"empty detail list":  {400, `{"detail":[]}`, `400 Bad Request: {"detail":[]}`},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := sdktest.NewServer(t)
			srv.Handle("POST /v1/x", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			err := newTestClient(srv).do(context.Background(), call{method: http.MethodPost, path: "/v1/x", audience: workspaceKey}, nil)
			if err == nil {
				t.Fatal("no error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err, tc.want)
			}
			if !strings.Contains(err.Error(), "(request ID sdktest-") {
				t.Errorf("error %q has no request ID", err)
			}
			if StatusCode(err) != tc.status {
				t.Errorf("StatusCode = %d", StatusCode(err))
			}
		})
	}
}

func TestErrorHelpers(t *testing.T) {
	t.Parallel()
	notFound := &Error{StatusCode: 404}
	conflict := &Error{StatusCode: 409, Method: "POST", Path: "/v1/x"}
	if !IsNotFound(notFound) || IsNotFound(conflict) || IsNotFound(errors.New("x")) {
		t.Error("IsNotFound")
	}
	if !IsConflict(conflict) || StatusCode(nil) != 0 {
		t.Error("IsConflict or StatusCode")
	}
	if got := conflict.Error(); got != "mistral: POST /v1/x: 409 Conflict" {
		t.Errorf("Error() = %q", got)
	}
	if got := truncate(strings.Repeat("a", 600)); len(got) != 503 {
		t.Errorf("truncate kept %d characters", len(got))
	}
}

// escapePath escapes every segment after the base, a base it built
// included, and keeps segments that hold dots without being dot segments.
func TestEscapePath(t *testing.T) {
	t.Parallel()
	if got := escapePath("/v1/agents", "a/b c", "versions"); got != "/v1/agents/a%2Fb%20c/versions" {
		t.Errorf("escapePath = %q", got)
	}
	if got := escapePath(escapePath("/v1/connectors", "c 1"), "...", ".x"); got != "/v1/connectors/c%201/.../.x" {
		t.Errorf("a built base and segments that only hold dots = %q", got)
	}
}

// A segment that would move the request elsewhere once the URL is resolved
// gives a path that names the segment and carries the mark send refuses.
func TestEscapePathRefusesSegmentsThatChangeThePath(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		"":                  `/v1/agents/%!(invalid path segment "")`,
		".":                 `/v1/agents/%!(invalid path segment ".")`,
		"..":                `/v1/agents/%!(invalid path segment "..")`,
		"/v1/libraries":     `/v1/agents/%!(invalid path segment "/v1/libraries")`,
		"/../libraries/LIB": `/v1/agents/%!(invalid path segment "/../libraries/LIB")`,
	}
	for segment, want := range tests {
		path := escapePath("/v1/agents", "ag_1", segment, "aliases")
		if path != want {
			t.Errorf("escapePath with %q = %q, want %q", segment, path, want)
		}
		if !strings.Contains(path, invalidSegment) {
			t.Errorf("escapePath with %q = %q, without the mark send refuses", segment, path)
		}
	}
}

// A typed call given such an ID fails with a build error, which is never
// retried, before anything reaches the transport.
func TestAnInvalidPathSegmentIsRefusedBeforeAnythingIsSent(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	transport := &countingTransport{next: http.DefaultTransport}
	c := New(Config{BaseURL: srv.URL, HTTPClient: &http.Client{Transport: transport}, Retry: fastRetry})
	ctx := context.Background()

	calls := map[string]func() error{
		`invalid path segment "/../libraries/LIB"`: func() error { return c.DeleteAgent(ctx, "/../libraries/LIB") },
		`invalid path segment ".."`:                func() error { _, err := c.GetAgent(ctx, ".."); return err },
		`invalid path segment ""`:                  func() error { _, err := c.GetLibrary(ctx, ""); return err },
		`invalid path segment "."`:                 func() error { return c.DeleteDocument(ctx, "lib_1", ".") },
	}
	for want, call := range calls {
		err := call()
		if _, ok := errors.AsType[*buildError](err); !ok {
			t.Errorf("%s: err = %v, want a build error", want, err)
		}
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to contain %s", err, want)
		}
		if err == nil || !strings.Contains(err.Error(), `a path segment is empty, ".", ".." or starts with "/"`) {
			t.Errorf("err = %v, want it to say why the segment is refused", err)
		}
	}
	if got := transport.n.Load(); got != 0 {
		t.Errorf("%d attempts reached the transport, want none", got)
	}
	srv.AssertCalled("", 0)
}

// countingTransport counts the attempts that reach the transport, including
// those that fail before a connection exists.
type countingTransport struct {
	n    atomic.Int64
	next http.RoundTripper
}

func (c *countingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.n.Add(1)
	return c.next.RoundTrip(r)
}

func TestRetries(t *testing.T) {
	t.Parallel()
	tests := map[string]struct {
		method     string
		idempotent bool
		status     int
		faults     int
		wantCalls  int
		wantErr    bool
	}{
		"read retried on 503":              {method: http.MethodGet, status: 503, faults: 2, wantCalls: 3},
		"read gives up after MaxRetries":   {method: http.MethodGet, status: 500, faults: 9, wantCalls: 4, wantErr: true},
		"delete retried on 502":            {method: http.MethodDelete, status: 502, faults: 1, wantCalls: 2},
		"create not retried on 503":        {method: http.MethodPost, status: 503, faults: 1, wantCalls: 1, wantErr: true},
		"create not retried on 500":        {method: http.MethodPost, status: 500, faults: 1, wantCalls: 1, wantErr: true},
		"create retried on 429":            {method: http.MethodPost, status: 429, faults: 2, wantCalls: 3},
		"patch not retried on 504":         {method: http.MethodPatch, status: 504, faults: 1, wantCalls: 1, wantErr: true},
		"idempotent patch retried on 504":  {method: http.MethodPatch, idempotent: true, status: 504, faults: 1, wantCalls: 2},
		"no retry on 400":                  {method: http.MethodGet, status: 400, faults: 1, wantCalls: 1, wantErr: true},
		"no retry on 409 even for a read":  {method: http.MethodGet, status: 409, faults: 1, wantCalls: 1, wantErr: true},
		"upsert put retried on 503":        {method: http.MethodPut, status: 503, faults: 1, wantCalls: 2},
		"create not retried after a reset": {method: http.MethodPost, status: 0, faults: 1, wantCalls: 1, wantErr: true},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := sdktest.NewServer(t)
			route := tc.method + " /v1/x"
			srv.Respond(route, http.StatusOK, map[string]any{"ok": true})
			if tc.status == 0 {
				srv.ResetNext(route, tc.faults)
			} else {
				srv.FailNext(route, tc.status, tc.faults)
			}
			err := newTestClient(srv).do(context.Background(), call{method: tc.method, path: "/v1/x", audience: workspaceKey, idempotent: tc.idempotent}, nil)
			if (err != nil) != tc.wantErr {
				t.Errorf("err = %v, want error %v", err, tc.wantErr)
			}
			srv.AssertCalled(route, tc.wantCalls)
		})
	}
}

func TestCreateRetriedWhenTheRequestWasNeverSent(t *testing.T) {
	t.Parallel()
	transport := &countingTransport{next: http.DefaultTransport}
	c := New(Config{
		BaseURL:    "http://127.0.0.1:1", // nothing listens: the dial fails before anything is written
		HTTPClient: &http.Client{Transport: transport},
		Retry:      fastRetry,
	})
	err := c.do(context.Background(), call{method: http.MethodPost, path: "/v1/x", body: map[string]any{}, audience: workspaceKey}, nil)
	if err == nil {
		t.Fatal("no error")
	}
	if got := transport.n.Load(); got != 4 {
		t.Errorf("%d attempts, want 1 + MaxRetries", got)
	}
}

func TestRetryAfter(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	var calls atomic.Int64
	srv.Handle("POST /v1/x", func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			sdktest.WriteError(w, http.StatusTooManyRequests, "slow down")
			return
		}
		sdktest.WriteJSON(w, http.StatusOK, map[string]any{})
	})
	srv.Handle("POST /v1/long", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "120")
		sdktest.WriteError(w, http.StatusTooManyRequests, "come back in two minutes")
	})
	c := newTestClient(srv)

	if err := c.do(context.Background(), call{method: http.MethodPost, path: "/v1/x", audience: workspaceKey}, nil); err != nil {
		t.Errorf("Retry-After 0: %v", err)
	}
	srv.AssertCalled("POST /v1/x", 2)

	err := c.do(context.Background(), call{method: http.MethodPost, path: "/v1/long", audience: workspaceKey}, nil)
	if StatusCode(err) != http.StatusTooManyRequests || !strings.Contains(err.Error(), "come back in two minutes") {
		t.Errorf("a Retry-After above MaxRetryAfter must return the 429: %v", err)
	}
	srv.AssertCalled("POST /v1/long", 1)
}

func TestRetryAfterParsing(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	tests := map[string]struct {
		in   string
		want time.Duration
		ok   bool
	}{
		"seconds":     {"3", 3 * time.Second, true},
		"negative":    {"-1", 0, false},
		"date":        {now.Add(10 * time.Second).Format(http.TimeFormat), 10 * time.Second, true},
		"past date":   {now.Add(-time.Hour).Format(http.TimeFormat), 0, true},
		"empty":       {"", 0, false},
		"unparseable": {"soon", 0, false},
	}
	for name, tc := range tests {
		if got, ok := retryAfter(tc.in, now); got != tc.want || ok != tc.ok {
			t.Errorf("%s: retryAfter(%q) = %v, %v", name, tc.in, got, ok)
		}
	}
}

func TestBackoffIsCappedAndJittered(t *testing.T) {
	t.Parallel()
	p := RetryPolicy{BaseDelay: 100 * time.Millisecond, MaxDelay: 300 * time.Millisecond}
	for attempt, base := range []time.Duration{100, 200, 300, 300} {
		base *= time.Millisecond
		for range 20 {
			if d := p.backoff(attempt); d < base || d > base+base/4 {
				t.Fatalf("backoff(%d) = %v, want within [%v, %v]", attempt, d, base, base+base/4)
			}
		}
	}
	if d := (RetryPolicy{}).backoff(2); d != 0 {
		t.Errorf("zero policy backoff = %v", d)
	}
}

func TestRetryStopsWhenTheContextEnds(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.FailNext("GET /v1/x", 503, 5)
	srv.Respond("GET /v1/x", http.StatusOK, map[string]any{})
	c := New(Config{BaseURL: srv.URL, Retry: RetryPolicy{MaxRetries: 5, BaseDelay: time.Hour, MaxDelay: time.Hour}})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := c.do(ctx, call{method: http.MethodGet, path: "/v1/x", audience: workspaceKey}, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want the context's", err)
	}
	if err := wait(ctx, 0); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("wait on an ended context = %v", err)
	}
}
