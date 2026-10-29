// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

// member is a member as GET /v1/admin/users lists it.
func member(n int) map[string]any {
	return map[string]any{
		"uuid": fmt.Sprintf("00000000-0000-0000-0000-%012d", n), "email": fmt.Sprintf("user%d@example.com", n),
		"name": fmt.Sprintf("User %d", n), "oid_id": nil, "raw_roles": []string{"M"}, "raw_role": "M",
		"created_at": "2026-10-29T10:00:00Z",
	}
}

// pendingInvite is an invite as GET /v1/admin/users lists it.
func pendingInvite(n int) map[string]any {
	return map[string]any{
		"uuid": fmt.Sprintf("10000000-0000-0000-0000-%012d", n), "email": fmt.Sprintf("guest%d@example.com", n),
		"raw_roles": []string{"M"}, "raw_role": "M", "expired": false, "created_at": "2026-10-29T11:00:00Z",
		"workspace_uuids": []string{},
	}
}

// A page holds members and invites together: page 1 is full with 60 members
// and 40 invites, page 2 holds the rest, and repeats one member of page 1
// as a list that changes during the walk can.
func TestListUsersWalksEveryPageWithTheEmailFilter(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Handle("GET /v1/admin/users", func(w http.ResponseWriter, r *http.Request) {
		var members, invites []any
		switch sdktest.QueryInt(r, "page", 0) {
		case 1:
			for i := range 60 {
				members = append(members, member(i))
			}
			for i := range 40 {
				invites = append(invites, pendingInvite(i))
			}
		case 2:
			members = []any{member(59), member(60)}
			invites = []any{pendingInvite(40)}
		}
		sdktest.WriteJSON(w, http.StatusOK, map[string]any{"members": members, "invites": invites, "total": 103, "page": 1, "page_size": 100})
	})

	dir, err := newTestClient(srv).ListUsers(context.Background(), "ada@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(dir.Users) != 61 || len(dir.Invites) != 41 {
		t.Fatalf("%d users and %d invites, want 61 and 41", len(dir.Users), len(dir.Invites))
	}
	u, inv := dir.Users[60], dir.Invites[40]
	if u.ID != "00000000-0000-0000-0000-000000000060" || *u.Email != "user60@example.com" || *u.Name != "User 60" ||
		u.RawRoles[0] != "M" || !u.CreatedAt.Equal(time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("user = %+v", u)
	}
	if inv.ID != "10000000-0000-0000-0000-000000000040" || inv.Email != "guest40@example.com" || inv.Expired || len(inv.WorkspaceIDs) != 0 {
		t.Errorf("invite = %+v", inv)
	}
	srv.AssertQueries("GET /v1/admin/users",
		"email=ada%40example.com&page=1&page_size=100", "email=ada%40example.com&page=2&page_size=100")
	srv.AssertCredential("", sdktest.AdminKey)
}

// Without an email, no filter is sent; without a total, the walk ends on a
// short page.
func TestListUsersWithoutAFilterOrATotal(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("GET /v1/admin/users", http.StatusOK, map[string]any{"members": []any{member(1)}, "invites": nil, "total": nil})

	dir, err := newTestClient(srv).ListUsers(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(dir.Users) != 1 || dir.Invites == nil || len(dir.Invites) != 0 {
		t.Errorf("directory = %+v", dir)
	}
	srv.AssertQueries("GET /v1/admin/users", "page=1&page_size=100")
}

// A failed page ends the walk with the error of the API.
func TestListUsersReturnsTheAPIError(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("GET /v1/admin/users", http.StatusForbidden, map[string]any{"detail": "Forbidden"})
	if _, err := newTestClient(srv).ListUsers(context.Background(), ""); StatusCode(err) != http.StatusForbidden {
		t.Errorf("err = %v, want a 403", err)
	}
}

// GetUser decodes the fields only the read by ID returns (first and last
// name) and the workspaces, and returns a 404 as one.
func TestGetUser(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Handle("GET /v1/admin/users/{user_id}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("user_id") != "u-1" {
			sdktest.WriteError(w, http.StatusNotFound, "User not found")
			return
		}
		sdktest.WriteJSON(w, http.StatusOK, map[string]any{
			"uuid": "u-1", "email": "ada@example.com", "name": "Ada Lovelace", "first_name": "Ada", "last_name": "Lovelace",
			"raw_roles": []string{"0d48f530-095c-43fe-8aea-6673bcacabe6"}, "created_at": "2026-10-29T10:00:00Z",
			"workspaces": []any{map[string]any{"uuid": "w-1", "name": "Default", "is_default": true, "raw_roles": []string{"A"}}},
		})
	})
	c := newTestClient(srv)

	u, err := c.GetUser(context.Background(), "u-1")
	if err != nil {
		t.Fatal(err)
	}
	if *u.FirstName != "Ada" || *u.LastName != "Lovelace" || u.RawRoles[0] != "0d48f530-095c-43fe-8aea-6673bcacabe6" ||
		len(u.Workspaces) != 1 || u.Workspaces[0].ID != "w-1" || !u.Workspaces[0].IsDefault || u.Workspaces[0].RawRoles[0] != "A" {
		t.Errorf("user = %+v", u)
	}
	if _, err := c.GetUser(context.Background(), "u-2"); !IsNotFound(err) {
		t.Errorf("err = %v, want a 404", err)
	}
	srv.AssertCredential("", sdktest.AdminKey)
}

// The create sends every field it manages, null included, and returns the
// refusals the API answers with a 200.
func TestInviteUsersSendsEveryFieldAndReadsTheRefusals(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("POST /v1/admin/users-invite", http.StatusOK, map[string]any{
		"invalid_emails": []string{}, "already_members": []string{"ada@example.com"}, "invited_members_count": 0,
	})

	got, err := newTestClient(srv).InviteUsers(context.Background(), InviteRequest{Email: "ada@example.com", RoleNames: []string{"member"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.AlreadyMembers) != 1 || len(got.InvalidEmails) != 0 || got.InvitedCount == nil || *got.InvitedCount != 0 {
		t.Errorf("result = %+v", got)
	}
	srv.AssertJSONBodies("POST /v1/admin/users-invite", `{"email": "ada@example.com", "role_names": ["member"], "workspace_uuids": null}`)
	srv.AssertCredential("", sdktest.AdminKey)
}

// A 5xx may come after the invite was sent: a replay could send a second
// email.
func TestInviteUsersIsSentOnceOnA5xx(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("POST /v1/admin/users-invite", http.StatusOK, map[string]any{"invalid_emails": []string{}, "already_members": []string{}})
	srv.FailNext("POST /v1/admin/users-invite", http.StatusBadGateway, 1)

	if _, err := newTestClient(srv).InviteUsers(context.Background(), InviteRequest{Email: "a@example.com"}); StatusCode(err) != http.StatusBadGateway {
		t.Errorf("err = %v, want the 502", err)
	}
	srv.AssertCalled("POST /v1/admin/users-invite", 1)
}

// DeleteInvite escapes the invite ID into the path and returns a 404 as
// one, which Delete then ignores.
func TestDeleteInvite(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Handle("DELETE /v1/admin/users-invite/{invite_uuid}", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("invite_uuid") != "i-1" {
			sdktest.WriteError(w, http.StatusNotFound, "Invite not found")
			return
		}
		sdktest.WriteJSON(w, http.StatusOK, map[string]any{"message": "Invite deleted"})
	})
	c := newTestClient(srv)

	if err := c.DeleteInvite(context.Background(), "i-1"); err != nil {
		t.Error(err)
	}
	if err := c.DeleteInvite(context.Background(), "i-2"); !IsNotFound(err) {
		t.Errorf("err = %v, want a 404", err)
	}
	if got := srv.Requests("DELETE /v1/admin/users-invite/{invite_uuid}")[0].Path; got != "/v1/admin/users-invite/i-1" {
		t.Errorf("path = %s", got)
	}
}
