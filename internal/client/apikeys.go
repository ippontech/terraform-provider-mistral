// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// apiKeyPageSize is the limit of each page of GET /v1/admin/api-keys, its
// default in the specification.
const apiKeyPageSize = 100

// APIKey is a workspace API key: APIKeyOUT for a create, APIKeyExtendedOUT
// for a list, which adds the workspace
// (https://docs.mistral.ai/admin/identity-access/api-keys).
type APIKey struct {
	ID string `json:"key_id"`
	// Key is the secret, which only the create returns.
	Key       *string `json:"key"`
	Name      *string `json:"name"`
	HiddenKey string  `json:"hidden_key"`
	// ExpirationDate is a date (YYYY-MM-DD), null for a key that never
	// expires.
	ExpirationDate *string    `json:"expiration_date"`
	CreatedAt      *time.Time `json:"created_at"`
	// WorkspaceID is set by the list only.
	WorkspaceID *string `json:"workspace_id"`
}

// CreateAPIKeyRequest is the body of a create (AdminCreateAPIKeyIN). Every
// field is sent, null included.
type CreateAPIKeyRequest struct {
	Name *string `json:"name"`
	// Expiration is a date (YYYY-MM-DD). When an expiration policy applies,
	// the API sets the latest date it allows instead of a later or missing
	// one (https://docs.mistral.ai/admin/identity-access/api-key-policy).
	Expiration  *string `json:"expiration"`
	WorkspaceID string  `json:"workspace_uuid"`
	// UserID is the user the key is created for.
	UserID string `json:"user_id"`
}

// CreateAPIKey creates a workspace API key and returns it with its secret:
// POST /v1/admin/api-keys. It is not idempotent: a replay after a 5xx could
// create a second key.
func (cl *Client) CreateAPIKey(ctx context.Context, req CreateAPIKeyRequest) (*APIKey, error) {
	var out APIKey
	if err := cl.do(ctx, call{method: http.MethodPost, path: "/v1/admin/api-keys", body: req, audience: adminKey}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteAPIKey deletes an API key, which stops working: DELETE
// /v1/admin/api-keys/{key_id}.
func (cl *Client) DeleteAPIKey(ctx context.Context, id string) error {
	return cl.do(ctx, call{method: http.MethodDelete, path: escapePath("/v1/admin/api-keys", id), audience: adminKey}, nil)
}

// FindAPIKey returns the first API key of the organization that match
// accepts, or nil, walking GET /v1/admin/api-keys by offset. A non-empty name
// keeps the keys whose name contains it, ignoring case and accents, or whose
// secret ends with it. The answer has no total and no cursor: the list ends
// on a page shorter than the limit.
func (cl *Client) FindAPIKey(ctx context.Context, name string, match func(APIKey) bool) (*APIKey, error) {
	fetch := func(ctx context.Context, offset int) ([]APIKey, error) {
		q := url.Values{"limit": {strconv.Itoa(apiKeyPageSize)}, "offset": {strconv.Itoa(offset)}}
		if name != "" {
			q.Set("name", name)
		}
		var out struct {
			Keys []APIKey `json:"keys"`
		}
		err := cl.do(ctx, call{method: http.MethodGet, path: "/v1/admin/api-keys", query: q, audience: adminKey}, &out)
		return out.Keys, err
	}
	return find(func(visit func(APIKey) bool) error { return walkOffset(ctx, apiKeyPageSize, fetch, visit) }, match)
}
