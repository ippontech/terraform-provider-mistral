// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

// A create sends an absent name and expiration as null, decodes the secret,
// and is not replayed after a 5xx.
func TestCreateAPIKeySendsNullsAndIsSentOnceOnA5xx(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("POST /v1/admin/api-keys", http.StatusOK, map[string]any{
		"key_id": "k-1", "key": "fake-secret-1", "name": nil, "hidden_key": "****et-1", //gitleaks:allow
		"created_at": "2026-10-29T10:00:00Z", "expiration_date": "2026-11-28",
		"actions": map[string]any{"rotate": nil, "delete": map[string]any{"status": "available"}},
	})
	c := newTestClient(srv)

	k, err := c.CreateAPIKey(context.Background(), CreateAPIKeyRequest{WorkspaceID: "ws-1", UserID: "u-1"})
	if err != nil {
		t.Fatal(err)
	}
	if k.ID != "k-1" || *k.Key != "fake-secret-1" || k.Name != nil || *k.ExpirationDate != "2026-11-28" || k.CreatedAt.Day() != 29 {
		t.Errorf("key = %+v", k)
	}
	srv.AssertJSONBodies("POST /v1/admin/api-keys", `{"name": null, "expiration": null, "workspace_uuid": "ws-1", "user_id": "u-1"}`)
	srv.AssertCredential("", sdktest.AdminKey)

	srv.FailNext("POST /v1/admin/api-keys", http.StatusInternalServerError, 1)
	if _, err := c.CreateAPIKey(context.Background(), CreateAPIKeyRequest{}); StatusCode(err) != http.StatusInternalServerError {
		t.Errorf("err = %v, want the 500", err)
	}
	srv.AssertCalled("POST /v1/admin/api-keys", 2)
}

// A delete is retried after a 5xx, like a read.
func TestDeleteAPIKey(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("DELETE /v1/admin/api-keys/{key_id}", http.StatusOK, map[string]any{"detail": "API key deleted"})
	srv.FailNext("DELETE /v1/admin/api-keys/{key_id}", http.StatusBadGateway, 1)

	if err := newTestClient(srv).DeleteAPIKey(context.Background(), "k-1"); err != nil {
		t.Error(err)
	}
	srv.AssertCalled("DELETE /v1/admin/api-keys/{key_id}", 2)
	if got := srv.Requests("")[1].Path; got != "/v1/admin/api-keys/k-1" {
		t.Errorf("path = %s", got)
	}
}

// keyPages serves n keys named ci-<i>, by offset and limit with no total, as
// GET /v1/admin/api-keys does.
func keyPages(t *testing.T, n int) *sdktest.Server {
	t.Helper()
	all := make([]map[string]any, n)
	for i := range all {
		all[i] = map[string]any{"key_id": fmt.Sprintf("k-%d", i), "name": fmt.Sprintf("ci-%d", i), "workspace_id": "ws-1", "key": nil}
	}
	srv := sdktest.NewServer(t)
	srv.Handle("GET /v1/admin/api-keys", func(w http.ResponseWriter, r *http.Request) {
		page := sdktest.OffsetOf(all, sdktest.QueryInt(r, "offset", 0), sdktest.QueryInt(r, "limit", 100))
		sdktest.WriteJSON(w, http.StatusOK, map[string]any{"keys": page})
	})
	return srv
}

// FindAPIKey walks the offsets, with the name filter when there is one, and
// stops on a short page, since the answer has no total.
func TestFindAPIKeyWalksOffsetsUntilAShortPage(t *testing.T) {
	t.Parallel()
	srv := keyPages(t, 150)
	c := newTestClient(srv)
	ctx := context.Background()

	k, err := c.FindAPIKey(ctx, "ci", func(k APIKey) bool { return k.ID == "k-149" })
	if err != nil || k == nil || *k.WorkspaceID != "ws-1" || *k.Name != "ci-149" || k.Key != nil {
		t.Fatalf("FindAPIKey = %+v, %v", k, err)
	}
	none, err := c.FindAPIKey(ctx, "", func(APIKey) bool { return false })
	if err != nil || none != nil {
		t.Errorf("FindAPIKey without a match = %+v, %v", none, err)
	}
	// The second page holds 50 keys, fewer than the limit: the list ends there.
	srv.AssertQueries("GET /v1/admin/api-keys",
		"limit=100&name=ci&offset=0", "limit=100&name=ci&offset=100",
		"limit=100&offset=0", "limit=100&offset=100")
	srv.AssertCredential("", sdktest.AdminKey)

	srv.FailNext("GET /v1/admin/api-keys", http.StatusForbidden, 1)
	if _, err := c.FindAPIKey(ctx, "", func(APIKey) bool { return true }); StatusCode(err) != http.StatusForbidden {
		t.Errorf("err = %v, want the 403", err)
	}
}
