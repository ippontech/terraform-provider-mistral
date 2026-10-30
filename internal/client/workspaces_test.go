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

// A create sends every field, defaults included, with the Admin API key, and
// is not replayed after a 5xx.
func TestCreateWorkspaceSendsEveryFieldOnce(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("POST /v1/admin/workspaces", http.StatusOK, map[string]any{
		"uuid": "ws-1", "name": "ML", "description": "", "icon": nil, "is_default": false,
		"raw_roles": []any{"A"}, "raw_role": "A",
	})
	c := newTestClient(srv)

	ws, err := c.CreateWorkspace(context.Background(), CreateWorkspaceRequest{Name: "ML", AdminUserID: "u-1"})
	if err != nil {
		t.Fatal(err)
	}
	if ws.ID != "ws-1" || ws.Name != "ML" || *ws.Description != "" || ws.Icon != nil || ws.IsDefault {
		t.Errorf("workspace = %+v", ws)
	}
	srv.AssertJSONBodies("POST /v1/admin/workspaces",
		`{"name": "ML", "description": "", "icon": "", "add_all_org_members": false, "admin_user_id": "u-1"}`)
	srv.AssertCredential("", sdktest.AdminKey)

	// A create is not replayed after a 5xx: the workspace may exist.
	srv.FailNext("POST /v1/admin/workspaces", http.StatusBadGateway, 1)
	if _, err := c.CreateWorkspace(context.Background(), CreateWorkspaceRequest{Name: "ML"}); StatusCode(err) != http.StatusBadGateway {
		t.Errorf("err = %v, want the 502", err)
	}
	srv.AssertCalled("POST /v1/admin/workspaces", 2)
}

// An update is not replayed after a 5xx; an archive, a DELETE, is.
func TestUpdateAndArchiveWorkspace(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("PATCH /v1/admin/workspaces/{workspace_uuid}", http.StatusOK, map[string]any{"uuid": "ws-1", "name": "Renamed"})
	srv.Respond("DELETE /v1/admin/workspaces/{workspace_uuid}", http.StatusNoContent, nil)
	c := newTestClient(srv)
	ctx := context.Background()

	if ws, err := c.UpdateWorkspace(ctx, "ws-1", UpdateWorkspaceRequest{Name: "Renamed"}); err != nil || ws.Name != "Renamed" {
		t.Errorf("UpdateWorkspace = %+v, %v", ws, err)
	}
	srv.AssertJSONBodies("PATCH /v1/admin/workspaces/{workspace_uuid}", `{"name": "Renamed", "description": "", "icon": ""}`)
	srv.FailNext("PATCH /v1/admin/workspaces/{workspace_uuid}", http.StatusServiceUnavailable, 1)
	if _, err := c.UpdateWorkspace(ctx, "ws-1", UpdateWorkspaceRequest{}); StatusCode(err) != http.StatusServiceUnavailable {
		t.Errorf("an update after a 503 must not be replayed: %v", err)
	}
	srv.AssertCalled("PATCH /v1/admin/workspaces/{workspace_uuid}", 2)

	// A delete is retried like a read.
	srv.FailNext("DELETE /v1/admin/workspaces/{workspace_uuid}", http.StatusServiceUnavailable, 1)
	if err := c.ArchiveWorkspace(ctx, "ws-1"); err != nil {
		t.Error(err)
	}
	srv.AssertCalled("DELETE /v1/admin/workspaces/{workspace_uuid}", 2)
	if got := srv.Requests("DELETE /v1/admin/workspaces/{workspace_uuid}")[0].Path; got != "/v1/admin/workspaces/ws-1" {
		t.Errorf("path = %s", got)
	}
}

// workspacePages serves n workspaces named ws-<i>, paged by number from 1
// with their total, as GET /v1/admin/workspaces does.
func workspacePages(t *testing.T, n int) *sdktest.Server {
	t.Helper()
	all := make([]map[string]any, n)
	for i := range all {
		all[i] = map[string]any{"uuid": fmt.Sprintf("ws-%d", i), "name": fmt.Sprintf("ws-%d", i), "is_default": i == 0}
	}
	srv := sdktest.NewServer(t)
	srv.Handle("GET /v1/admin/workspaces", func(w http.ResponseWriter, r *http.Request) {
		page, size := sdktest.QueryInt(r, "page", 1), sdktest.QueryInt(r, "page_size", 25)
		sdktest.WriteJSON(w, http.StatusOK, map[string]any{
			"total": n, "items": sdktest.PageOf(all, page, size, 1), "object": "list", "page": page, "page_size": size,
		})
	})
	return srv
}

// ListWorkspaces walks the pages by number until it has the total.
func TestListWorkspacesWalksEveryPage(t *testing.T) {
	t.Parallel()
	srv := workspacePages(t, 150)

	got, err := newTestClient(srv).ListWorkspaces(context.Background(), WorkspaceFilter{})
	if err != nil || len(got) != 150 || got[149].ID != "ws-149" || !got[0].IsDefault {
		t.Fatalf("ListWorkspaces = %d workspaces, %v", len(got), err)
	}
	srv.AssertQueries("GET /v1/admin/workspaces", "page=1&page_size=100", "page=2&page_size=100")
	srv.AssertCredential("", sdktest.AdminKey)
}

// Without a total, or with a null one, the walk goes on until a short page:
// 105 workspaces take two pages of 100, for the list and the find. The
// specification marks total as required; this covers an answer that leaves
// it out anyway.
func TestWorkspaceWalksWithoutATotal(t *testing.T) {
	t.Parallel()
	for name, null := range map[string]bool{"missing": false, "null": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := sdktest.NewServer(t)
			pagesWithoutTotal(srv, "GET /v1/admin/workspaces", "items", func(i int) map[string]any {
				return map[string]any{"uuid": fmt.Sprintf("ws-%d", i), "name": fmt.Sprintf("ws-%d", i)}
			}, null)
			c := newTestClient(srv)
			ctx := context.Background()

			if got, err := c.ListWorkspaces(ctx, WorkspaceFilter{}); err != nil || len(got) != 105 || got[104].ID != "ws-104" {
				t.Errorf("ListWorkspaces = %d workspaces, %v, want 105", len(got), err)
			}
			if ws, err := c.FindWorkspace(ctx, WorkspaceFilter{}, func(w Workspace) bool { return w.ID == "ws-104" }); err != nil || ws == nil {
				t.Errorf("FindWorkspace of the last workspace = %+v, %v", ws, err)
			}
			srv.AssertQueries("GET /v1/admin/workspaces",
				"page=1&page_size=100", "page=2&page_size=100", "page=1&page_size=100", "page=2&page_size=100")
		})
	}
}

// With a total, a walk whose last page is full ends there, without reading
// an empty third page.
func TestWorkspaceWalkStopsAtTheTotal(t *testing.T) {
	t.Parallel()
	srv := workspacePages(t, 200)
	if got, err := newTestClient(srv).ListWorkspaces(context.Background(), WorkspaceFilter{}); err != nil || len(got) != 200 {
		t.Fatalf("ListWorkspaces = %d workspaces, %v", len(got), err)
	}
	srv.AssertQueries("GET /v1/admin/workspaces", "page=1&page_size=100", "page=2&page_size=100")
}

// An answer with no workspace is an empty list, not nil.
func TestListWorkspacesAnswersAnEmptyList(t *testing.T) {
	t.Parallel()
	got, err := newTestClient(workspacePages(t, 0)).ListWorkspaces(context.Background(), WorkspaceFilter{})
	if err != nil || got == nil || len(got) != 0 {
		t.Errorf("ListWorkspaces = %#v, %v", got, err)
	}
}

// FindWorkspace sends the search and the archive filter, and stops at the page
// of the match.
func TestFindWorkspaceSendsTheFilterAndStopsAtTheMatch(t *testing.T) {
	t.Parallel()
	srv := workspacePages(t, 250)
	c := newTestClient(srv)
	ctx := context.Background()

	ws, err := c.FindWorkspace(ctx, WorkspaceFilter{Search: "ws 1", IncludeArchived: true}, func(w Workspace) bool { return w.ID == "ws-120" })
	if err != nil || ws == nil || ws.Name != "ws-120" {
		t.Fatalf("FindWorkspace = %+v, %v", ws, err)
	}
	// The match is on page 2: page 3 is never read.
	srv.AssertQueries("GET /v1/admin/workspaces",
		"is_archived=true&page=1&page_size=100&search=ws+1", "is_archived=true&page=2&page_size=100&search=ws+1")

	none, err := c.FindWorkspace(ctx, WorkspaceFilter{}, func(Workspace) bool { return false })
	if err != nil || none != nil {
		t.Errorf("FindWorkspace without a match = %+v, %v", none, err)
	}
	srv.FailNext("GET /v1/admin/workspaces", http.StatusInternalServerError, 4)
	if _, err := c.FindWorkspace(ctx, WorkspaceFilter{}, func(Workspace) bool { return true }); StatusCode(err) != http.StatusInternalServerError {
		t.Errorf("err = %v, want the 500 after the retries", err)
	}
}

// The add-or-update of members is replayed after a 5xx: the documentation
// calls it idempotent.
func TestPutWorkspaceMembersIsAnIdempotentUpsert(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("PATCH /v1/admin/workspaces/{workspace_uuid}/users", http.StatusOK, map[string]any{"added_members_count": 1, "updated_members_count": 0})
	srv.FailNext("PATCH /v1/admin/workspaces/{workspace_uuid}/users", http.StatusServiceUnavailable, 1)

	err := newTestClient(srv).PutWorkspaceMembers(context.Background(), "ws-1", []WorkspaceMember{{UserID: "u-1", RoleNames: []string{"dev", "user"}}})
	if err != nil {
		t.Fatal(err)
	}
	// The 503 is replayed: the documentation calls the call idempotent.
	srv.AssertJSONBodies("PATCH /v1/admin/workspaces/{workspace_uuid}/users",
		`{"members": [{"user_uuid": "u-1", "role_names": ["dev", "user"]}]}`,
		`{"members": [{"user_uuid": "u-1", "role_names": ["dev", "user"]}]}`)
	if got := srv.Requests("")[0].Path; got != "/v1/admin/workspaces/ws-1/users" {
		t.Errorf("path = %s", got)
	}
	srv.AssertCredential("", sdktest.AdminKey)
}

// The removal is a DELETE with a JSON body, and returns the users it did not
// remove.
func TestRemoveWorkspaceMembersSendsTheUsersInTheBody(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("DELETE /v1/admin/workspaces/{workspace_uuid}/remove-users", http.StatusOK,
		map[string]any{"deleted_members_count": 1, "not_deleted_members": []any{"u-2"}})

	notDeleted, err := newTestClient(srv).RemoveWorkspaceMembers(context.Background(), "ws-1", []string{"u-1", "u-2"})
	if err != nil || len(notDeleted) != 1 || notDeleted[0] != "u-2" {
		t.Errorf("RemoveWorkspaceMembers = %v, %v", notDeleted, err)
	}
	srv.AssertJSONBodies("DELETE /v1/admin/workspaces/{workspace_uuid}/remove-users",
		`{"members": [{"user_uuid": "u-1"}, {"user_uuid": "u-2"}]}`)
	r := srv.Requests("")[0]
	if r.Path != "/v1/admin/workspaces/ws-1/remove-users" || r.Header.Get("Content-Type") != "application/json" {
		t.Errorf("request = %s %s", r.Path, r.Header.Get("Content-Type"))
	}
}

// Every call returns the API error, which the helpers classify.
func TestWorkspaceCallsReturnTheAPIError(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("PATCH /v1/admin/workspaces/{workspace_uuid}/users", http.StatusUnprocessableEntity, map[string]any{"detail": "Invalid role"})
	srv.Respond("PATCH /v1/admin/workspaces/{workspace_uuid}", http.StatusNotFound, map[string]any{"detail": "Workspace not found"})
	srv.Respond("POST /v1/admin/workspaces", http.StatusConflict, map[string]any{"detail": "Name taken"})
	c := newTestClient(srv)
	ctx := context.Background()

	if err := c.PutWorkspaceMembers(ctx, "ws-1", nil); StatusCode(err) != http.StatusUnprocessableEntity {
		t.Errorf("PutWorkspaceMembers err = %v, want a 422", err)
	}
	if _, err := c.UpdateWorkspace(ctx, "ws-1", UpdateWorkspaceRequest{}); !IsNotFound(err) {
		t.Errorf("UpdateWorkspace err = %v, want a 404", err)
	}
	if _, err := c.CreateWorkspace(ctx, CreateWorkspaceRequest{}); !IsConflict(err) {
		t.Errorf("CreateWorkspace err = %v, want a 409", err)
	}
}
