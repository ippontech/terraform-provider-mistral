// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package libraries

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

// Every value of ProcessStatus, and one the specification does not list,
// has an outcome.
func TestOutcomeOf(t *testing.T) {
	t.Parallel()
	for status, want := range map[string]outcome{
		"todo": pending, "in_progress": pending, "waiting_for_capacity": pending, "Running": pending,
		"done": processed, "noop": processed, "self_managed": processed,
		"error": failed, "missing_content": failed,
	} {
		if got := outcomeOf(status); got != want {
			t.Errorf("outcomeOf(%q) = %d, want %d", status, got, want)
		}
	}
}

// statusClient is a client of a fake that answers the status route with
// statuses, one per read.
func statusClient(t *testing.T, statuses ...string) (*client.Client, *sdktest.Server) {
	t.Helper()
	srv := sdktest.NewServer(t)
	srv.Handle("GET /v1/libraries/{library_id}/documents/{document_id}/status", func(w http.ResponseWriter, _ *http.Request) {
		status := statuses[0]
		if len(statuses) > 1 {
			statuses = statuses[1:]
		}
		sdktest.WriteJSON(w, http.StatusOK, map[string]any{"document_id": "doc", "process_status": status})
	})
	return client.New(client.Config{BaseURL: srv.URL, APIKey: sdktest.TestAPIKey}), srv
}

// The wait reads the status until it is no longer pending.
func TestWaitForProcessing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	cl, srv := statusClient(t, "in_progress", "waiting_for_capacity", "done")
	if status, err := waitForProcessing(ctx, cl, "lib", "doc", "todo"); err != nil || status != "done" {
		t.Errorf("waitForProcessing = %q, %v", status, err)
	}
	srv.AssertCalled("", 3)

	// A document processed at once needs no read.
	cl, srv = statusClient(t, "error")
	if status, err := waitForProcessing(ctx, cl, "lib", "doc", "done"); err != nil || status != "done" {
		t.Errorf("waitForProcessing = %q, %v", status, err)
	}
	srv.AssertCalled("", 0)

	// The wait stops at a failure too: the caller reports it.
	cl, _ = statusClient(t, "missing_content")
	if status, err := waitForProcessing(ctx, cl, "lib", "doc", "todo"); err != nil || status != "missing_content" {
		t.Errorf("waitForProcessing = %q, %v", status, err)
	}
}

// The wait returns the error of its context and the last status it read.
func TestWaitForProcessingStopsAtTheDeadline(t *testing.T) {
	t.Parallel()
	cl, _ := statusClient(t, "in_progress")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	status, err := waitForProcessing(ctx, cl, "lib", "doc", "todo")
	if !errors.Is(err, context.DeadlineExceeded) || (status != "in_progress" && status != "todo") {
		t.Errorf("waitForProcessing = %q, %v", status, err)
	}
}

// A read that fails ends the wait with its error.
func TestWaitForProcessingReturnsAReadError(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("GET /v1/libraries/{library_id}/documents/{document_id}/status", http.StatusNotFound, map[string]any{"detail": "Document not found"})
	cl := client.New(client.Config{BaseURL: srv.URL, APIKey: sdktest.TestAPIKey})
	if status, err := waitForProcessing(context.Background(), cl, "lib", "doc", "todo"); !client.IsNotFound(err) || status != "todo" {
		t.Errorf("waitForProcessing = %q, %v", status, err)
	}
}

// A document expires at its expires_at, not after it.
func TestExpired(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	before, after := now.Add(-time.Second), now.Add(time.Second)
	if expired(client.Document{}, now) || expired(client.Document{ExpiresAt: &after}, now) {
		t.Error("a document without an expiry, or before it, has not expired")
	}
	if !expired(client.Document{ExpiresAt: &before}, now) || !expired(client.Document{ExpiresAt: &now}, now) {
		t.Error("a document at or past its expiry has expired")
	}
}

// The file part takes the media type of its extension, or a generic one.
func TestContentType(t *testing.T) {
	t.Parallel()
	if got := contentType("docs/pricing.pdf"); got != "application/pdf" {
		t.Errorf("contentType(pdf) = %q", got)
	}
	if got := contentType("docs/notes.unknown-extension"); got != "application/octet-stream" {
		t.Errorf("contentType(unknown) = %q", got)
	}
}

// An expiry keeps the configured text while the API returns the same
// instant.
func TestInstantState(t *testing.T) {
	t.Parallel()
	got := time.Date(2027, 1, 1, 0, 0, 0, 500, time.UTC)
	prior := types.StringValue("2027-01-01T01:00:00.0000005+01:00")
	if s := instantState(&got, prior); s != prior {
		t.Errorf("same instant: got %s, want the prior text", s)
	}
	if s := instantState(&got, types.StringValue("2027-01-02T00:00:00Z")); s.ValueString() != "2027-01-01T00:00:00.0000005Z" {
		t.Errorf("other instant: got %s", s)
	}
	if s := instantState(&got, types.StringUnknown()); s.ValueString() != "2027-01-01T00:00:00.0000005Z" {
		t.Errorf("unknown prior: got %s", s)
	}
	if !instantState(nil, prior).IsNull() {
		t.Error("no expiry must be null")
	}
}

// Attributes of any JSON type read as strings.
func TestAttributesState(t *testing.T) {
	t.Parallel()
	got := attributesState(map[string]json.RawMessage{"s": json.RawMessage(`"x"`), "n": json.RawMessage(`2`), "l": json.RawMessage(`["a"]`)})
	if got["s"] != "x" || got["n"] != "2" || got["l"] != `["a"]` {
		t.Errorf("attributesState = %v", got)
	}
	if attributesState(map[string]json.RawMessage{}) != nil {
		t.Error("no attributes must be null")
	}
}

// A library answered without owner_type reads as owned by the workspace.
func TestLibraryStateDefaultsTheOwner(t *testing.T) {
	t.Parallel()
	m := libraryState(client.Library{ID: "lib", Name: "n"})
	if m.OwnerType.ValueString() != defaultOwnerType || !m.OwnerID.IsNull() || !m.CreatedAt.IsNull() || !m.Description.IsNull() {
		t.Errorf("libraryState = %+v", m)
	}
}

// The remaining helpers, on the values the fake tests do not reach.
func TestSmallHelpers(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	if (nameFromSource{}).MarkdownDescription(ctx) == "" || (rfc3339{}).MarkdownDescription(ctx) == "" {
		t.Error("the plan modifier and the validator describe themselves")
	}
	if stringPointer(types.StringUnknown()) != nil || *stringPointer(types.StringValue("a")) != "a" {
		t.Error("stringPointer")
	}
	if !optionalTimestamp(nil).IsNull() {
		t.Error("optionalTimestamp(nil) must be null")
	}
	key := shareKey{libraryID: "lib", shareWithType: "Org", shareWithID: "org"}
	if key.importID() != "lib:Org:org" {
		t.Errorf("importID = %s", key.importID())
	}
}
