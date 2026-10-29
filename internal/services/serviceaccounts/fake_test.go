// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package serviceaccounts_test

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

const (
	routeCreate          = "POST /v1/service-accounts"
	routeGet             = "GET /v1/service-accounts/{service_account_id}"
	routeUpdate          = "PATCH /v1/service-accounts/{service_account_id}"
	routeDelete          = "DELETE /v1/service-accounts/{service_account_id}"
	routeListRoles       = "GET /v1/service-accounts/{service_account_id}/roles"
	routeSetRoles        = "PUT /v1/service-accounts/{service_account_id}/roles"
	routeAssignableRoles = "GET /v1/service-accounts/assignable-roles"
)

// The workspace and organization of the tests, and three of the workspace
// roles the roles guide names
// (https://docs.mistral.ai/admin/identity-access/roles-permissions).
const (
	workspaceID    = "5d3c4c7e-1f0a-4b8e-9c61-0a6f3e2b9d10"
	organizationID = "0b7d9e2a-6c1f-4a3e-8d5b-2f9c7a1e4b60"
	roleMember     = "11111111-1111-4111-8111-111111111111"
	roleExecutor   = "22222222-2222-4222-8222-222222222222"
	roleDev        = "33333333-3333-4333-8333-333333333333"
)

// fake serves the service accounts API
// (https://docs.mistral.ai/api/endpoint/beta/service_accounts) over an
// in-memory store. Besides the transport, it holds what the provider depends
// on: the roles of each account, which the create sets and the PUT replaces
// as a whole, a name unique in its workspace
// (https://docs.mistral.ai/admin/identity-access/service-accounts), and the
// 422 of a PUT without a role (SetServiceAccountRolesRequest in the
// specification).
type fake struct {
	*sdktest.Server
	accounts *sdktest.Collection
	// roles maps an account ID to the IDs of its roles.
	roles sync.Map
	// assignable maps a workspace ID to its assignable roles; any other
	// workspace has none.
	assignable map[string][]any

	mu    sync.Mutex
	clock time.Time
}

// newFake starts a fake whose workspace can assign the three roles of the
// tests.
func newFake(t *testing.T) *fake {
	t.Helper()
	f := &fake{
		Server:   sdktest.NewServer(t),
		accounts: sdktest.NewCollection("id"),
		assignable: map[string][]any{workspaceID: {
			assignableRole(roleMember, "member", "No role-granted permission.", false),
			assignableRole(roleExecutor, "workflow_executor", "Registers and runs workflows.", false),
			assignableRole(roleDev, "dev", "Access to Studio.", false),
		}},
		clock: time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC),
	}
	f.Handle(routeCreate, f.create)
	f.Handle(routeGet, f.get)
	f.Handle(routeUpdate, f.update)
	f.Handle(routeDelete, f.delete)
	f.Handle(routeListRoles, f.listRoles)
	f.Handle(routeSetRoles, f.setRoles)
	f.Handle(routeAssignableRoles, f.listAssignable)
	return f
}

// assignableRole is an AssignableServiceAccountRole of the specification.
func assignableRole(id, name, description string, custom bool) map[string]any {
	return map[string]any{"uuid": id, "name": name, "description": description, "is_custom_role": custom}
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

// create stores the account and its roles, and answers 409 when the
// workspace has an account by that name. The status of a taken name is not
// documented: 409 is our reading.
func (f *fake) create(w http.ResponseWriter, r *http.Request) {
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	taken := f.accounts.List(func(a map[string]any) bool {
		return a["workspace_id"] == body["workspace_id"] && a["name"] == body["name"]
	})
	if len(taken) > 0 {
		sdktest.WriteError(w, http.StatusConflict, "A service account with this name already exists in the workspace")
		return
	}
	workspace, _ := body["workspace_id"].(string)
	name, _ := body["name"].(string)
	roleIDs, _ := body["role_ids"].([]any)
	id := f.seed(workspace, name, body["description"], roleIDs...)
	account, _ := f.accounts.Get(id)
	sdktest.WriteJSON(w, http.StatusCreated, account)
}

// get answers the account, 404 when it does not exist.
func (f *fake) get(w http.ResponseWriter, r *http.Request) {
	account, ok := f.accounts.Get(r.PathValue("service_account_id"))
	if !ok {
		sdktest.WriteError(w, http.StatusNotFound, "Service account not found")
		return
	}
	sdktest.WriteJSON(w, http.StatusOK, account)
}

// update sets the description of the body, null included.
func (f *fake) update(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("service_account_id")
	if _, ok := f.accounts.Get(id); !ok {
		sdktest.WriteError(w, http.StatusNotFound, "Service account not found")
		return
	}
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	updated, _ := f.accounts.Patch(id, map[string]any{"description": body["description"], "updated_at": f.now()})
	sdktest.WriteJSON(w, http.StatusOK, updated)
}

// delete removes the account and its roles, and answers 204.
func (f *fake) delete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("service_account_id")
	if !f.accounts.Delete(id) {
		sdktest.WriteError(w, http.StatusNotFound, "Service account not found")
		return
	}
	f.roles.Delete(id)
	w.WriteHeader(http.StatusNoContent)
}

// listRoles answers the roles of the account, 404 when it does not exist.
func (f *fake) listRoles(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("service_account_id")
	if _, ok := f.accounts.Get(id); !ok {
		sdktest.WriteError(w, http.StatusNotFound, "Service account not found")
		return
	}
	sdktest.WriteJSON(w, http.StatusOK, f.rolesBody(id))
}

// setRoles replaces the roles of the account with the role_ids of the body,
// which must hold one at least.
func (f *fake) setRoles(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("service_account_id")
	if _, ok := f.accounts.Get(id); !ok {
		sdktest.WriteError(w, http.StatusNotFound, "Service account not found")
		return
	}
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	roleIDs, _ := body["role_ids"].([]any)
	if len(roleIDs) == 0 {
		sdktest.WriteValidationError(w, []string{"body", "role_ids"}, "List should have at least 1 item after validation, not 0")
		return
	}
	f.roles.Store(id, roleIDs)
	sdktest.WriteJSON(w, http.StatusOK, f.rolesBody(id))
}

// listAssignable answers the assignable roles of the workspace_id query.
func (f *fake) listAssignable(w http.ResponseWriter, r *http.Request) {
	items := f.assignable[r.URL.Query().Get("workspace_id")]
	if items == nil {
		items = []any{}
	}
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// rolesBody is the ListServiceAccountRolesResponse of the account.
func (f *fake) rolesBody(id string) map[string]any {
	items := []any{}
	if stored, ok := f.roles.Load(id); ok {
		for _, roleID := range stored.([]any) {
			items = append(items, map[string]any{"role_id": roleID})
		}
	}
	return map[string]any{"items": items}
}

// seed stores an account with its roles, as the create or the Admin Console
// makes it, and returns its ID.
func (f *fake) seed(workspace, name string, description any, roleIDs ...any) string {
	now := f.now()
	account := f.accounts.Create(map[string]any{
		"id": sdktest.NewUUID(), "name": name, "customer_id": organizationID, "organization_id": organizationID,
		"workspace_id": workspace, "description": description, "created_at": now, "updated_at": now, "deleted_at": nil,
	})
	id := account["id"].(string)
	f.roles.Store(id, roleIDs)
	return id
}
