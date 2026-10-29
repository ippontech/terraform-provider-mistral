// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces_test

import (
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

const (
	routeListWorkspaces   = "GET /v1/admin/workspaces"
	routeCreateWorkspace  = "POST /v1/admin/workspaces"
	routeUpdateWorkspace  = "PATCH /v1/admin/workspaces/{workspace_uuid}"
	routeArchiveWorkspace = "DELETE /v1/admin/workspaces/{workspace_uuid}"
	routePutMembers       = "PATCH /v1/admin/workspaces/{workspace_uuid}/users"
	routeRemoveMembers    = "DELETE /v1/admin/workspaces/{workspace_uuid}/remove-users"
	routeGetUser          = "GET /v1/admin/users/{user_id}"
	routeListRoles        = "GET /v1/admin/roles"
)

// roleIDs maps the workspace role names to role UUIDs. The mapping is the
// fake's own: the specification lists the static role UUIDs
// (StaticWorkspaceRoles) without their names, which only GET /v1/admin/roles
// gives.
var roleIDs = map[string]string{
	"user":                  "10000000-0000-4000-8000-000000000001",
	"dev":                   "10000000-0000-4000-8000-000000000002",
	"mistral_code_user":     "10000000-0000-4000-8000-000000000003",
	"billing":               "10000000-0000-4000-8000-000000000004",
	"workspace_contributor": "10000000-0000-4000-8000-000000000005",
	"workspace_admin":       "10000000-0000-4000-8000-000000000006",
	"observability_viewer":  "10000000-0000-4000-8000-000000000007",
}

// fake serves the workspaces of the Admin API
// (https://docs.mistral.ai/admin/admin-api/manage-workspaces) over an
// in-memory organization. Besides the transport, it holds what the provider
// depends on:
//   - the list hides archived workspaces unless is_archived=true, and its
//     answer says nothing of the archive (WorkspacesOut, WorkspaceOUT);
//   - a delete archives the workspace, for good, and refuses the default
//     one ("The default Workspace cannot be archived",
//     https://docs.mistral.ai/admin/workspaces/your-first-workspace);
//   - a create grants the Workspace Admin role to admin_user_id
//     (AdminWorkspaceIn), and adds every user with add_all_org_members;
//   - a membership reads back only through the user (GET
//     /v1/admin/users/{user_id}), with role UUIDs, which GET /v1/admin/roles
//     names.
type fake struct {
	*sdktest.Server
	workspaces *sdktest.Collection
	members    *sdktest.Collection
	users      *sdktest.Collection

	mu sync.Mutex
	// kept are users the next removal leaves in place, a canned refusal for
	// the test of a removal that fails without an error status.
	kept map[string]bool
}

// newFake starts the fake with an empty organization: no workspace, no user.
func newFake(t *testing.T) *fake {
	t.Helper()
	f := &fake{
		Server:     sdktest.NewServer(t),
		workspaces: sdktest.NewCollection("uuid"),
		members:    sdktest.NewCollection("key"),
		users:      sdktest.NewCollection("uuid"),
		kept:       map[string]bool{},
	}
	f.Handle(routeListWorkspaces, f.listWorkspaces)
	f.Handle(routeCreateWorkspace, f.createWorkspace)
	f.Handle(routeUpdateWorkspace, f.updateWorkspace)
	f.Handle(routeArchiveWorkspace, f.archiveWorkspace)
	f.Handle(routePutMembers, f.putMembers)
	f.Handle(routeRemoveMembers, f.removeMembers)
	f.Handle(routeGetUser, f.getUser)
	f.Handle(routeListRoles, f.listRoles)
	return f
}

// listWorkspaces pages the workspaces by number from 1, page_size 25 by
// default, with the total. Our reading of search is a case-insensitive
// substring of the name, and of is_archived=true that it adds the archived
// workspaces to the active ones ("Whether to include archived Workspaces").
func (f *fake) listWorkspaces(w http.ResponseWriter, r *http.Request) {
	search := strings.ToLower(r.URL.Query().Get("search"))
	includeArchived := r.URL.Query().Get("is_archived") == "true"
	matching := f.workspaces.List(func(ws map[string]any) bool {
		return (includeArchived || ws["archived"] != true) && strings.Contains(strings.ToLower(ws["name"].(string)), search)
	})
	page, size := sdktest.QueryInt(r, "page", 1), sdktest.QueryInt(r, "page_size", 25)
	items := []any{}
	for _, ws := range sdktest.PageOf(matching, page, size, 1) {
		items = append(items, workspaceBody(ws))
	}
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{"total": len(matching), "items": items, "object": "list", "page": page, "page_size": size})
}

// createWorkspace stores the workspace with the create defaults, and makes
// admin_user_id, and every user with add_all_org_members, members of it.
func (f *fake) createWorkspace(w http.ResponseWriter, r *http.Request) {
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	if body["name"] == nil || body["admin_user_id"] == nil {
		sdktest.WriteValidationError(w, []string{"body", "name"}, "Field required")
		return
	}
	ws := map[string]any{"name": body["name"], "description": body["description"], "icon": body["icon"], "is_default": false}
	for _, k := range []string{"description", "icon"} {
		if ws[k] == nil {
			ws[k] = ""
		}
	}
	ws = f.workspaces.Create(ws)
	id := ws["uuid"].(string)
	f.setRoles(id, body["admin_user_id"].(string), "workspace_admin")
	if body["add_all_org_members"] == true {
		for _, u := range f.users.List(nil) {
			f.setRoles(id, u["uuid"].(string), "workspace_contributor")
		}
	}
	out := workspaceBody(ws)
	out["raw_roles"], out["raw_role"] = []any{roleIDs["workspace_admin"]}, roleIDs["workspace_admin"]
	sdktest.WriteJSON(w, http.StatusOK, out)
}

// updateWorkspace sets the fields of the body that are not null: our reading
// of UpdateWorkspaceIN, whose fields are all nullable.
func (f *fake) updateWorkspace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("workspace_uuid")
	if _, ok := f.active(id); !ok {
		sdktest.WriteError(w, http.StatusNotFound, "Workspace not found")
		return
	}
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	patch := map[string]any{}
	for _, k := range []string{"name", "description", "icon"} {
		if body[k] != nil {
			patch[k] = body[k]
		}
	}
	ws, _ := f.workspaces.Patch(id, patch)
	sdktest.WriteJSON(w, http.StatusOK, workspaceBody(ws))
}

// archiveWorkspace archives the workspace. Our reading is that an archived
// workspace answers 404, as one that never existed.
func (f *fake) archiveWorkspace(w http.ResponseWriter, r *http.Request) {
	ws, ok := f.active(r.PathValue("workspace_uuid"))
	switch {
	case !ok:
		sdktest.WriteError(w, http.StatusNotFound, "Workspace not found")
	case ws["is_default"] == true:
		sdktest.WriteError(w, http.StatusBadRequest, "The default Workspace cannot be archived")
	default:
		f.archive(ws["uuid"].(string))
		w.WriteHeader(http.StatusNoContent)
	}
}

// putMembers adds each member, or sets the roles of an existing one: the
// roles of the request replace the member's, our reading of "Add or update".
// A role name the organization does not have is a 422, and a user outside
// the organization a 404.
func (f *fake) putMembers(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("workspace_uuid")
	if _, ok := f.active(id); !ok {
		sdktest.WriteError(w, http.StatusNotFound, "Workspace not found")
		return
	}
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	added, updated := 0, 0
	for _, m := range body["members"].([]any) {
		member := m.(map[string]any)
		user := member["user_uuid"].(string)
		if _, ok := f.users.Get(user); !ok {
			sdktest.WriteError(w, http.StatusNotFound, "User not found")
			return
		}
		var names []string
		for _, n := range member["role_names"].([]any) {
			if roleIDs[n.(string)] == "" {
				sdktest.WriteValidationError(w, []string{"body", "members", "role_names"}, "Invalid role name")
				return
			}
			names = append(names, n.(string))
		}
		if _, exists := f.members.Get(id + ":" + user); exists {
			updated++
		} else {
			added++
		}
		f.setRoles(id, user, names...)
	}
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{"added_members_count": added, "updated_members_count": updated})
}

// removeMembers removes each member, and lists the users it did not remove:
// our reading of not_deleted_members includes users that were not members.
func (f *fake) removeMembers(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("workspace_uuid")
	if _, ok := f.active(id); !ok {
		sdktest.WriteError(w, http.StatusNotFound, "Workspace not found")
		return
	}
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	deleted, notDeleted := 0, []any{}
	for _, m := range body["members"].([]any) {
		user := m.(map[string]any)["user_uuid"].(string)
		f.mu.Lock()
		kept := f.kept[user]
		delete(f.kept, user)
		f.mu.Unlock()
		if !kept && f.members.Delete(id+":"+user) {
			deleted++
		} else {
			notDeleted = append(notDeleted, user)
		}
	}
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{"deleted_members_count": deleted, "not_deleted_members": notDeleted})
}

// getUser answers the user with the active workspaces it belongs to
// (AdminUserOUT, MemberWorkspaceInfo).
func (f *fake) getUser(w http.ResponseWriter, r *http.Request) {
	user, ok := f.users.Get(r.PathValue("user_id"))
	if !ok {
		sdktest.WriteError(w, http.StatusNotFound, "User not found")
		return
	}
	workspaces := []any{}
	for _, m := range f.members.List(func(m map[string]any) bool { return m["user_uuid"] == user["uuid"] }) {
		ws, ok := f.active(m["workspace_uuid"].(string))
		if !ok {
			continue
		}
		roles := m["raw_roles"].([]any)
		workspaces = append(workspaces, map[string]any{
			"uuid": ws["uuid"], "name": ws["name"], "is_default": ws["is_default"], "raw_roles": roles, "raw_role": roles[0],
		})
	}
	user["workspaces"] = workspaces
	sdktest.WriteJSON(w, http.StatusOK, user)
}

// listRoles answers the workspace roles of the fake, and one organization
// role.
func (f *fake) listRoles(w http.ResponseWriter, _ *http.Request) {
	roles := []any{}
	for _, name := range slices.Sorted(maps.Keys(roleIDs)) {
		roles = append(roles, map[string]any{"name": name, "uuid": roleIDs[name], "description": name, "is_custom_role": false})
	}
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{
		"workspace_roles":    roles,
		"organization_roles": []any{map[string]any{"name": "member", "uuid": "20000000-0000-4000-8000-000000000001", "description": "", "is_custom_role": false}},
	})
}

// active returns the workspace id unless it is archived or unknown.
func (f *fake) active(id string) (map[string]any, bool) {
	ws, ok := f.workspaces.Get(id)
	if !ok || ws["archived"] == true {
		return nil, false
	}
	return ws, true
}

// setRoles makes user a member of the workspace with the roles names.
func (f *fake) setRoles(workspaceID, user string, names ...string) {
	raw := []any{}
	for _, n := range names {
		raw = append(raw, roleIDs[n])
	}
	f.setRawRoles(workspaceID, user, raw...)
}

// setRawRoles makes user a member of the workspace with the raw roles, as a
// membership reads back: role UUIDs, or the legacy codes A and M.
func (f *fake) setRawRoles(workspaceID, user string, raw ...any) {
	f.members.Put(map[string]any{"key": workspaceID + ":" + user, "workspace_uuid": workspaceID, "user_uuid": user, "raw_roles": raw})
}

// archive archives the workspace, as the Admin Panel does.
func (f *fake) archive(id string) {
	f.workspaces.Patch(id, map[string]any{"archived": true})
}

// keep makes the next removal of user leave it in place.
func (f *fake) keep(user string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.kept[user] = true
}

// addUser adds a member to the organization and returns its ID.
func (f *fake) addUser(email string) string {
	return f.users.Create(map[string]any{"email": email, "name": email, "raw_roles": []any{"20000000-0000-4000-8000-000000000001"}})["uuid"].(string)
}

// seed stores a workspace created outside Terraform and returns its ID.
func (f *fake) seed(name string, isDefault bool) string {
	return f.workspaces.Create(map[string]any{"name": name, "description": "", "icon": nil, "is_default": isDefault})["uuid"].(string)
}

// seedMany stores n workspaces named <prefix>-<i>.
func (f *fake) seedMany(prefix string, n int) {
	for i := range n {
		f.seed(fmt.Sprintf("%s-%03d", prefix, i), false)
	}
}

// workspaceBody is a workspace as the API answers it (WorkspaceOUT), without
// the archive flag of the store.
func workspaceBody(ws map[string]any) map[string]any {
	return map[string]any{
		"uuid": ws["uuid"], "name": ws["name"], "description": ws["description"], "icon": ws["icon"],
		"members_count": nil, "groups_count": nil, "spend_limit": nil, "is_default": ws["is_default"],
	}
}
