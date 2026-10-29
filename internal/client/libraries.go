// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// Library is a library of documents for retrieval (Library in the
// specification, https://docs.mistral.ai/api/endpoint/beta/libraries). The
// fields the specification marks deprecated (chunk_size, emoji,
// generated_name, generated_description, the member counts and
// org_sharing_role) are not decoded.
type Library struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	// OwnerID is the workspace or the user that owns the library, by
	// OwnerType.
	OwnerID   *string `json:"owner_id"`
	OwnerType string  `json:"owner_type"`
	// NbDocuments and TotalSize follow the documents of the library: they
	// change with every upload and deletion.
	NbDocuments int64     `json:"nb_documents"`
	TotalSize   int64     `json:"total_size"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// CreateLibraryRequest is the body of a create (CreateLibraryRequest). The
// deprecated chunk_size is never sent.
type CreateLibraryRequest struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
	// OwnerType is User or Workspace. The API defaults it to Workspace for an
	// API key, and only a key with the "Private and shared connectors" scope
	// can create a User library.
	OwnerType string `json:"owner_type"`
}

// UpdateLibraryRequest is the body of PATCH /v1/libraries/{library_id}
// (UpdateLibraryRequest). Both fields are sent, a null description included,
// so that an update clears a description the configuration removed.
type UpdateLibraryRequest struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
}

// LibraryShare is one access grant on a library (Sharing). The deprecated
// user_id and org_id are not decoded.
type LibraryShare struct {
	LibraryID string `json:"library_id"`
	// Role is the level of the grant: the request calls it level.
	Role          string  `json:"role"`
	ShareWithType string  `json:"share_with_type"`
	ShareWithUUID *string `json:"share_with_uuid"`
}

// LibraryShareRequest is the body of PUT /v1/libraries/{library_id}/share
// (SharingRequest). The deprecated org_id is never sent.
type LibraryShareRequest struct {
	// Level is Viewer or Editor.
	Level string `json:"level"`
	// ShareWithUUID is the ID of the user, workspace or organization.
	ShareWithUUID string `json:"share_with_uuid"`
	// ShareWithType is User, Workspace or Org.
	ShareWithType string `json:"share_with_type"`
}

// libraryShareTarget is the body of DELETE /v1/libraries/{library_id}/share
// (SharingDelete): the grant to remove, without its level.
type libraryShareTarget struct {
	ShareWithUUID string `json:"share_with_uuid"`
	ShareWithType string `json:"share_with_type"`
}

// Document is a document of a library (Document). The deprecated hash,
// processing_status and tokens_processing_* fields are not decoded, nor are
// the fields the provider does not expose (summary, url, uploaded_by_*).
type Document struct {
	ID        string  `json:"id"`
	LibraryID string  `json:"library_id"`
	Name      string  `json:"name"`
	MimeType  *string `json:"mime_type"`
	Extension *string `json:"extension"`
	// Size is in bytes, by our reading: the specification gives no unit.
	Size          *int64 `json:"size"`
	NumberOfPages *int64 `json:"number_of_pages"`
	// ProcessStatus is one of the ProcessStatus enum: todo, in_progress and
	// waiting_for_capacity while the library processes the document, then
	// done, noop, self_managed, missing_content or error.
	ProcessStatus string `json:"process_status"`
	// Attributes values are any JSON value; the provider writes strings only.
	Attributes map[string]json.RawMessage `json:"attributes"`
	// ExpiresAt is when the API deletes the document, when set.
	ExpiresAt       *time.Time `json:"expires_at"`
	CreatedAt       time.Time  `json:"created_at"`
	LastProcessedAt *time.Time `json:"last_processed_at"`
}

// UpdateDocumentRequest is the body of PATCH
// /v1/libraries/{library_id}/documents/{document_id} (UpdateDocumentRequest).
// Every field is sent, null included, so that an update clears the
// attributes and the expiry the configuration removed.
type UpdateDocumentRequest struct {
	Name       string            `json:"name"`
	Attributes map[string]string `json:"attributes"`
	// ExpiresAt is an RFC 3339 date-time.
	ExpiresAt *string `json:"expires_at"`
}

// DocumentStatus is the processing status of a document (ProcessingStatus).
type DocumentStatus struct {
	DocumentID    string `json:"document_id"`
	ProcessStatus string `json:"process_status"`
}

// DocumentFile is the file of an upload.
type DocumentFile struct {
	// Filename is the name of the file part, which the API takes as the name
	// of the document and reads the extension from.
	Filename string
	// ContentType is the type of the file part; application/octet-stream
	// when empty.
	ContentType string
	// Open returns the content. It is called before each attempt, so that a
	// retried upload sends the whole file again, and a file that cannot be
	// opened fails before anything is sent.
	Open func() (io.ReadCloser, error)
}

// CreateLibrary creates a library: POST /v1/libraries. It is not idempotent.
func (cl *Client) CreateLibrary(ctx context.Context, req CreateLibraryRequest) (*Library, error) {
	var out Library
	if err := cl.do(ctx, call{method: http.MethodPost, path: "/v1/libraries", body: req, audience: workspaceKey}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetLibrary returns a library: GET /v1/libraries/{library_id}.
func (cl *Client) GetLibrary(ctx context.Context, id string) (*Library, error) {
	var out Library
	if err := cl.do(ctx, call{method: http.MethodGet, path: escapePath("/v1/libraries", id), audience: workspaceKey}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateLibrary sets the name and the description of a library: PATCH
// /v1/libraries/{library_id}. The PUT of the same path is deprecated.
func (cl *Client) UpdateLibrary(ctx context.Context, id string, req UpdateLibraryRequest) (*Library, error) {
	var out Library
	if err := cl.do(ctx, call{method: http.MethodPatch, path: escapePath("/v1/libraries", id), body: req, audience: workspaceKey}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteLibrary deletes a library and its documents: DELETE
// /v1/libraries/{library_id}. The API answers 200 with the library today and
// announces 204; the body is not read.
func (cl *Client) DeleteLibrary(ctx context.Context, id string) error {
	return cl.do(ctx, call{method: http.MethodDelete, path: escapePath("/v1/libraries", id), audience: workspaceKey}, nil)
}

// ListLibraryShares returns every access grant on a library: GET
// /v1/libraries/{library_id}/share, which is not paginated.
func (cl *Client) ListLibraryShares(ctx context.Context, libraryID string) ([]LibraryShare, error) {
	var out struct {
		Data []LibraryShare `json:"data"`
	}
	if err := cl.do(ctx, call{method: http.MethodGet, path: escapePath("/v1/libraries", libraryID, "share"), audience: workspaceKey}, &out); err != nil {
		return nil, err
	}
	if out.Data == nil {
		return []LibraryShare{}, nil
	}
	return out.Data, nil
}

// PutLibraryShare grants an entity access to a library, or changes its level:
// PUT /v1/libraries/{library_id}/share, an upsert ("create or update the
// access level of an entity"), retried like a read.
func (cl *Client) PutLibraryShare(ctx context.Context, libraryID string, req LibraryShareRequest) (*LibraryShare, error) {
	var out LibraryShare
	c := call{method: http.MethodPut, path: escapePath("/v1/libraries", libraryID, "share"), body: req, audience: workspaceKey}
	if err := cl.do(ctx, c, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteLibraryShare removes the grant of an entity: DELETE
// /v1/libraries/{library_id}/share, with the entity in the body. The API
// answers 200 with the grant today and announces 204; the body is not read.
func (cl *Client) DeleteLibraryShare(ctx context.Context, libraryID, shareWithType, shareWithUUID string) error {
	body := libraryShareTarget{ShareWithUUID: shareWithUUID, ShareWithType: shareWithType}
	return cl.do(ctx, call{method: http.MethodDelete, path: escapePath("/v1/libraries", libraryID, "share"), body: body, audience: workspaceKey}, nil)
}

// UploadDocument uploads a file to a library: POST
// /v1/libraries/{library_id}/documents, multipart with one file part. The API
// queues the document for processing. It answers 201 with the new document,
// or 200 with the document of the library that has the same content, which
// created tells apart. It is not idempotent.
func (cl *Client) UploadDocument(ctx context.Context, libraryID string, f DocumentFile) (doc *Document, created bool, err error) {
	var out Document
	var status int
	form := &multipartForm{files: []formFile{{field: "file", filename: f.Filename, contentType: f.ContentType, open: f.Open}}}
	c := call{method: http.MethodPost, path: escapePath("/v1/libraries", libraryID, "documents"), form: form, audience: workspaceKey, status: &status}
	if err := cl.do(ctx, c, &out); err != nil {
		return nil, false, err
	}
	return &out, status == http.StatusCreated, nil
}

// GetDocument returns a document of a library: GET
// /v1/libraries/{library_id}/documents/{document_id}.
func (cl *Client) GetDocument(ctx context.Context, libraryID, documentID string) (*Document, error) {
	var out Document
	if err := cl.do(ctx, call{method: http.MethodGet, path: documentPath(libraryID, documentID), audience: workspaceKey}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetDocumentStatus returns the processing status of a document: GET
// /v1/libraries/{library_id}/documents/{document_id}/status.
func (cl *Client) GetDocumentStatus(ctx context.Context, libraryID, documentID string) (*DocumentStatus, error) {
	var out DocumentStatus
	if err := cl.do(ctx, call{method: http.MethodGet, path: documentPath(libraryID, documentID) + "/status", audience: workspaceKey}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateDocument sets the metadata of a document: PATCH
// /v1/libraries/{library_id}/documents/{document_id}. The content of a
// document cannot change; the PUT of the same path is deprecated.
func (cl *Client) UpdateDocument(ctx context.Context, libraryID, documentID string, req UpdateDocumentRequest) (*Document, error) {
	var out Document
	if err := cl.do(ctx, call{method: http.MethodPatch, path: documentPath(libraryID, documentID), body: req, audience: workspaceKey}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteDocument deletes a document from its library and from the search
// index: DELETE /v1/libraries/{library_id}/documents/{document_id}.
func (cl *Client) DeleteDocument(ctx context.Context, libraryID, documentID string) error {
	return cl.do(ctx, call{method: http.MethodDelete, path: documentPath(libraryID, documentID), audience: workspaceKey}, nil)
}

// documentPath is the escaped path of a document of a library.
func documentPath(libraryID, documentID string) string {
	return escapePath("/v1/libraries", libraryID, "documents", documentID)
}
