// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package sdktest is the shared fake of the Mistral API for unit tests: an
// httptest server with a route table, a request recorder, the API's error
// shapes, fault injection, an in-memory collection and the page helpers of
// the three pagination styles.
//
// It imports neither internal/client nor internal/provider, so every package
// can use it in its tests; internal/providertest adds the Terraform side.
//
// Every behaviour follows a page of the API reference or of the
// specification (https://docs.mistral.ai/openapi.yaml), cited where it is
// implemented, and a fake holds only what CLAUDE.md, "What a fake holds",
// allows. Handlers run on server goroutines, so the fake reports a broken
// client with t.Errorf, never t.Fatal, and answers with an error the client
// does not retry (400), so the test ends instead of looping.
package sdktest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// DefaultMaxRequests bounds the requests one Server answers, so a client
// stuck in a loop fails the test quickly.
const DefaultMaxRequests = 500

// Dummy credentials. internal/providertest configures the provider with
// them, so AssertCredential tells them apart.
const (
	TestAPIKey   = "sdktest-api-key"   //nolint:gosec // G101: a dummy value only the fake sees
	TestAdminKey = "sdktest-admin-key" //nolint:gosec // G101: a dummy value only the fake sees
)

// Credential names one of the provider's two keys.
type Credential int

const (
	// APIKey is the standard key (api_key).
	APIKey Credential = iota + 1
	// AdminKey is the Admin API key (admin_api_key).
	AdminKey
)

func (c Credential) String() string {
	switch c {
	case APIKey:
		return "api_key"
	case AdminKey:
		return "admin_api_key"
	}
	return fmt.Sprintf("Credential(%d)", int(c))
}

func (c Credential) key() string {
	if c == AdminKey {
		return TestAdminKey
	}
	return TestAPIKey
}

// Server is an httptest fake of the Mistral API. Routes use the patterns of
// net/http.ServeMux, method included: "POST /v1/agents",
// "GET /v1/agents/{agent_id}". The query string is not part of the route;
// Request.Query keeps it.
//
// A request no route matches fails the test and gets a 404.
type Server struct {
	*httptest.Server

	t   testing.TB
	mux *http.ServeMux

	mu          sync.Mutex
	requests    []Request
	faults      map[string][]fault
	maxRequests int
}

// Request is one request the Server received.
type Request struct {
	Method string
	// Path is the URL path, without the query string.
	Path string
	// Route is the pattern it matched, "" when none did.
	Route  string
	Query  url.Values
	Header http.Header
	Body   []byte
	// Fault is "status 503" or "reset" when FailNext or ResetNext answered
	// it instead of the route's handler.
	Fault string
}

// DecodeJSON unmarshals the request body into v.
func (r Request) DecodeJSON(v any) error {
	return json.Unmarshal(r.Body, v)
}

// Part is one part of a multipart/form-data request.
type Part struct {
	Field    string
	Filename string
	Content  []byte
}

// Parts parses the multipart body of r.
func (r Request) Parts() ([]Part, error) {
	return parseParts(r.Header.Get("Content-Type"), bytes.NewReader(r.Body))
}

// ReadParts parses the multipart body of a handler's request.
func ReadParts(r *http.Request) ([]Part, error) {
	return parseParts(r.Header.Get("Content-Type"), r.Body)
}

func parseParts(contentType string, body io.Reader) ([]Part, error) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "multipart/form-data" {
		return nil, fmt.Errorf("content type %q is not multipart/form-data", contentType)
	}
	mr := multipart.NewReader(body, params["boundary"])
	var parts []Part
	for {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return parts, nil
		}
		if err != nil {
			return nil, err
		}
		content, err := io.ReadAll(p)
		if err != nil {
			return nil, err
		}
		parts = append(parts, Part{Field: p.FormName(), Filename: p.FileName(), Content: content})
	}
}

type fault struct {
	status  int // 0 means a connection reset
	message string
}

// NewServer starts a Server with no route and closes it when the test ends.
func NewServer(tb testing.TB) *Server {
	tb.Helper()
	s := &Server{
		t:           tb,
		mux:         http.NewServeMux(),
		faults:      map[string][]fault{},
		maxRequests: DefaultMaxRequests,
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	tb.Cleanup(s.Close)
	return s
}

// Handle registers h for route. It panics, like http.ServeMux, on a
// malformed or conflicting pattern.
func (s *Server) Handle(route string, h http.HandlerFunc) {
	s.mux.HandleFunc(route, h)
}

// Respond registers a route that always answers status with body.
func (s *Server) Respond(route string, status int, body any) {
	s.Handle(route, func(w http.ResponseWriter, _ *http.Request) {
		WriteJSON(w, status, body)
	})
}

// SetMaxRequests changes DefaultMaxRequests for this server.
func (s *Server) SetMaxRequests(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maxRequests = n
}

// FailNext makes the next n requests to route fail with status, before its
// handler runs. Mind the retries of the client: a read is replayed on 429
// and 5xx, a create on 429 only.
func (s *Server) FailNext(route string, status, n int) {
	s.addFaults(route, fault{status: status}, n)
}

// FailNextWith is FailNext with the detail the API gives, for a test that
// checks the message reaches the user.
func (s *Server) FailNextWith(route string, status int, detail string, n int) {
	s.addFaults(route, fault{status: status, message: detail}, n)
}

// ResetNext drops the connection of the next n requests to route without an
// answer. Mind net/http: its transport silently replays a GET that met a
// reset on a reused keep-alive connection.
func (s *Server) ResetNext(route string, n int) {
	s.addFaults(route, fault{}, n)
}

func (s *Server) addFaults(route string, f fault, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for range n {
		s.faults[route] = append(s.faults[route], f)
	}
}

var requestCounter atomic.Int64

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.t.Errorf("sdktest: read body of %s %s: %v", r.Method, r.URL.Path, err)
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	w.Header().Set("Request-Id", fmt.Sprintf("sdktest-%d", requestCounter.Add(1)))

	_, route := s.mux.Handler(r)
	rec := Request{
		Method: r.Method,
		Path:   r.URL.Path,
		Route:  route,
		Query:  r.URL.Query(),
		Header: r.Header.Clone(),
		Body:   body,
	}

	s.mu.Lock()
	var f *fault
	if queue := s.faults[route]; route != "" && len(queue) > 0 {
		f = &queue[0]
		s.faults[route] = queue[1:]
		rec.Fault = "reset"
		if f.status != 0 {
			rec.Fault = fmt.Sprintf("status %d", f.status)
		}
	}
	s.requests = append(s.requests, rec)
	tooMany := len(s.requests) > s.maxRequests
	s.mu.Unlock()

	switch {
	case tooMany:
		s.t.Errorf("sdktest: more than %d requests (last: %s %s); is the client looping?", s.maxRequests, r.Method, r.URL.Path)
		WriteError(w, http.StatusBadRequest, "sdktest: request budget exhausted")
	case route == "":
		s.t.Errorf("sdktest: unexpected request %s %s: no route matches", r.Method, r.URL.String())
		WriteError(w, http.StatusNotFound, "sdktest: no route for "+r.Method+" "+r.URL.Path)
	case f != nil && f.status != 0:
		message := f.message
		if message == "" {
			message = fmt.Sprintf("sdktest: injected %d", f.status)
		}
		WriteError(w, f.status, message)
	case f != nil:
		s.reset(w, r)
	default:
		s.mux.ServeHTTP(w, r)
	}
}

// reset closes the connection with a TCP RST, so the client sees
// "connection reset by peer" rather than a clean EOF.
func (s *Server) reset(w http.ResponseWriter, r *http.Request) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		s.t.Errorf("sdktest: cannot reset %s %s: no hijacking", r.Method, r.URL.Path)
		return
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		s.t.Errorf("sdktest: hijack %s %s: %v", r.Method, r.URL.Path, err)
		return
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetLinger(0)
	}
	_ = conn.Close()
}

// DecodeBody reads the JSON object of a handler's request, numbers kept as
// json.Number. A body that is not a JSON object is a bug of the client: it
// fails the test and answers 400.
func (s *Server) DecodeBody(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	body := map[string]any{}
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()
	if err := dec.Decode(&body); err != nil {
		s.t.Errorf("sdktest: %s %s: body is not a JSON object: %v", r.Method, r.URL.Path, err)
		WriteError(w, http.StatusBadRequest, "sdktest: body is not a JSON object")
		return nil, false
	}
	return body, true
}

// Requests returns the requests that matched route, in arrival order, or
// every request when route is "".
func (s *Server) Requests(route string) []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Request
	for _, r := range s.requests {
		if route == "" || r.Route == route {
			out = append(out, r)
		}
	}
	return out
}

// AssertCalled fails the test unless exactly n requests matched route,
// faulted ones included. Pass 0 to assert a route was never called.
func (s *Server) AssertCalled(route string, n int) {
	s.t.Helper()
	if got := s.Requests(route); len(got) != n {
		s.t.Errorf("sdktest: %s called %d times, want %d\n%s", route, len(got), n, s.describe())
	}
}

// AssertCredential fails the test unless every request to route, or every
// request when route is "", carried exactly the key of c as a bearer token.
func (s *Server) AssertCredential(route string, c Credential) {
	s.t.Helper()
	want := "Bearer " + c.key()
	for _, r := range s.Requests(route) {
		if got := r.Header.Get("Authorization"); got != want {
			s.t.Errorf("sdktest: %s %s sent Authorization %q, want the %s", r.Method, r.Path, redact(got), c)
		}
		if r.Header.Get("X-Api-Key") != "" {
			s.t.Errorf("sdktest: %s %s sent an x-api-key header besides the bearer token", r.Method, r.Path)
		}
	}
}

// AssertQueries fails the test unless the requests to route carried, in
// order, exactly the encoded query strings of want ("" for none).
func (s *Server) AssertQueries(route string, want ...string) {
	s.t.Helper()
	got := s.Requests(route)
	if len(got) != len(want) {
		s.t.Errorf("sdktest: %s called %d times, want %d\n%s", route, len(got), len(want), s.describe())
		return
	}
	for i, r := range got {
		w := want[i] //nolint:gosec // G602: the lengths are equal, checked above
		if q := r.Query.Encode(); q != w {
			s.t.Errorf("sdktest: request %d to %s has query %q, want %q", i, route, q, w)
		}
	}
}

// AssertJSONBodies fails the test unless the requests to route carried, in
// order, the JSON documents of want, compared as values (JSONDiff).
func (s *Server) AssertJSONBodies(route string, want ...string) {
	s.t.Helper()
	got := s.Requests(route)
	if len(got) != len(want) {
		s.t.Errorf("sdktest: %s called %d times, want %d\n%s", route, len(got), len(want), s.describe())
		return
	}
	for i, r := range got {
		if diff := JSONDiff(string(r.Body), want[i]); diff != "" { //nolint:gosec // G602: the lengths are equal, checked above
			s.t.Errorf("sdktest: body %d of %s differs:\n%s", i, route, diff)
		}
	}
}

func (s *Server) describe() string {
	var b strings.Builder
	b.WriteString("requests received:")
	for _, r := range s.Requests("") {
		fmt.Fprintf(&b, "\n  %s %s", r.Method, r.Path)
		if q := r.Query.Encode(); q != "" {
			b.WriteString("?" + q)
		}
		if r.Fault != "" {
			b.WriteString(" [" + r.Fault + "]")
		}
	}
	return b.String()
}

func redact(v string) string {
	if len(v) > 12 {
		return v[:12] + "..."
	}
	return v
}

// WriteJSON answers status with body encoded as JSON; a nil body sends no
// content.
func WriteJSON(w http.ResponseWriter, status int, body any) {
	if body == nil {
		w.WriteHeader(status)
		return
	}
	b, err := json.Marshal(body)
	if err != nil {
		http.Error(w, "sdktest: the test gave a body that is not JSON: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(b, '\n'))
}

// WriteError answers status with the FastAPI error shape the API uses,
// {"detail": "<detail>"} (a 401 answers {"detail": "Invalid API Key"}).
func WriteError(w http.ResponseWriter, status int, detail string) {
	WriteJSON(w, status, map[string]any{"detail": detail})
}

// WriteValidationError answers 422 with the HTTPValidationError shape of the
// specification: {"detail": [{"loc": [...], "msg": "...", "type": "..."}]}.
func WriteValidationError(w http.ResponseWriter, loc []string, msg string) {
	WriteJSON(w, http.StatusUnprocessableEntity, map[string]any{
		"detail": []any{map[string]any{"loc": loc, "msg": msg, "type": "value_error"}},
	})
}
