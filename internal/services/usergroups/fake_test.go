// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package usergroups_test

import (
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

const (
	routeCreateGroup     = "POST /v1/admin/user-groups"
	routeListGroups      = "GET /v1/admin/user-groups"
	routeGetGroup        = "GET /v1/admin/user-groups/{group_uuid}"
	routeUpdateGroup     = "PATCH /v1/admin/user-groups/{group_uuid}"
	routeDeleteGroup     = "DELETE /v1/admin/user-groups/{group_uuid}"
	routeSetRole         = "PATCH /v1/admin/user-groups/{group_uuid}/organization-role"
	routeListMembers     = "GET /v1/admin/user-groups/{group_uuid}/members"
	routeAddMembers      = "POST /v1/admin/user-groups/{group_uuid}/members"
	routeRemoveMembers   = "DELETE /v1/admin/user-groups/{group_uuid}/members"
	routeListWorkspaces  = "GET /v1/admin/user-groups/{group_uuid}/workspaces"
	routeAssignWorkspace = "POST /v1/admin/user-groups/{group_uuid}/workspaces"
	routeUpdateWorkspace = "PATCH /v1/admin/user-groups/{group_uuid}/workspaces/{workspace_uuid}"
	routeRemoveWorkspace = "DELETE /v1/admin/user-groups/{group_uuid}/workspaces/{workspace_uuid}"
)

// fake serves the user groups API of the Admin API
// (https://docs.mistral.ai/api/endpoint/beta/admin/user-groups) over an
// in-memory store of groups, memberships and workspace assignments.
//
// Besides the transport (paths, bodies, 204 and 201 answers without a body,
// pages from 1 with total), it holds what the provider depends on:
//   - a PATCH of a group leaves a null or absent field unchanged, clears the
//     description with "" and echoes it, and replaces parent_group_ids with
//     the list it gets (AdminUpdateUserGroupIn);
//   - a new group has no organization role until PATCH .../organization-role
//     sets one, and is not externally managed;
//   - the lists of a group that does not exist answer 404, and deleting a
//     group deletes its memberships and assignments, since members lose the
//     roles a group gave them when they leave it
//     (https://docs.mistral.ai/admin/identity-access/groups);
//   - search filters the groups by name, as a case-insensitive substring:
//     our reading of "Search term used to filter user groups by name".
type fake struct {
	*sdktest.Server
	groups      *sdktest.Collection
	members     *sdktest.Collection
	assignments *sdktest.Collection

	mu    sync.Mutex
	clock time.Time
}

// newFake returns a fake with no group that serves every route of the user
// groups API.
func newFake(t *testing.T) *fake {
	t.Helper()
	f := &fake{
		Server:      sdktest.NewServer(t),
		groups:      sdktest.NewCollection("uuid"),
		members:     sdktest.NewCollection("key"),
		assignments: sdktest.NewCollection("key"),
		clock:       time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC),
	}
	f.Handle(routeCreateGroup, f.createGroup)
	f.Handle(routeListGroups, f.listGroups)
	f.Handle(routeGetGroup, f.getGroup)
	f.Handle(routeUpdateGroup, f.updateGroup)
	f.Handle(routeDeleteGroup, f.deleteGroup)
	f.Handle(routeSetRole, f.setRole)
	f.Handle(routeListMembers, f.listMembers)
	f.Handle(routeAddMembers, f.addMembers)
	f.Handle(routeRemoveMembers, f.removeMembers)
	f.Handle(routeListWorkspaces, f.listWorkspaces)
	f.Handle(routeAssignWorkspace, f.assignWorkspace)
	f.Handle(routeUpdateWorkspace, f.updateWorkspace)
	f.Handle(routeRemoveWorkspace, f.removeWorkspace)
	return f
}

// now returns the time of the next assignment: one minute after the
// previous one.
func (f *fake) now() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clock = f.clock.Add(time.Minute)
	return f.clock.Format(time.RFC3339)
}

// createGroup stores a new group, with no organization role and not
// externally managed, and answers it.
func (f *fake) createGroup(w http.ResponseWriter, r *http.Request) {
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	if name, _ := body["name"].(string); name == "" {
		sdktest.WriteValidationError(w, []string{"body", "name"}, "Field required")
		return
	}
	parents := body["parent_group_ids"]
	if parents == nil {
		parents = []any{}
	}
	group := f.groups.Create(map[string]any{
		"uuid": sdktest.NewUUID(), "name": body["name"], "description": body["description"],
		"externally_managed": false, "organization_role": nil, "parent_group_ids": parents,
	})
	sdktest.WriteJSON(w, http.StatusOK, group)
}

// listGroups answers the groups whose name contains search, a page at a time.
func (f *fake) listGroups(w http.ResponseWriter, r *http.Request) {
	search := strings.ToLower(r.URL.Query().Get("search"))
	groups := f.groups.List(func(g map[string]any) bool {
		return strings.Contains(strings.ToLower(g["name"].(string)), search)
	})
	writePage(w, r, "items", groups)
}

// getGroup answers a group, or 404.
func (f *fake) getGroup(w http.ResponseWriter, r *http.Request) {
	group, ok := f.groups.Get(r.PathValue("group_uuid"))
	if !ok {
		sdktest.WriteError(w, http.StatusNotFound, "User group not found")
		return
	}
	sdktest.WriteJSON(w, http.StatusOK, group)
}

// updateGroup sets the fields the body gives a value: "omit the field or
// pass null to leave it unchanged".
func (f *fake) updateGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("group_uuid")
	if _, ok := f.groups.Get(id); !ok {
		sdktest.WriteError(w, http.StatusNotFound, "User group not found")
		return
	}
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	patch := map[string]any{}
	for _, k := range []string{"name", "description", "parent_group_ids"} {
		if v, set := body[k]; set && v != nil {
			patch[k] = v
		}
	}
	group, _ := f.groups.Patch(id, patch)
	sdktest.WriteJSON(w, http.StatusOK, group)
}

// deleteGroup deletes a group with its memberships and assignments.
func (f *fake) deleteGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("group_uuid")
	if !f.groups.Delete(id) {
		sdktest.WriteError(w, http.StatusNotFound, "User group not found")
		return
	}
	for _, m := range f.members.List(ofGroup(id)) {
		f.members.Delete(m["key"].(string))
	}
	for _, a := range f.assignments.List(ofGroup(id)) {
		f.assignments.Delete(a["key"].(string))
	}
	w.WriteHeader(http.StatusNoContent)
}

// setRole stores the organization role as the body writes it, and answers
// 204.
func (f *fake) setRole(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("group_uuid")
	if _, ok := f.groups.Get(id); !ok {
		sdktest.WriteError(w, http.StatusNotFound, "User group not found")
		return
	}
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	f.groups.Patch(id, map[string]any{"organization_role": body["organization_role"]})
	w.WriteHeader(http.StatusNoContent)
}

// listMembers answers the members of a group a page at a time, under the
// key members.
func (f *fake) listMembers(w http.ResponseWriter, r *http.Request) {
	id, ok := f.existingGroup(w, r)
	if !ok {
		return
	}
	out := []map[string]any{}
	for _, m := range f.members.List(ofGroup(id)) {
		out = append(out, map[string]any{"user_uuid": m["user_uuid"], "name": m["name"], "email": m["email"]})
	}
	writePage(w, r, "members", out)
}

// addMembers adds the users of the body; a user already in the group stays
// in it, without an error.
func (f *fake) addMembers(w http.ResponseWriter, r *http.Request) {
	id, users, ok := f.memberBody(w, r)
	if !ok {
		return
	}
	for _, u := range users {
		if _, exists := f.members.Get(id + ":" + u); !exists {
			f.members.Put(map[string]any{"key": id + ":" + u, "group_uuid": id, "user_uuid": u, "name": nil, "email": nil})
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// removeMembers removes the users of the body; a user not in the group is
// ignored.
func (f *fake) removeMembers(w http.ResponseWriter, r *http.Request) {
	id, users, ok := f.memberBody(w, r)
	if !ok {
		return
	}
	for _, u := range users {
		f.members.Delete(id + ":" + u)
	}
	w.WriteHeader(http.StatusNoContent)
}

// listWorkspaces answers the assignments of a group a page at a time, under
// the key items.
func (f *fake) listWorkspaces(w http.ResponseWriter, r *http.Request) {
	id, ok := f.existingGroup(w, r)
	if !ok {
		return
	}
	out := []map[string]any{}
	for _, a := range f.assignments.List(ofGroup(id)) {
		out = append(out, assignmentBody(a))
	}
	writePage(w, r, "items", out)
}

// assignWorkspace stores an assignment with the roles of role_names, and
// answers 201 with no body.
func (f *fake) assignWorkspace(w http.ResponseWriter, r *http.Request) {
	id, ok := f.existingGroup(w, r)
	if !ok {
		return
	}
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	workspace, _ := body["workspace_uuid"].(string)
	f.assignments.Put(map[string]any{
		"key": id + ":" + workspace, "group_uuid": id, "workspace_uuid": workspace,
		"workspace_name": "Workspace " + workspace, "roles": roleRefs(body["role_names"]), "created": f.now(),
	})
	w.WriteHeader(http.StatusCreated)
}

// updateWorkspace replaces the roles of an assignment, and answers 204.
func (f *fake) updateWorkspace(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("group_uuid") + ":" + r.PathValue("workspace_uuid")
	if _, ok := f.assignments.Get(key); !ok {
		sdktest.WriteError(w, http.StatusNotFound, "Workspace assignment not found")
		return
	}
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	f.assignments.Patch(key, map[string]any{"roles": roleRefs(body["role_names"])})
	w.WriteHeader(http.StatusNoContent)
}

// removeWorkspace deletes an assignment, or answers 404.
func (f *fake) removeWorkspace(w http.ResponseWriter, r *http.Request) {
	if !f.assignments.Delete(r.PathValue("group_uuid") + ":" + r.PathValue("workspace_uuid")) {
		sdktest.WriteError(w, http.StatusNotFound, "Workspace assignment not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// existingGroup returns the group of the path, or answers 404.
func (f *fake) existingGroup(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("group_uuid")
	if _, ok := f.groups.Get(id); !ok {
		sdktest.WriteError(w, http.StatusNotFound, "User group not found")
		return "", false
	}
	return id, true
}

// memberBody returns the group of the path and the user_uuids of the body.
func (f *fake) memberBody(w http.ResponseWriter, r *http.Request) (string, []string, bool) {
	id, ok := f.existingGroup(w, r)
	if !ok {
		return "", nil, false
	}
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return "", nil, false
	}
	var users []string
	list, _ := body["user_uuids"].([]any)
	for _, u := range list {
		users = append(users, u.(string))
	}
	return id, users, true
}

// writePage answers the page the query asks for, from page 1, with the
// envelope of the user groups lists: {total, <key>, page, page_size}.
func writePage(w http.ResponseWriter, r *http.Request, key string, items []map[string]any) {
	page, size := sdktest.QueryInt(r, "page", 1), sdktest.QueryInt(r, "page_size", 25)
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{
		"total": len(items), key: sdktest.PageOf(items, page, size, 1), "page": page, "page_size": size,
	})
}

// ofGroup keeps the memberships or assignments of the group id.
func ofGroup(id string) func(map[string]any) bool {
	return func(o map[string]any) bool { return o["group_uuid"] == id }
}

// roleRefs turns role_names into the roles of GroupWorkspaceAssignmentOut,
// in the order the client sent them.
func roleRefs(names any) []any {
	list, _ := names.([]any)
	out := make([]any, 0, len(list))
	for _, n := range list {
		out = append(out, map[string]any{"name": n, "uuid": "role-" + n.(string)})
	}
	return out
}

// assignmentBody is an assignment as GroupWorkspaceAssignmentOut, with the
// deprecated role_name and role_uuid of its first role.
func assignmentBody(a map[string]any) map[string]any {
	out := map[string]any{
		"workspace_uuid": a["workspace_uuid"], "workspace_name": a["workspace_name"], "roles": a["roles"],
		"created": a["created"], "role_name": nil, "role_uuid": nil,
	}
	if roles, _ := a["roles"].([]any); len(roles) > 0 {
		first := roles[0].(map[string]any)
		out["role_name"], out["role_uuid"] = first["name"], first["uuid"]
	}
	return out
}

// seedGroup stores a group created outside Terraform, with the defaults of
// a new group under the fields given, and returns its ID.
func (f *fake) seedGroup(fields map[string]any) string {
	g := map[string]any{
		"uuid": sdktest.NewUUID(), "description": nil, "externally_managed": false,
		"organization_role": nil, "parent_group_ids": []any{},
	}
	maps.Copy(g, fields)
	return f.groups.Create(g)["uuid"].(string)
}

// seedMembers adds n users, "usr-000" and on, to a group outside Terraform.
func (f *fake) seedMembers(groupID string, n int) {
	for i := range n {
		user := fmt.Sprintf("usr-%03d", i)
		f.members.Put(map[string]any{
			"key": groupID + ":" + user, "group_uuid": groupID, "user_uuid": user,
			"name": "User " + user, "email": user + "@example.com",
		})
	}
}

// seedAssignments gives a group access to n workspaces, "ws-000" and on,
// outside Terraform, with the roles given.
func (f *fake) seedAssignments(groupID string, n int, roles ...string) {
	names := make([]any, 0, len(roles))
	for _, r := range roles {
		names = append(names, r)
	}
	for i := range n {
		ws := fmt.Sprintf("ws-%03d", i)
		f.assignments.Put(map[string]any{
			"key": groupID + ":" + ws, "group_uuid": groupID, "workspace_uuid": ws,
			"workspace_name": "Workspace " + ws, "roles": roleRefs(names), "created": f.now(),
		})
	}
}

// requestedPages returns the page query parameter of every request to
// route, in order.
func (f *fake) requestedPages(route string) []string {
	var out []string
	for _, r := range f.Requests(route) {
		out = append(out, r.Query.Get("page"))
	}
	return out
}

// assertWalkedPage2 fails the test unless a request to route asked for the
// second page, which a find over more than 100 items must reach.
func (f *fake) assertWalkedPage2(t *testing.T, route string) {
	t.Helper()
	if !slices.Contains(f.requestedPages(route), "2") {
		t.Errorf("no request to %s asked for page 2: %v", route, f.requestedPages(route))
	}
}
