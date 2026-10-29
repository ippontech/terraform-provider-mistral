// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package libraries_test

import (
	"crypto/sha256"
	"encoding/hex"
	"maps"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

const (
	routeCreateLibrary  = "POST /v1/libraries"
	routeGetLibrary     = "GET /v1/libraries/{library_id}"
	routeUpdateLibrary  = "PATCH /v1/libraries/{library_id}"
	routeDeleteLibrary  = "DELETE /v1/libraries/{library_id}"
	routeListShares     = "GET /v1/libraries/{library_id}/share"
	routePutShare       = "PUT /v1/libraries/{library_id}/share"
	routeDeleteShare    = "DELETE /v1/libraries/{library_id}/share"
	routeUpload         = "POST /v1/libraries/{library_id}/documents"
	routeGetDocument    = "GET /v1/libraries/{library_id}/documents/{document_id}"
	routeUpdateDocument = "PATCH /v1/libraries/{library_id}/documents/{document_id}"
	routeDeleteDocument = "DELETE /v1/libraries/{library_id}/documents/{document_id}"
	routeDocumentStatus = "GET /v1/libraries/{library_id}/documents/{document_id}/status"
)

// The owners of the fake: the workspace of the API key (the owner_id of the
// library of the libraries guide), the user a User library belongs to, and
// the organization a grant names in its deprecated org_id.
const (
	workspaceID = "6340e568-a546-4c41-9dee-1fbeb80493e1"
	userID      = "7b1c3f0e-2d4a-4e8b-9c6d-5a4f3e2d1c0b"
	orgID       = "0c9d8e7f-6a5b-4c3d-8e2f-1a0b9c8d7e6f"
)

// fake serves the libraries API (https://docs.mistral.ai/api/endpoint/beta/libraries)
// over an in-memory store. Besides the transport, it holds what the provider
// depends on (https://docs.mistral.ai/studio/search/libraries):
//   - owner_type defaults to Workspace for an API key (CreateLibraryRequest);
//   - nb_documents and total_size follow the documents of the library;
//   - deleting a library deletes its documents and its grants;
//   - the grants of each library, upserted by entity;
//   - an upload answers 201 with a new document, or 200 with the document of
//     the library that has the same content (the 200 response of the upload
//     in the specification);
//   - the processing status moves on at each read of the status, from todo to
//     a final status (default in_progress, then done);
//   - a document past its expires_at is gone ("the document will be
//     automatically deleted after this date").
//
// Its clock moves one minute per write: at(n) is the n-th write.
type fake struct {
	*sdktest.Server
	libraries *sdktest.Collection
	shares    *sdktest.Collection
	documents *sdktest.Collection

	mu    sync.Mutex
	clock time.Time
	// statuses holds, per document ID, the statuses its next status reads
	// return; the last one stays.
	statuses map[string][]string
	// outcomes holds, per file name, the statuses after an upload, which
	// process sets for a test.
	outcomes map[string][]string
}

// newFake starts an empty fake with every route of the libraries API.
func newFake(t *testing.T) *fake {
	t.Helper()
	f := &fake{
		Server:    sdktest.NewServer(t),
		libraries: sdktest.NewCollection("id"),
		shares:    sdktest.NewCollection("key"),
		documents: sdktest.NewCollection("id"),
		clock:     time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC),
		statuses:  map[string][]string{},
		outcomes:  map[string][]string{},
	}
	f.Handle(routeCreateLibrary, f.createLibrary)
	f.Handle(routeGetLibrary, f.getLibrary)
	f.Handle(routeUpdateLibrary, f.updateLibrary)
	f.Handle(routeDeleteLibrary, f.deleteLibrary)
	f.Handle(routeListShares, f.listShares)
	f.Handle(routePutShare, f.putShare)
	f.Handle(routeDeleteShare, f.deleteShare)
	f.Handle(routeUpload, f.upload)
	f.Handle(routeGetDocument, f.getDocument)
	f.Handle(routeUpdateDocument, f.updateDocument)
	f.Handle(routeDeleteDocument, f.deleteDocument)
	f.Handle(routeDocumentStatus, f.documentStatus)
	return f
}

// now returns the time of the next write: one minute after the previous one.
func (f *fake) now() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clock = f.clock.Add(time.Minute)
	return f.clock.Format(time.RFC3339)
}

// at is the time of the n-th write of the fake, from 1.
func at(n int) string {
	return time.Date(2026, 10, 29, 10, n, 0, 0, time.UTC).Format(time.RFC3339)
}

// process makes the uploads of the file name go through statuses, one per
// read of the status, instead of in_progress then done.
func (f *fake) process(filename string, statuses ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.outcomes[filename] = statuses
}

// createLibrary stores a library, owned by the workspace of the key unless
// owner_type is User, and answers 201.
func (f *fake) createLibrary(w http.ResponseWriter, r *http.Request) {
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	name, _ := body["name"].(string)
	if name == "" {
		sdktest.WriteValidationError(w, []string{"body", "name"}, "Field required")
		return
	}
	ownerType, ownerID := "Workspace", workspaceID
	switch body["owner_type"] {
	case nil, "Workspace":
	case "User":
		ownerType, ownerID = "User", userID
	default:
		sdktest.WriteValidationError(w, []string{"body", "owner_type"}, "Input should be 'User' or 'Workspace'")
		return
	}
	now := f.now()
	lib := f.libraries.Create(map[string]any{
		"name": name, "description": body["description"], "owner_type": ownerType, "owner_id": ownerID,
		"created_at": now, "updated_at": now,
		// Deprecated fields, which the guide's answer carries.
		"chunk_size": nil, "emoji": nil, "generated_name": nil, "generated_description": nil,
		"explicit_user_members_count": nil, "explicit_workspace_members_count": nil, "org_sharing_role": nil,
	})
	sdktest.WriteJSON(w, http.StatusCreated, f.libraryBody(lib))
}

// getLibrary answers the library with the counts of its documents.
func (f *fake) getLibrary(w http.ResponseWriter, r *http.Request) {
	lib, ok := f.library(w, r)
	if !ok {
		return
	}
	sdktest.WriteJSON(w, http.StatusOK, f.libraryBody(lib))
}

// updateLibrary sets the name and the description the body carries, null
// included, and refuses any other field, as UpdateLibraryRequest does.
func (f *fake) updateLibrary(w http.ResponseWriter, r *http.Request) {
	lib, ok := f.library(w, r)
	if !ok {
		return
	}
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	for k := range body {
		if k != "name" && k != "description" {
			sdktest.WriteValidationError(w, []string{"body", k}, "Extra inputs are not permitted")
			return
		}
	}
	body["updated_at"] = f.now()
	updated, _ := f.libraries.Patch(lib["id"].(string), body)
	sdktest.WriteJSON(w, http.StatusOK, f.libraryBody(updated))
}

// deleteLibrary answers 200 with the library, which the specification
// announces will become 204, and deletes its documents and grants.
func (f *fake) deleteLibrary(w http.ResponseWriter, r *http.Request) {
	lib, ok := f.library(w, r)
	if !ok {
		return
	}
	id := lib["id"].(string)
	for _, d := range f.documents.List(func(d map[string]any) bool { return d["library_id"] == id }) {
		f.documents.Delete(d["id"].(string))
	}
	for _, s := range f.shares.List(func(s map[string]any) bool { return s["library_id"] == id }) {
		f.shares.Delete(s["key"].(string))
	}
	body := f.libraryBody(lib)
	f.libraries.Delete(id)
	sdktest.WriteJSON(w, http.StatusOK, body)
}

// library returns the library of the path, or answers 404.
func (f *fake) library(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	lib, ok := f.libraries.Get(r.PathValue("library_id"))
	if !ok {
		sdktest.WriteError(w, http.StatusNotFound, "Library not found")
	}
	return lib, ok
}

// libraryBody is the library with the counts of its documents.
func (f *fake) libraryBody(lib map[string]any) map[string]any {
	docs := f.liveDocuments(lib["id"].(string))
	size := 0
	for _, d := range docs {
		size += number(d["size"])
	}
	lib["nb_documents"], lib["total_size"] = len(docs), size
	return lib
}

// listShares answers every grant of the library, unpaginated, under data.
func (f *fake) listShares(w http.ResponseWriter, r *http.Request) {
	lib, ok := f.library(w, r)
	if !ok {
		return
	}
	id := lib["id"].(string)
	data := f.shares.List(func(s map[string]any) bool { return s["library_id"] == id })
	for _, s := range data {
		delete(s, "key")
	}
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{"data": data})
}

// putShare creates the grant of the entity, or changes its level: "create or
// update the access level of an entity".
func (f *fake) putShare(w http.ResponseWriter, r *http.Request) {
	lib, ok := f.library(w, r)
	if !ok {
		return
	}
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	level, entity, uuid := body["level"], body["share_with_type"], body["share_with_uuid"]
	switch {
	case level != "Viewer" && level != "Editor":
		sdktest.WriteValidationError(w, []string{"body", "level"}, "Input should be 'Viewer' or 'Editor'")
		return
	case entity != "User" && entity != "Workspace" && entity != "Org":
		sdktest.WriteValidationError(w, []string{"body", "share_with_type"}, "Input should be 'User', 'Workspace' or 'Org'")
		return
	case uuid == nil || uuid == "":
		sdktest.WriteValidationError(w, []string{"body", "share_with_uuid"}, "Field required")
		return
	}
	share := map[string]any{
		"key":        shareKey(lib["id"].(string), entity.(string), uuid.(string)),
		"library_id": lib["id"], "user_id": nil, "org_id": orgID,
		"role": level, "share_with_type": entity, "share_with_uuid": uuid,
	}
	f.shares.Put(share)
	delete(share, "key")
	sdktest.WriteJSON(w, http.StatusOK, share)
}

// deleteShare removes the grant the body names, and answers 200 with it,
// which the specification announces will become 204.
func (f *fake) deleteShare(w http.ResponseWriter, r *http.Request) {
	lib, ok := f.library(w, r)
	if !ok {
		return
	}
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	entity, _ := body["share_with_type"].(string)
	uuid, _ := body["share_with_uuid"].(string)
	key := shareKey(lib["id"].(string), entity, uuid)
	share, found := f.shares.Get(key)
	if !found {
		sdktest.WriteError(w, http.StatusNotFound, "Sharing not found")
		return
	}
	f.shares.Delete(key)
	delete(share, "key")
	sdktest.WriteJSON(w, http.StatusOK, share)
}

// shareKey is the store key of a grant: one per entity of a library.
func shareKey(libraryID, entity, uuid string) string {
	return libraryID + ":" + entity + ":" + uuid
}

// upload stores the file part as a document of the library, queued for
// processing, unless the library already holds the same content.
func (f *fake) upload(w http.ResponseWriter, r *http.Request) {
	lib, ok := f.library(w, r)
	if !ok {
		return
	}
	parts, err := sdktest.ReadParts(r)
	if err != nil || len(parts) != 1 || parts[0].Field != "file" || parts[0].Filename == "" {
		sdktest.WriteValidationError(w, []string{"body", "file"}, "Expected one file part named file")
		return
	}
	file := parts[0]
	sum := sha256.Sum256(file.Content)
	hash := hex.EncodeToString(sum[:])
	libraryID := lib["id"].(string)
	for _, d := range f.liveDocuments(libraryID) {
		if d["hash"] == hash {
			sdktest.WriteJSON(w, http.StatusOK, d)
			return
		}
	}
	extension := strings.TrimPrefix(filepath.Ext(file.Filename), ".")
	doc := f.documents.Create(map[string]any{
		"library_id": libraryID, "hash": hash, "name": file.Filename,
		"mime_type": mimeTypes[extension], "extension": nilIfEmpty(extension), "size": len(file.Content),
		"created_at": f.now(), "last_processed_at": nil, "number_of_pages": nil, "summary": nil,
		"process_status": "todo", "processing_status": "Running", "tokens_processing_total": 0,
		"tokens_processing_main_content": nil, "tokens_processing_summary": nil,
		"uploaded_by_id": workspaceID, "uploaded_by_type": "Workspace", "url": nil, "attributes": nil, "expires_at": nil,
	})
	f.mu.Lock()
	outcome, set := f.outcomes[file.Filename]
	if !set {
		outcome = []string{"in_progress", "done"}
	}
	f.statuses[doc["id"].(string)] = outcome
	f.mu.Unlock()
	sdktest.WriteJSON(w, http.StatusCreated, doc)
}

// mimeTypes are the media types the fake gives the extensions of the tests.
var mimeTypes = map[string]any{"pdf": "application/pdf", "md": "text/markdown", "txt": "text/plain"}

// getDocument answers the document, 404 once deleted or expired.
func (f *fake) getDocument(w http.ResponseWriter, r *http.Request) {
	doc, ok := f.document(w, r)
	if !ok {
		return
	}
	sdktest.WriteJSON(w, http.StatusOK, doc)
}

// updateDocument sets the name, the attributes and the expiry the body
// carries, null included.
func (f *fake) updateDocument(w http.ResponseWriter, r *http.Request) {
	doc, ok := f.document(w, r)
	if !ok {
		return
	}
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	for k, v := range body {
		switch {
		case !slices.Contains([]string{"name", "attributes", "expires_at"}, k):
			sdktest.WriteValidationError(w, []string{"body", k}, "Extra inputs are not permitted")
			return
		case k == "expires_at" && v != nil && !isTime(v):
			sdktest.WriteValidationError(w, []string{"body", k}, "Input should be a valid datetime")
			return
		}
	}
	updated, _ := f.documents.Patch(doc["id"].(string), body)
	sdktest.WriteJSON(w, http.StatusOK, updated)
}

// deleteDocument deletes the document and answers 204.
func (f *fake) deleteDocument(w http.ResponseWriter, r *http.Request) {
	doc, ok := f.document(w, r)
	if !ok {
		return
	}
	f.documents.Delete(doc["id"].(string))
	w.WriteHeader(http.StatusNoContent)
}

// documentStatus moves the processing on by one status, and fills what the
// processing finds once it is done.
func (f *fake) documentStatus(w http.ResponseWriter, r *http.Request) {
	doc, ok := f.document(w, r)
	if !ok {
		return
	}
	id := doc["id"].(string)
	f.mu.Lock()
	status := doc["process_status"]
	if queue := f.statuses[id]; len(queue) > 0 {
		status, f.statuses[id] = queue[0], queue[1:]
	}
	f.mu.Unlock()
	patch := map[string]any{"process_status": status}
	if status == "done" && doc["last_processed_at"] == nil {
		patch["last_processed_at"], patch["number_of_pages"] = f.now(), 1
	}
	f.documents.Patch(id, patch)
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{"document_id": id, "process_status": status, "processing_status": "Running"})
}

// document returns the live document of the path, in the library of the
// path, or answers 404.
func (f *fake) document(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	if _, ok := f.library(w, r); !ok {
		return nil, false
	}
	doc, ok := f.documents.Get(r.PathValue("document_id"))
	if !ok || doc["library_id"] != r.PathValue("library_id") || !alive(doc) {
		sdktest.WriteError(w, http.StatusNotFound, "Document not found")
		return nil, false
	}
	return doc, true
}

// liveDocuments returns the documents of a library that have not expired.
func (f *fake) liveDocuments(libraryID string) []map[string]any {
	return f.documents.List(func(d map[string]any) bool { return d["library_id"] == libraryID && alive(d) })
}

// alive reports whether a document is before its expiry, if it has one.
func alive(doc map[string]any) bool {
	s, _ := doc["expires_at"].(string)
	if s == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339, s)
	return err != nil || t.After(time.Now())
}

// isTime reports whether v is an RFC 3339 date-time, as a date-time field
// of the API requires.
func isTime(v any) bool {
	s, _ := v.(string)
	_, err := time.Parse(time.RFC3339, s)
	return err == nil
}

// nilIfEmpty is null for the extension of a file name without one.
func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// number reads a JSON number of the store, which DeepCopy keeps as
// json.Number.
func number(v any) int {
	n, ok := v.(interface{ Int64() (int64, error) })
	if !ok {
		return 0
	}
	i, _ := n.Int64()
	return int(i)
}

// seedLibrary stores the library "studio", created outside Terraform in AI
// Studio, and returns its ID.
func (f *fake) seedLibrary() string {
	now := f.now()
	return f.libraries.Create(map[string]any{
		"name": "studio", "description": "Built in AI Studio", "owner_type": "Workspace", "owner_id": workspaceID,
		"created_at": now, "updated_at": now,
	})["id"].(string)
}

// seedDocument stores a processed document uploaded outside Terraform and
// returns its ID.
func (f *fake) seedDocument(libraryID, name, content string, fields map[string]any) string {
	sum := sha256.Sum256([]byte(content))
	now := f.now()
	doc := map[string]any{
		"library_id": libraryID, "hash": hex.EncodeToString(sum[:]), "name": name,
		"mime_type": "text/markdown", "extension": "md", "size": len(content), "number_of_pages": 1,
		"created_at": now, "last_processed_at": now, "process_status": "done", "attributes": nil, "expires_at": nil,
	}
	maps.Copy(doc, fields)
	return f.documents.Create(doc)["id"].(string)
}
