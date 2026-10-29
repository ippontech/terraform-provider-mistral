// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

// library is the body of the libraries guide
// (https://docs.mistral.ai/studio/search/libraries), deprecated fields
// included.
var library = map[string]any{
	"id": "0197f425-5e85-7353-b8e7-e8b974b9c613", "name": "Mistral Models",
	"created_at": "2025-07-10T11:42:59.230268Z", "updated_at": "2025-07-10T11:42:59.230268Z",
	"owner_id": "6340e568-a546-4c41-9dee-1fbeb80493e1", "owner_type": "Workspace",
	"total_size": 0, "nb_documents": 0, "chunk_size": nil, "emoji": nil,
	"description":    "A simple library with information about Mistral models.",
	"generated_name": nil, "generated_description": nil,
	"explicit_user_members_count": nil, "explicit_workspace_members_count": nil, "org_sharing_role": nil,
}

// A create sends every field, null included, decodes the answer of the
// guide, and is not replayed after a 5xx.
func TestCreateLibrarySendsEveryFieldAndIsSentOnceOnA5xx(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("POST /v1/libraries", http.StatusCreated, library)
	c := newTestClient(srv)
	ctx := context.Background()

	lib, err := c.CreateLibrary(ctx, CreateLibraryRequest{Name: "Mistral Models", OwnerType: "Workspace"})
	if err != nil {
		t.Fatal(err)
	}
	if lib.ID != "0197f425-5e85-7353-b8e7-e8b974b9c613" || lib.OwnerType != "Workspace" || *lib.OwnerID != "6340e568-a546-4c41-9dee-1fbeb80493e1" ||
		lib.CreatedAt.Nanosecond() != 230268000 {
		t.Errorf("library = %+v", lib)
	}
	srv.FailNext("POST /v1/libraries", http.StatusInternalServerError, 1)
	if _, err := c.CreateLibrary(ctx, CreateLibraryRequest{Name: "b", Description: new("d"), OwnerType: "User"}); StatusCode(err) != http.StatusInternalServerError {
		t.Errorf("err = %v, want the 500", err)
	}
	srv.AssertJSONBodies("POST /v1/libraries",
		`{"name": "Mistral Models", "description": null, "owner_type": "Workspace"}`,
		`{"name": "b", "description": "d", "owner_type": "User"}`)
	srv.AssertCredential("", sdktest.APIKey)
}

// An update sends both fields and is not replayed after a 5xx.
func TestGetUpdateDeleteLibrary(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("GET /v1/libraries/{library_id}", http.StatusOK, library)
	srv.Respond("PATCH /v1/libraries/{library_id}", http.StatusOK, library)
	srv.Respond("DELETE /v1/libraries/{library_id}", http.StatusOK, library)
	c := newTestClient(srv)
	ctx := context.Background()

	if lib, err := c.GetLibrary(ctx, "lib_1"); err != nil || lib.Name != "Mistral Models" {
		t.Errorf("GetLibrary = %+v, %v", lib, err)
	}
	if _, err := c.UpdateLibrary(ctx, "lib_1", UpdateLibraryRequest{Name: "n"}); err != nil {
		t.Error(err)
	}
	if err := c.DeleteLibrary(ctx, "lib_1"); err != nil {
		t.Error(err)
	}
	srv.FailNext("PATCH /v1/libraries/{library_id}", http.StatusServiceUnavailable, 1)
	if _, err := c.UpdateLibrary(ctx, "lib_1", UpdateLibraryRequest{Name: "n"}); StatusCode(err) != http.StatusServiceUnavailable {
		t.Errorf("an update after a 503 must not be replayed: %v", err)
	}
	srv.AssertJSONBodies("PATCH /v1/libraries/{library_id}", `{"name": "n", "description": null}`, `{"name": "n", "description": null}`)
	if got := srv.Requests("GET /v1/libraries/{library_id}")[0].Path; got != "/v1/libraries/lib_1" {
		t.Errorf("path = %s", got)
	}
}

// Every call returns the *Error of the API.
func TestLibraryCallsReturnTheAPIError(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("GET /v1/libraries/{library_id}", http.StatusNotFound, map[string]any{"detail": "Library not found"})
	srv.Respond("PATCH /v1/libraries/{library_id}", http.StatusNotFound, map[string]any{"detail": "Library not found"})
	srv.Respond("GET /v1/libraries/{library_id}/share", http.StatusNotFound, map[string]any{"detail": "Library not found"})
	srv.Respond("PUT /v1/libraries/{library_id}/share", http.StatusForbidden, map[string]any{"detail": "Not the owner"})
	srv.Respond("POST /v1/libraries/{library_id}/documents", http.StatusNotFound, map[string]any{"detail": "Library not found"})
	srv.Respond("GET /v1/libraries/{library_id}/documents/{document_id}", http.StatusNotFound, map[string]any{"detail": "Document not found"})
	srv.Respond("GET /v1/libraries/{library_id}/documents/{document_id}/status", http.StatusNotFound, map[string]any{"detail": "Document not found"})
	srv.Respond("PATCH /v1/libraries/{library_id}/documents/{document_id}", http.StatusUnprocessableEntity, map[string]any{"detail": "bad"})
	c := newTestClient(srv)
	ctx := context.Background()

	if _, err := c.GetLibrary(ctx, "l"); !IsNotFound(err) {
		t.Errorf("GetLibrary err = %v", err)
	}
	if _, err := c.UpdateLibrary(ctx, "l", UpdateLibraryRequest{}); !IsNotFound(err) {
		t.Errorf("UpdateLibrary err = %v", err)
	}
	if _, err := c.ListLibraryShares(ctx, "l"); !IsNotFound(err) {
		t.Errorf("ListLibraryShares err = %v", err)
	}
	if _, err := c.PutLibraryShare(ctx, "l", LibraryShareRequest{}); StatusCode(err) != http.StatusForbidden {
		t.Errorf("PutLibraryShare err = %v", err)
	}
	if _, _, err := c.UploadDocument(ctx, "l", textFile("a.txt", "a")); !IsNotFound(err) {
		t.Errorf("UploadDocument err = %v", err)
	}
	if _, err := c.GetDocument(ctx, "l", "d"); !IsNotFound(err) {
		t.Errorf("GetDocument err = %v", err)
	}
	if _, err := c.GetDocumentStatus(ctx, "l", "d"); !IsNotFound(err) {
		t.Errorf("GetDocumentStatus err = %v", err)
	}
	if _, err := c.UpdateDocument(ctx, "l", "d", UpdateDocumentRequest{}); StatusCode(err) != http.StatusUnprocessableEntity {
		t.Errorf("UpdateDocument err = %v", err)
	}
}

// The PUT of a grant is an upsert, replayed after a 5xx; the DELETE carries
// the entity in its body.
func TestLibraryShares(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	share := map[string]any{
		"library_id": "lib_1", "user_id": nil, "org_id": "org_1", "role": "Viewer",
		"share_with_type": "Workspace", "share_with_uuid": "ws_1",
	}
	srv.Respond("GET /v1/libraries/{library_id}/share", http.StatusOK, map[string]any{"data": []any{share}})
	srv.Respond("PUT /v1/libraries/{library_id}/share", http.StatusOK, share)
	srv.Respond("DELETE /v1/libraries/{library_id}/share", http.StatusOK, share)
	c := newTestClient(srv)
	ctx := context.Background()

	if got, err := c.ListLibraryShares(ctx, "lib_1"); err != nil || len(got) != 1 || got[0].Role != "Viewer" || *got[0].ShareWithUUID != "ws_1" {
		t.Errorf("ListLibraryShares = %+v, %v", got, err)
	}
	// The PUT is an upsert: a 503 is retried.
	srv.FailNext("PUT /v1/libraries/{library_id}/share", http.StatusServiceUnavailable, 1)
	if got, err := c.PutLibraryShare(ctx, "lib_1", LibraryShareRequest{Level: "Viewer", ShareWithUUID: "ws_1", ShareWithType: "Workspace"}); err != nil || got.Role != "Viewer" {
		t.Errorf("PutLibraryShare = %+v, %v", got, err)
	}
	if err := c.DeleteLibraryShare(ctx, "lib_1", "Workspace", "ws_1"); err != nil {
		t.Error(err)
	}
	want := `{"level": "Viewer", "share_with_uuid": "ws_1", "share_with_type": "Workspace"}`
	srv.AssertJSONBodies("PUT /v1/libraries/{library_id}/share", want, want)
	srv.AssertJSONBodies("DELETE /v1/libraries/{library_id}/share", `{"share_with_uuid": "ws_1", "share_with_type": "Workspace"}`)
}

// A null list of grants reads as an empty one.
func TestListLibrarySharesAnswersNullAsEmpty(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("GET /v1/libraries/{library_id}/share", http.StatusOK, map[string]any{"data": nil})
	got, err := newTestClient(srv).ListLibraryShares(context.Background(), "lib_1")
	if err != nil || got == nil || len(got) != 0 {
		t.Errorf("ListLibraryShares = %#v, %v", got, err)
	}
}

// textFile is a DocumentFile served from memory.
func textFile(name, content string) DocumentFile {
	return DocumentFile{Filename: name, ContentType: "text/plain", Open: func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(content)), nil
	}}
}

// document is the upload answer of the libraries guide.
var document = map[string]any{
	"id": "424fdcb8-3c11-478c-a651-9637be8b4fc4", "library_id": "0197f425-5e85-7353-b8e7-e8b974b9c613",
	"hash": "8ad11d7d6d3a9ce8a0870088ebbcdb00", "mime_type": "application/pdf", "extension": "pdf",
	"size": 3749788, "name": "mistral7b.pdf", "created_at": "2025-07-10T11:43:01.017430Z",
	"process_status": "todo", "processing_status": "Running", "uploaded_by_id": "6340e568-a546-4c41-9dee-1fbeb80493e1",
	"uploaded_by_type": "Workspace", "tokens_processing_total": 0, "summary": nil, "last_processed_at": nil,
	"number_of_pages": nil, "attributes": map[string]any{"team": "support", "year": 2024}, "expires_at": "2027-01-01T00:00:00Z",
}

// An upload answers 201 for a new document and 200 for a document of the
// library with the same content.
func TestUploadDocumentTellsANewDocumentFromAnExistingOne(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	status := http.StatusCreated
	srv.Handle("POST /v1/libraries/{library_id}/documents", func(w http.ResponseWriter, r *http.Request) {
		parts, err := sdktest.ReadParts(r)
		if err != nil || len(parts) != 1 || parts[0].Field != "file" || parts[0].Filename != "guide.txt" || string(parts[0].Content) != "hello" {
			t.Errorf("parts = %+v, %v", parts, err)
		}
		sdktest.WriteJSON(w, status, document)
	})
	c := newTestClient(srv)
	ctx := context.Background()

	doc, created, err := c.UploadDocument(ctx, "lib_1", textFile("guide.txt", "hello"))
	if err != nil || !created {
		t.Fatalf("UploadDocument = %v, %v", created, err)
	}
	if doc.ProcessStatus != "todo" || *doc.Size != 3749788 || string(doc.Attributes["year"]) != "2024" || doc.ExpiresAt.Year() != 2027 || doc.LastProcessedAt != nil {
		t.Errorf("document = %+v", doc)
	}
	status = http.StatusOK
	if _, created, err := c.UploadDocument(ctx, "lib_1", textFile("guide.txt", "hello")); err != nil || created {
		t.Errorf("a 200 must report an existing document: created = %v, %v", created, err)
	}
	if got := srv.Requests("POST /v1/libraries/{library_id}/documents")[0].Path; got != "/v1/libraries/lib_1/documents" {
		t.Errorf("path = %s", got)
	}
}

// An upload is not replayed after a 5xx, and a file that cannot be opened
// sends nothing.
func TestUploadDocumentIsSentOnceOnA5xxAndNotAtAllWithoutItsFile(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("POST /v1/libraries/{library_id}/documents", http.StatusCreated, document)
	srv.FailNext("POST /v1/libraries/{library_id}/documents", http.StatusBadGateway, 1)
	c := newTestClient(srv)
	ctx := context.Background()

	if _, _, err := c.UploadDocument(ctx, "lib_1", textFile("a.txt", "a")); StatusCode(err) != http.StatusBadGateway {
		t.Errorf("err = %v, want the 502", err)
	}
	srv.AssertCalled("POST /v1/libraries/{library_id}/documents", 1)

	missing := DocumentFile{Filename: "gone.pdf", Open: func() (io.ReadCloser, error) { return nil, errors.New("no such file") }}
	if _, _, err := c.UploadDocument(ctx, "lib_1", missing); err == nil || !strings.Contains(err.Error(), "open gone.pdf: no such file") {
		t.Errorf("err = %v", err)
	}
	srv.AssertCalled("POST /v1/libraries/{library_id}/documents", 1)
}

// The document calls use their paths, and the PATCH sends every field, null
// included, without a replay after a 5xx.
func TestDocumentCalls(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("GET /v1/libraries/{library_id}/documents/{document_id}", http.StatusOK, document)
	srv.Respond("GET /v1/libraries/{library_id}/documents/{document_id}/status", http.StatusOK,
		map[string]any{"document_id": "doc_1", "process_status": "done", "processing_status": "Completed"})
	srv.Respond("PATCH /v1/libraries/{library_id}/documents/{document_id}", http.StatusOK, document)
	srv.Respond("DELETE /v1/libraries/{library_id}/documents/{document_id}", http.StatusNoContent, nil)
	c := newTestClient(srv)
	ctx := context.Background()

	if doc, err := c.GetDocument(ctx, "lib_1", "doc_1"); err != nil || doc.Name != "mistral7b.pdf" {
		t.Errorf("GetDocument = %+v, %v", doc, err)
	}
	if st, err := c.GetDocumentStatus(ctx, "lib_1", "doc_1"); err != nil || st.ProcessStatus != "done" || st.DocumentID != "doc_1" {
		t.Errorf("GetDocumentStatus = %+v, %v", st, err)
	}
	if _, err := c.UpdateDocument(ctx, "lib_1", "doc_1", UpdateDocumentRequest{Name: "Guide"}); err != nil {
		t.Error(err)
	}
	srv.FailNext("PATCH /v1/libraries/{library_id}/documents/{document_id}", http.StatusInternalServerError, 1)
	_, err := c.UpdateDocument(ctx, "lib_1", "doc_1", UpdateDocumentRequest{Name: "Guide", Attributes: map[string]string{"team": "a"}, ExpiresAt: new("2027-01-01T00:00:00Z")})
	if StatusCode(err) != http.StatusInternalServerError {
		t.Errorf("an update after a 500 must not be replayed: %v", err)
	}
	if err := c.DeleteDocument(ctx, "lib_1", "doc_1"); err != nil {
		t.Error(err)
	}
	srv.AssertJSONBodies("PATCH /v1/libraries/{library_id}/documents/{document_id}",
		`{"name": "Guide", "attributes": null, "expires_at": null}`,
		`{"name": "Guide", "attributes": {"team": "a"}, "expires_at": "2027-01-01T00:00:00Z"}`)
	for route, want := range map[string]string{
		"GET /v1/libraries/{library_id}/documents/{document_id}/status": "/v1/libraries/lib_1/documents/doc_1/status",
		"DELETE /v1/libraries/{library_id}/documents/{document_id}":     "/v1/libraries/lib_1/documents/doc_1",
	} {
		if got := srv.Requests(route)[0].Path; got != want {
			t.Errorf("%s path = %s, want %s", route, got, want)
		}
	}
	srv.AssertCredential("", sdktest.APIKey)
}

// The status code reaches the caller only on success.
func TestDoReportsTheStatusOfASuccess(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("POST /v1/things", http.StatusAccepted, json.RawMessage(`{}`))
	srv.Respond("POST /v1/fails", http.StatusConflict, map[string]any{"detail": "exists"})
	c := newTestClient(srv)

	status := 0
	if err := c.do(context.Background(), call{method: http.MethodPost, path: "/v1/things", audience: workspaceKey, status: &status}, nil); err != nil || status != http.StatusAccepted {
		t.Errorf("status = %d, %v", status, err)
	}
	status = 0
	if err := c.do(context.Background(), call{method: http.MethodPost, path: "/v1/fails", audience: workspaceKey, status: &status}, nil); !IsConflict(err) || status != 0 {
		t.Errorf("status = %d, %v", status, err)
	}
}
