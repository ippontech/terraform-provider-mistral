// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Error is a non-2xx answer of the API.
type Error struct {
	StatusCode int
	Method     string
	Path       string
	// Message is what the API said: the detail of a FastAPI error, the
	// message of a gateway error, or the start of the body.
	Message string
	// RequestID is the request-id header, when the API sent one.
	RequestID string
}

func (e *Error) Error() string {
	msg := fmt.Sprintf("mistral: %s %s: %d %s", e.Method, e.Path, e.StatusCode, http.StatusText(e.StatusCode))
	if e.Message != "" {
		msg += ": " + e.Message
	}
	if e.RequestID != "" {
		msg += " (request ID " + e.RequestID + ")"
	}
	return msg
}

// StatusCode returns the HTTP status of an *Error in err's chain, or 0.
func StatusCode(err error) int {
	if apiErr, ok := errors.AsType[*Error](err); ok {
		return apiErr.StatusCode
	}
	return 0
}

// IsNotFound reports whether err is a 404 of the API.
func IsNotFound(err error) bool { return StatusCode(err) == http.StatusNotFound }

// IsConflict reports whether err is a 409 of the API.
func IsConflict(err error) bool { return StatusCode(err) == http.StatusConflict }

func newError(c call, resp *http.Response) *Error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	return &Error{
		StatusCode: resp.StatusCode,
		Method:     c.method,
		Path:       c.path,
		Message:    errorMessage(body),
		RequestID:  firstHeader(resp.Header, "Request-Id", "X-Request-Id", "Mistral-Correlation-Id"),
	}
}

// errorMessage reads the shapes the API answers errors with:
//   - FastAPI validation errors, {"detail": [{"loc": [...], "msg": "...", "type": "..."}]}
//     (HTTPValidationError in the specification, 422);
//   - FastAPI errors, {"detail": "..."} (a 401 says {"detail": "Invalid API Key"});
//   - observability errors, {"detail": {"message": "..."}};
//   - gateway errors, {"message": "no Route matched with those values", "request_id": "..."}.
//
// Anything else gives the start of the body.
func errorMessage(body []byte) string {
	var envelope struct {
		Detail  json.RawMessage `json:"detail"`
		Message string          `json:"message"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return truncate(strings.TrimSpace(string(body)))
	}
	if msg := detailMessage(envelope.Detail); msg != "" {
		return msg
	}
	if envelope.Message != "" {
		return envelope.Message
	}
	return truncate(strings.TrimSpace(string(body)))
}

func detailMessage(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var items []struct {
		Loc []any  `json:"loc"`
		Msg string `json:"msg"`
	}
	if json.Unmarshal(raw, &items) == nil {
		if len(items) == 0 {
			return ""
		}
		parts := make([]string, 0, len(items))
		for _, it := range items {
			loc := make([]string, 0, len(it.Loc))
			for _, l := range it.Loc {
				loc = append(loc, fmt.Sprint(l))
			}
			if len(loc) > 0 {
				parts = append(parts, strings.Join(loc, ".")+": "+it.Msg)
			} else {
				parts = append(parts, it.Msg)
			}
		}
		return strings.Join(parts, "; ")
	}
	var obj struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.Message != "" {
		return obj.Message
	}
	return truncate(string(raw))
}

func truncate(s string) string {
	const limit = 500
	if len(s) > limit {
		return s[:limit] + "..."
	}
	return s
}

func firstHeader(h http.Header, names ...string) string {
	for _, n := range names {
		if v := h.Get(n); v != "" {
			return v
		}
	}
	return ""
}
