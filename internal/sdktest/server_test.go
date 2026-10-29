// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package sdktest

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// recordingTB records the errors a Server reports, so a test can check that
// the fake catches a broken client without failing itself.
type recordingTB struct {
	testing.TB
	mu     sync.Mutex
	errors []string
}

func (r *recordingTB) Helper() {}

func (r *recordingTB) Errorf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}

func (r *recordingTB) has(substr string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.errors {
		if strings.Contains(e, substr) {
			return true
		}
	}
	return false
}

func do(t *testing.T, srv *Server, method, path, body string, header map[string]string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		return nil, err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestServerRoutesRecordsAndAsserts(t *testing.T) {
	t.Parallel()
	srv := NewServer(t)
	srv.Respond("POST /v1/things", http.StatusCreated, map[string]any{"id": "1"})
	srv.Handle("GET /v1/things/{id}", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]any{"id": r.PathValue("id")})
	})

	resp, body := do(t, srv, http.MethodPost, "/v1/things?x=1", `{"name":"a"}`, map[string]string{"Authorization": "Bearer " + TestAPIKey})
	if resp.StatusCode != http.StatusCreated || !strings.Contains(body, `"id":"1"`) {
		t.Errorf("POST = %d %s", resp.StatusCode, body)
	}
	if resp.Header.Get("Request-Id") == "" {
		t.Error("no Request-Id header")
	}
	resp, body = do(t, srv, http.MethodGet, "/v1/things/42", "", map[string]string{"Authorization": "Bearer " + TestAdminKey})
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"id":"42"`) {
		t.Errorf("GET = %d %s", resp.StatusCode, body)
	}

	srv.AssertCalled("POST /v1/things", 1)
	srv.AssertQueries("POST /v1/things", "x=1")
	srv.AssertJSONBodies("POST /v1/things", `{"name": "a"}`)
	srv.AssertCredential("POST /v1/things", APIKey)
	srv.AssertCredential("GET /v1/things/{id}", AdminKey)
	var decoded map[string]string
	if err := srv.Requests("POST /v1/things")[0].DecodeJSON(&decoded); err != nil || decoded["name"] != "a" {
		t.Errorf("DecodeJSON = %v, %v", decoded, err)
	}
	if n := len(srv.Requests("")); n != 2 {
		t.Errorf("%d requests recorded", n)
	}
}

func TestServerFaults(t *testing.T) {
	t.Parallel()
	srv := NewServer(t)
	srv.Respond("GET /v1/x", http.StatusOK, map[string]any{})
	srv.FailNext("GET /v1/x", http.StatusServiceUnavailable, 1)
	srv.FailNextWith("GET /v1/x", http.StatusConflict, "already exists", 1)
	srv.ResetNext("GET /v1/x", 1)

	resp, body := do(t, srv, http.MethodGet, "/v1/x", "", nil)
	if resp.StatusCode != http.StatusServiceUnavailable || !strings.Contains(body, "sdktest: injected 503") {
		t.Errorf("first = %d %s", resp.StatusCode, body)
	}
	resp, body = do(t, srv, http.MethodGet, "/v1/x", "", nil)
	if resp.StatusCode != http.StatusConflict || body != "{\"detail\":\"already exists\"}\n" {
		t.Errorf("second = %d %q", resp.StatusCode, body)
	}
	if resp, msg := do(t, srv, http.MethodGet, "/v1/x", "", map[string]string{"Connection": "close"}); resp != nil && resp.StatusCode != http.StatusOK {
		t.Errorf("third = %v %s", resp.StatusCode, msg)
	}
	resp, _ = do(t, srv, http.MethodGet, "/v1/x", "", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("after the faults = %d", resp.StatusCode)
	}
	faults := []string{}
	for _, r := range srv.Requests("GET /v1/x") {
		faults = append(faults, r.Fault)
	}
	if got := strings.Join(faults, ","); !strings.HasPrefix(got, "status 503,status 409,reset") {
		t.Errorf("faults = %q", got)
	}
}

func TestServerCatchesBrokenClients(t *testing.T) {
	t.Parallel()
	rec := &recordingTB{TB: t}
	srv := NewServer(rec)
	srv.Handle("POST /v1/x", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := srv.DecodeBody(w, r); ok {
			WriteJSON(w, http.StatusOK, nil)
		}
	})
	srv.Respond("GET /v1/y", http.StatusOK, map[string]any{})

	if resp, _ := do(t, srv, http.MethodGet, "/v1/nowhere", "", nil); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown route = %d", resp.StatusCode)
	}
	if resp, _ := do(t, srv, http.MethodPost, "/v1/x", "not json", nil); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad body = %d", resp.StatusCode)
	}
	do(t, srv, http.MethodGet, "/v1/y", "", map[string]string{"Authorization": "Bearer wrong-key-long-enough", "X-Api-Key": "k"})
	srv.AssertCredential("GET /v1/y", APIKey)
	srv.AssertCalled("GET /v1/y", 2)
	srv.AssertQueries("GET /v1/y", "a", "b")
	srv.AssertJSONBodies("GET /v1/y", "{}", "{}")
	do(t, srv, http.MethodPost, "/v1/x", `{"a":1}`, nil)
	srv.AssertJSONBodies("POST /v1/x", `{}`, `{"a":2}`)
	do(t, srv, http.MethodGet, "/v1/y", "", nil)
	srv.AssertQueries("GET /v1/y", "q", "q")
	srv.SetMaxRequests(1)
	do(t, srv, http.MethodGet, "/v1/y", "", nil)

	for _, want := range []string{
		"no route matches", "body is not a JSON object", `sent Authorization "Bearer wrong..."`,
		"an x-api-key header", "called 1 times, want 2", "called 1 times, want 2\nrequests received:",
		"body 0 of POST /v1/x differs", "has query \"\", want \"q\"", "more than 1 requests",
	} {
		if !rec.has(want) {
			t.Errorf("no error containing %q in %q", want, rec.errors)
		}
	}
}

func TestMultipartParts(t *testing.T) {
	t.Parallel()
	srv := NewServer(t)
	var got []Part
	srv.Handle("POST /v1/upload", func(w http.ResponseWriter, r *http.Request) {
		parts, err := ReadParts(r)
		if err != nil {
			t.Errorf("ReadParts: %v", err)
		}
		got = parts
		WriteJSON(w, http.StatusOK, map[string]any{})
	})
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("purpose", "ocr")
	fw, _ := mw.CreateFormFile("file", "doc.txt")
	_, _ = fw.Write([]byte("content"))
	_ = mw.Close()

	do(t, srv, http.MethodPost, "/v1/upload", buf.String(), map[string]string{"Content-Type": mw.FormDataContentType()})

	if len(got) != 2 || got[1].Filename != "doc.txt" || string(got[1].Content) != "content" {
		t.Errorf("parts = %+v", got)
	}
	if parts, err := srv.Requests("POST /v1/upload")[0].Parts(); err != nil || len(parts) != 2 {
		t.Errorf("Request.Parts = %v, %v", parts, err)
	}
	if _, err := (Request{Header: http.Header{"Content-Type": {"application/json"}}}).Parts(); err == nil {
		t.Error("Parts of a JSON request must fail")
	}
	if _, err := parseParts("multipart/form-data; boundary=x", strings.NewReader("--x\r\nbroken")); err == nil {
		t.Error("a broken multipart body must fail")
	}
}

func TestErrorWriters(t *testing.T) {
	t.Parallel()
	srv := NewServer(t)
	srv.Handle("POST /v1/v", func(w http.ResponseWriter, _ *http.Request) {
		WriteValidationError(w, []string{"body", "name"}, "Field required")
	})
	resp, body := do(t, srv, http.MethodPost, "/v1/v", "", nil)
	want := `{"detail":[{"loc":["body","name"],"msg":"Field required","type":"value_error"}]}`
	if resp.StatusCode != http.StatusUnprocessableEntity || strings.TrimSpace(body) != want {
		t.Errorf("WriteValidationError = %d %s", resp.StatusCode, body)
	}
}

func TestCredentialString(t *testing.T) {
	t.Parallel()
	for c, want := range map[Credential]string{APIKey: "api_key", AdminKey: "admin_api_key", 0: "Credential(0)"} {
		if got := c.String(); got != want {
			t.Errorf("%d.String() = %q", int(c), got)
		}
	}
	if redact("short") != "short" {
		t.Error("redact changed a short value")
	}
}
