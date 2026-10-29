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

const (
	routeUserGroups         = "GET /v1/admin/user-groups"
	routeUserGroupMembers   = "GET /v1/admin/user-groups/{group_uuid}/members"
	routeUserGroupWorkspace = "GET /v1/admin/user-groups/{group_uuid}/workspaces"
)

// pagedUserGroups serves n items of a user groups list on route, paged by
// page and page_size from page 1, under the key the list uses ("items" or
// "members"), with item(i) as the i-th item.
func pagedUserGroups(srv *sdktest.Server, route, key string, n int, item func(i int) map[string]any) {
	all := make([]map[string]any, n)
	for i := range all {
		all[i] = item(i)
	}
	srv.Handle(route, func(w http.ResponseWriter, r *http.Request) {
		page, size := sdktest.QueryInt(r, "page", 1), sdktest.QueryInt(r, "page_size", 25)
		sdktest.WriteJSON(w, http.StatusOK, map[string]any{
			"total": n, key: sdktest.PageOf(all, page, size, 1), "page": page, "page_size": size,
		})
	})
}

// A group without parents goes with parent_group_ids: [], never null: the
// specification types the field as an array.
func TestCreateUserGroupSendsATopLevelGroupWithAnEmptyParentList(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("POST /v1/admin/user-groups", http.StatusOK, map[string]any{
		"uuid": "grp_1", "name": "Interns", "description": nil, "externally_managed": false, "parent_group_ids": []any{},
	})
	c := newTestClient(srv)

	g, err := c.CreateUserGroup(context.Background(), CreateUserGroupRequest{Name: "Interns"})
	if err != nil || g.ID != "grp_1" || g.Description != nil || *g.ExternallyManaged {
		t.Fatalf("CreateUserGroup = %+v, %v", g, err)
	}
	if _, err := c.CreateUserGroup(context.Background(), CreateUserGroupRequest{
		Name: "Interns", Description: new("Summer interns"), ParentGroupIDs: []string{"grp_0"},
	}); err != nil {
		t.Fatal(err)
	}
	srv.AssertJSONBodies("POST /v1/admin/user-groups",
		`{"name": "Interns", "description": null, "parent_group_ids": []}`,
		`{"name": "Interns", "description": "Summer interns", "parent_group_ids": ["grp_0"]}`)
	srv.AssertCredential("", sdktest.AdminKey)
}

// A create is not replayed after a 5xx: the group may exist.
func TestCreateUserGroupIsSentOnceOnA5xx(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("POST /v1/admin/user-groups", http.StatusOK, map[string]any{"uuid": "grp_1"})
	srv.FailNext("POST /v1/admin/user-groups", http.StatusInternalServerError, 1)

	if _, err := newTestClient(srv).CreateUserGroup(context.Background(), CreateUserGroupRequest{Name: "a"}); StatusCode(err) != http.StatusInternalServerError {
		t.Errorf("err = %v, want the 500", err)
	}
	srv.AssertCalled("POST /v1/admin/user-groups", 1)
}

// The update sends every field with a value, so that it clears what the
// configuration removed, and is not replayed after a 5xx.
func TestGetUpdateDeleteUserGroup(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("GET /v1/admin/user-groups/{group_uuid}", http.StatusOK, map[string]any{
		"uuid": "grp_1", "name": "Interns", "organization_role": "member", "parent_group_ids": []any{"grp_0"},
	})
	srv.Respond("PATCH /v1/admin/user-groups/{group_uuid}", http.StatusOK, map[string]any{"uuid": "grp_1", "name": "Juniors"})
	srv.Respond("DELETE /v1/admin/user-groups/{group_uuid}", http.StatusNoContent, nil)
	srv.Respond("PATCH /v1/admin/user-groups/{group_uuid}/organization-role", http.StatusNoContent, nil)
	c := newTestClient(srv)
	ctx := context.Background()

	if g, err := c.GetUserGroup(ctx, "grp_1"); err != nil || *g.OrganizationRole != "member" || g.ParentGroupIDs[0] != "grp_0" {
		t.Errorf("GetUserGroup = %+v, %v", g, err)
	}
	if g, err := c.UpdateUserGroup(ctx, "grp_1", UpdateUserGroupRequest{Name: "Juniors"}); err != nil || g.Name != "Juniors" {
		t.Errorf("UpdateUserGroup = %+v, %v", g, err)
	}
	if err := c.SetUserGroupOrganizationRole(ctx, "grp_1", "billing_manager"); err != nil {
		t.Error(err)
	}
	if err := c.DeleteUserGroup(ctx, "grp_1"); err != nil {
		t.Error(err)
	}
	srv.FailNext("PATCH /v1/admin/user-groups/{group_uuid}", http.StatusServiceUnavailable, 1)
	if _, err := c.UpdateUserGroup(ctx, "grp_1", UpdateUserGroupRequest{Name: "Juniors"}); StatusCode(err) != http.StatusServiceUnavailable {
		t.Errorf("an update after a 503 must not be replayed: %v", err)
	}

	// The update clears the description with "" and the parents with [].
	srv.AssertJSONBodies("PATCH /v1/admin/user-groups/{group_uuid}",
		`{"name": "Juniors", "description": "", "parent_group_ids": []}`,
		`{"name": "Juniors", "description": "", "parent_group_ids": []}`)
	srv.AssertJSONBodies("PATCH /v1/admin/user-groups/{group_uuid}/organization-role", `{"organization_role": "billing_manager"}`)
	if got := srv.Requests("DELETE /v1/admin/user-groups/{group_uuid}")[0].Path; got != "/v1/admin/user-groups/grp_1" {
		t.Errorf("DELETE path = %s", got)
	}
	srv.AssertCredential("", sdktest.AdminKey)
}

// Every call returns the API error; a list that fails returns no partial
// result.
func TestUserGroupCallsReturnTheAPIError(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("GET /v1/admin/user-groups/{group_uuid}", http.StatusNotFound, map[string]any{"detail": "User group not found"})
	srv.Respond("PATCH /v1/admin/user-groups/{group_uuid}", http.StatusForbidden, map[string]any{"detail": "Externally managed"})
	srv.Respond(routeUserGroups, http.StatusInternalServerError, map[string]any{"detail": "boom"})
	srv.Respond(routeUserGroupMembers, http.StatusNotFound, map[string]any{"detail": "User group not found"})
	srv.Respond(routeUserGroupWorkspace, http.StatusNotFound, map[string]any{"detail": "User group not found"})
	c := newTestClient(srv)
	ctx := context.Background()

	if _, err := c.GetUserGroup(ctx, "grp_1"); !IsNotFound(err) {
		t.Errorf("GetUserGroup err = %v, want a 404", err)
	}
	if _, err := c.UpdateUserGroup(ctx, "grp_1", UpdateUserGroupRequest{}); StatusCode(err) != http.StatusForbidden {
		t.Errorf("UpdateUserGroup err = %v, want a 403", err)
	}
	if got, err := c.ListUserGroups(ctx, ""); StatusCode(err) != http.StatusInternalServerError || got != nil {
		t.Errorf("ListUserGroups = %v, %v, want the 500", got, err)
	}
	if _, err := c.FindUserGroupMember(ctx, "grp_1", "usr_1"); !IsNotFound(err) {
		t.Errorf("FindUserGroupMember err = %v, want a 404", err)
	}
	if _, err := c.FindUserGroupWorkspace(ctx, "grp_1", "ws_1"); !IsNotFound(err) {
		t.Errorf("FindUserGroupWorkspace err = %v, want a 404", err)
	}
	// A read is retried on 5xx: 1 call and 3 replays.
	srv.AssertCalled(routeUserGroups, 4)
}

// The list walks every page, 100 items at a time, with the search on each
// page, and without it when it is empty.
func TestListUserGroupsWalksEveryPageWithTheSearch(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	pagedUserGroups(srv, routeUserGroups, "items", 150, func(i int) map[string]any {
		return map[string]any{"uuid": fmt.Sprintf("grp_%d", i), "name": fmt.Sprintf("Engineering %d", i)}
	})
	c := newTestClient(srv)

	got, err := c.ListUserGroups(context.Background(), "Engineering")
	if err != nil || len(got) != 150 || got[149].ID != "grp_149" {
		t.Fatalf("ListUserGroups = %d groups, %v", len(got), err)
	}
	if _, err := c.ListUserGroups(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	srv.AssertQueries(routeUserGroups,
		"page=1&page_size=100&search=Engineering", "page=2&page_size=100&search=Engineering",
		"page=1&page_size=100", "page=2&page_size=100")
}

// A page with null items gives an empty slice, not nil.
func TestListUserGroupsAnswersAnEmptyList(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond(routeUserGroups, http.StatusOK, map[string]any{"total": 0, "items": nil, "page": 1, "page_size": 100})

	got, err := newTestClient(srv).ListUserGroups(context.Background(), "none")
	if err != nil || got == nil || len(got) != 0 {
		t.Errorf("ListUserGroups = %#v, %v", got, err)
	}
}

// The find walks the members until it meets the user, on the second page
// here, and returns nil for a user not in the group. Add and remove send a
// list of one user.
func TestUserGroupMembers(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	pagedUserGroups(srv, routeUserGroupMembers, "members", 120, func(i int) map[string]any {
		return map[string]any{"user_uuid": fmt.Sprintf("usr_%d", i), "name": nil, "email": fmt.Sprintf("u%d@example.com", i)}
	})
	srv.Respond("POST /v1/admin/user-groups/{group_uuid}/members", http.StatusNoContent, nil)
	srv.Respond("DELETE /v1/admin/user-groups/{group_uuid}/members", http.StatusNoContent, nil)
	c := newTestClient(srv)
	ctx := context.Background()

	m, err := c.FindUserGroupMember(ctx, "grp_1", "usr_110")
	if err != nil || m == nil || *m.Email != "u110@example.com" || m.Name != nil {
		t.Fatalf("FindUserGroupMember = %+v, %v", m, err)
	}
	if m, err := c.FindUserGroupMember(ctx, "grp_1", "usr_missing"); err != nil || m != nil {
		t.Errorf("FindUserGroupMember of a missing user = %+v, %v, want nil", m, err)
	}
	srv.AssertQueries(routeUserGroupMembers,
		"page=1&page_size=100", "page=2&page_size=100", "page=1&page_size=100", "page=2&page_size=100")

	if err := c.AddUserGroupMember(ctx, "grp_1", "usr_200"); err != nil {
		t.Error(err)
	}
	if err := c.RemoveUserGroupMember(ctx, "grp_1", "usr_200"); err != nil {
		t.Error(err)
	}
	srv.AssertJSONBodies("POST /v1/admin/user-groups/{group_uuid}/members", `{"user_uuids": ["usr_200"]}`)
	srv.AssertJSONBodies("DELETE /v1/admin/user-groups/{group_uuid}/members", `{"user_uuids": ["usr_200"]}`)
	srv.AssertCredential("", sdktest.AdminKey)
}

// Adding a member is not documented as idempotent: a 5xx is not replayed.
func TestAddUserGroupMemberIsSentOnceOnA5xx(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.FailNext("POST /v1/admin/user-groups/{group_uuid}/members", http.StatusBadGateway, 1)
	srv.Respond("POST /v1/admin/user-groups/{group_uuid}/members", http.StatusNoContent, nil)

	if err := newTestClient(srv).AddUserGroupMember(context.Background(), "grp_1", "usr_1"); StatusCode(err) != http.StatusBadGateway {
		t.Errorf("err = %v, want the 502", err)
	}
	srv.AssertCalled("POST /v1/admin/user-groups/{group_uuid}/members", 1)
}

// The find walks the assignments to the second page. The assignment calls
// send role_names, with workspace_uuid on the POST only.
func TestUserGroupWorkspaces(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	pagedUserGroups(srv, routeUserGroupWorkspace, "items", 101, func(i int) map[string]any {
		return map[string]any{
			"workspace_uuid": fmt.Sprintf("ws_%d", i), "workspace_name": "Production", "created": "2026-10-29T10:00:00Z",
			"roles": []any{map[string]any{"name": "user", "uuid": "role_user"}}, "role_name": "user", "role_uuid": "role_user",
		}
	})
	srv.Respond("POST /v1/admin/user-groups/{group_uuid}/workspaces", http.StatusCreated, nil)
	srv.Respond("PATCH /v1/admin/user-groups/{group_uuid}/workspaces/{workspace_uuid}", http.StatusNoContent, nil)
	srv.Respond("DELETE /v1/admin/user-groups/{group_uuid}/workspaces/{workspace_uuid}", http.StatusNoContent, nil)
	c := newTestClient(srv)
	ctx := context.Background()

	a, err := c.FindUserGroupWorkspace(ctx, "grp_1", "ws_100")
	if err != nil || a == nil || a.Roles[0].Name != "user" || a.WorkspaceName != "Production" || a.Created.Hour() != 10 {
		t.Fatalf("FindUserGroupWorkspace = %+v, %v", a, err)
	}
	if a, err := c.FindUserGroupWorkspace(ctx, "grp_1", "ws_missing"); err != nil || a != nil {
		t.Errorf("FindUserGroupWorkspace of a missing workspace = %+v, %v, want nil", a, err)
	}

	if err := c.AssignUserGroupWorkspace(ctx, "grp_1", "ws_1", []string{"user"}); err != nil {
		t.Error(err)
	}
	if err := c.UpdateUserGroupWorkspace(ctx, "grp_1", "ws_1", []string{"dev", "workspace_admin"}); err != nil {
		t.Error(err)
	}
	if err := c.RemoveUserGroupWorkspace(ctx, "grp_1", "ws_1"); err != nil {
		t.Error(err)
	}
	srv.AssertJSONBodies("POST /v1/admin/user-groups/{group_uuid}/workspaces", `{"workspace_uuid": "ws_1", "role_names": ["user"]}`)
	srv.AssertJSONBodies("PATCH /v1/admin/user-groups/{group_uuid}/workspaces/{workspace_uuid}", `{"role_names": ["dev", "workspace_admin"]}`)
	if got := srv.Requests("DELETE /v1/admin/user-groups/{group_uuid}/workspaces/{workspace_uuid}")[0].Path; got != "/v1/admin/user-groups/grp_1/workspaces/ws_1" {
		t.Errorf("DELETE path = %s", got)
	}
	srv.AssertCredential("", sdktest.AdminKey)
}

// Neither the assignment nor its update is documented as idempotent: a 5xx
// is not replayed.
func TestAssignUserGroupWorkspaceIsSentOnceOnA5xx(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("POST /v1/admin/user-groups/{group_uuid}/workspaces", http.StatusCreated, nil)
	srv.Respond("PATCH /v1/admin/user-groups/{group_uuid}/workspaces/{workspace_uuid}", http.StatusNoContent, nil)
	srv.FailNext("POST /v1/admin/user-groups/{group_uuid}/workspaces", http.StatusInternalServerError, 1)
	srv.FailNext("PATCH /v1/admin/user-groups/{group_uuid}/workspaces/{workspace_uuid}", http.StatusInternalServerError, 1)
	c := newTestClient(srv)
	ctx := context.Background()

	if err := c.AssignUserGroupWorkspace(ctx, "grp_1", "ws_1", []string{"user"}); StatusCode(err) != http.StatusInternalServerError {
		t.Errorf("AssignUserGroupWorkspace err = %v, want the 500", err)
	}
	if err := c.UpdateUserGroupWorkspace(ctx, "grp_1", "ws_1", []string{"user"}); StatusCode(err) != http.StatusInternalServerError {
		t.Errorf("UpdateUserGroupWorkspace err = %v, want the 500", err)
	}
	srv.AssertCalled("POST /v1/admin/user-groups/{group_uuid}/workspaces", 1)
	srv.AssertCalled("PATCH /v1/admin/user-groups/{group_uuid}/workspaces/{workspace_uuid}", 1)
}
