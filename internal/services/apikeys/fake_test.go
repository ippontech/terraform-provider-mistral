// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package apikeys_test

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

const (
	routeCreate = "POST /v1/admin/api-keys"
	routeList   = "GET /v1/admin/api-keys"
	routeDelete = "DELETE /v1/admin/api-keys/{key_id}"
)

// today is the date of the fake: the start of every expiration policy.
var today = time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)

// fake serves the API keys of the Admin API
// (https://docs.mistral.ai/admin/identity-access/api-keys) over an in-memory
// organization. Besides the transport, it holds what the provider depends
// on:
//   - only the create returns the secret ("The full key is shown only once");
//   - the list pages by offset with no total, filters by a part of the name
//     or the last 4 characters of the secret, and answers the workspace but
//     not the user a key was created for (APIKeyExtendedOUT);
//   - an expiration policy, when a test sets one, gives a key asked without
//     an expiration date, or beyond the maximum, "the latest expiration date
//     allowed by the policy"
//     (https://docs.mistral.ai/admin/identity-access/api-key-policy).
type fake struct {
	*sdktest.Server
	keys *sdktest.Collection

	mu sync.Mutex
	// maxDays is the maximum validity of the expiration policy, 0 for none.
	maxDays int
	// created counts the keys created, which number their secrets.
	created int
}

// newFake starts the fake with no key and no expiration policy.
func newFake(t *testing.T) *fake {
	t.Helper()
	f := &fake{Server: sdktest.NewServer(t), keys: sdktest.NewCollection("key_id")}
	f.Handle(routeCreate, f.create)
	f.Handle(routeList, f.list)
	f.Handle(routeDelete, f.delete)
	return f
}

// create stores a key with its secret and its expiry, and answers it with the
// secret, the one time the API does.
func (f *fake) create(w http.ResponseWriter, r *http.Request) {
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	if body["workspace_uuid"] == nil || body["user_id"] == nil {
		sdktest.WriteValidationError(w, []string{"body", "workspace_uuid"}, "Field required")
		return
	}
	f.mu.Lock()
	f.created++
	n := f.created
	f.mu.Unlock()
	secret := fmt.Sprintf("fake-secret-%04d", n)
	key := f.keys.Create(map[string]any{
		"name": body["name"], "workspace_id": body["workspace_uuid"], "user_id": body["user_id"],
		"secret":          secret,
		"hidden_key":      "************" + secret[len(secret)-4:],
		"created_at":      today.Add(time.Duration(n) * time.Minute).Format(time.RFC3339),
		"expiration_date": f.expiry(body["expiration"]),
	})
	out := listedKey(key)
	out["key"] = secret
	delete(out, "workspace_id")
	sdktest.WriteJSON(w, http.StatusOK, out)
}

// list pages the keys by offset, limit 100 by default, with no total. Our
// reading of the documented "name substring" match ignores case; accents
// are left aside.
func (f *fake) list(w http.ResponseWriter, r *http.Request) {
	filter := strings.ToLower(r.URL.Query().Get("name"))
	matching := f.keys.List(func(k map[string]any) bool {
		name, _ := k["name"].(string)
		return strings.Contains(strings.ToLower(name), filter) || strings.HasSuffix(k["secret"].(string), filter)
	})
	page := sdktest.OffsetOf(matching, sdktest.QueryInt(r, "offset", 0), sdktest.QueryInt(r, "limit", 100))
	keys := []any{}
	for _, k := range page {
		keys = append(keys, listedKey(k))
	}
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{"keys": keys})
}

// delete deletes the key, or answers 404.
func (f *fake) delete(w http.ResponseWriter, r *http.Request) {
	if !f.keys.Delete(r.PathValue("key_id")) {
		sdktest.WriteError(w, http.StatusNotFound, "API key not found")
		return
	}
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{"detail": "API key deleted"})
}

// expiry is the expiration date of a new key: the date asked, unless a
// policy is active and the date is missing or beyond its maximum.
func (f *fake) expiry(asked any) any {
	f.mu.Lock()
	maxDays := f.maxDays
	f.mu.Unlock()
	if maxDays == 0 {
		return asked
	}
	latest := today.AddDate(0, 0, maxDays).Format(time.DateOnly)
	// Dates in the YYYY-MM-DD format compare as strings.
	if date, _ := asked.(string); date != "" && date <= latest {
		return date
	}
	return latest
}

// setPolicy sets an expiration policy of maxDays days.
func (f *fake) setPolicy(maxDays int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.maxDays = maxDays
}

// seed stores a key created outside Terraform and returns its ID.
func (f *fake) seed(name, workspaceID string) string {
	return f.keys.Create(map[string]any{
		"name": name, "workspace_id": workspaceID, "user_id": "u-studio", "secret": "fake-studio-" + name,
		"hidden_key": "************" + name, "created_at": today.Format(time.RFC3339), "expiration_date": nil,
	})["key_id"].(string)
}

// listedKey is a key as the list answers it (APIKeyExtendedOUT): without
// its secret and without its user.
func listedKey(k map[string]any) map[string]any {
	return map[string]any{
		"key_id": k["key_id"], "key": nil, "name": k["name"], "hidden_key": k["hidden_key"],
		"created_at": k["created_at"], "expiration_date": k["expiration_date"],
		"actions":      map[string]any{"rotate": nil, "delete": map[string]any{"status": "available"}},
		"workspace_id": k["workspace_id"], "workspace_name": nil, "created_by": nil, "product": "API",
		"last_used": nil, "can_delete": true,
	}
}
