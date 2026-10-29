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

// The users and invites of the organization, through the Admin API
// (https://docs.mistral.ai/api/endpoint/beta/admin/users, guide at
// https://docs.mistral.ai/admin/admin-api/manage-users). Every call uses the
// Admin API key.

// User is a member of the organization: AdminUserOUT from the read by ID,
// or AdminOrganizationMemberOUT from the list, which has no first and last
// name.
type User struct {
	ID        string  `json:"uuid"`
	Email     *string `json:"email"`
	Name      *string `json:"name"`
	FirstName *string `json:"first_name"`
	LastName  *string `json:"last_name"`
	// RawRoles are the organization roles of the member: role UUIDs, which
	// GET /v1/admin/roles names, or the legacy letters of UserRole (A, M, B).
	RawRoles   []string        `json:"raw_roles"`
	Workspaces []UserWorkspace `json:"workspaces"`
	CreatedAt  time.Time       `json:"created_at"`
}

// UserWorkspace is a workspace a member belongs to (MemberWorkspaceInfo).
type UserWorkspace struct {
	ID        string `json:"uuid"`
	Name      string `json:"name"`
	IsDefault bool   `json:"is_default"`
	// RawRoles are the workspace roles of the member: role UUIDs, or the
	// legacy letters of WorkspaceRole (A, M).
	RawRoles []string `json:"raw_roles"`
}

// Invite is an invitation to the organization that is not accepted yet, as
// GET /v1/admin/users lists it (OrganizationInviteOUT). The Admin Panel shows
// the same list as "pending invitations, including expired invitations"
// (https://docs.mistral.ai/admin/identity-access/user-management).
type Invite struct {
	ID    string `json:"uuid"`
	Email string `json:"email"`
	// RawRoles are the organization roles the invite assigns, in the same
	// form as User.RawRoles.
	RawRoles     []string  `json:"raw_roles"`
	Expired      bool      `json:"expired"`
	CreatedAt    time.Time `json:"created_at"`
	WorkspaceIDs []string  `json:"workspace_uuids"`
}

// UserDirectory is every page of GET /v1/admin/users: the members of the
// organization and its pending invites.
type UserDirectory struct {
	Users   []User
	Invites []Invite
}

// InviteRequest is the body of POST /v1/admin/users-invite
// (OrganizationInviteIN) for the fields the provider manages. Every field is
// sent, null included, as the other requests of the provider do.
type InviteRequest struct {
	// Email is one address. The API also accepts several, separated by
	// commas or new lines, which the provider never sends: one resource is
	// one invite.
	Email string `json:"email"`
	// RoleNames are organization role names: member, billing_manager,
	// organization_admin.
	RoleNames    []string `json:"role_names"`
	WorkspaceIDs []string `json:"workspace_uuids"`
}

// InviteResult is the answer of POST /v1/admin/users-invite
// (OrganizationInvitesCreateOUT): counts and refused addresses, not the
// invite, whose ID the caller has to find by listing.
type InviteResult struct {
	InvalidEmails  []string `json:"invalid_emails"`
	AlreadyMembers []string `json:"already_members"`
	// InvitedCount is null when the API does not say.
	InvitedCount *int64 `json:"invited_members_count"`
}

// usersPageSize is the largest page_size GET /v1/admin/users accepts
// (maximum: 100 in the specification), so that a walk takes the fewest
// calls.
const usersPageSize = 100

// usersPage is one page of GET /v1/admin/users (OrganizationAdminUsersOUT).
// page_size bounds members and invites together, and total counts both: our
// reading of "Maximum number of results per page" and "Total number of users
// and invitations that match the request".
type usersPage struct {
	Members []User   `json:"members"`
	Invites []Invite `json:"invites"`
	Total   *int     `json:"total"`
}

// directoryEntry is one result of a page: a member or an invite, so that
// walkPages counts both against the page size.
type directoryEntry struct {
	user   *User
	invite *Invite
}

// ListUsers returns every member and pending invite of the organization
// whose email matches email, or all of them when email is empty: GET
// /v1/admin/users?email=&page=&page_size=, walked from page 1. The provider
// compares the addresses itself, since the specification does not say
// whether the filter is exact.
//
// An entry that two pages return, as a list that changes during the walk
// can, is kept once.
func (cl *Client) ListUsers(ctx context.Context, email string) (*UserDirectory, error) {
	fetch := func(ctx context.Context, page int) ([]directoryEntry, int, error) {
		q := url.Values{"page": {strconv.Itoa(page)}, "page_size": {strconv.Itoa(usersPageSize)}}
		if email != "" {
			q.Set("email", email)
		}
		var out usersPage
		if err := cl.do(ctx, call{method: http.MethodGet, path: "/v1/admin/users", query: q, audience: adminKey}, &out); err != nil {
			return nil, 0, err
		}
		entries := make([]directoryEntry, 0, len(out.Members)+len(out.Invites))
		for i := range out.Members {
			entries = append(entries, directoryEntry{user: &out.Members[i]})
		}
		for i := range out.Invites {
			entries = append(entries, directoryEntry{invite: &out.Invites[i]})
		}
		total := -1
		if out.Total != nil {
			total = *out.Total
		}
		return entries, total, nil
	}

	dir := &UserDirectory{Users: []User{}, Invites: []Invite{}}
	seen := map[string]bool{}
	err := walkPages(ctx, 1, usersPageSize, fetch, func(e directoryEntry) bool {
		switch {
		case e.user != nil && !seen["user:"+e.user.ID]:
			seen["user:"+e.user.ID] = true
			dir.Users = append(dir.Users, *e.user)
		case e.invite != nil && !seen["invite:"+e.invite.ID]:
			seen["invite:"+e.invite.ID] = true
			dir.Invites = append(dir.Invites, *e.invite)
		}
		return true
	})
	if err != nil {
		return nil, err
	}
	return dir, nil
}

// GetUser returns a member of the organization: GET /v1/admin/users/{user_id}.
func (cl *Client) GetUser(ctx context.Context, id string) (*User, error) {
	var out User
	if err := cl.do(ctx, call{method: http.MethodGet, path: escapePath("/v1/admin/users", id), audience: adminKey}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// InviteUsers sends the invites of req: POST /v1/admin/users-invite. It is
// not idempotent: a replay could send a second email. The API answers 200
// when it refuses an address, which the caller reads in the result.
func (cl *Client) InviteUsers(ctx context.Context, req InviteRequest) (*InviteResult, error) {
	var out InviteResult
	if err := cl.do(ctx, call{method: http.MethodPost, path: "/v1/admin/users-invite", body: req, audience: adminKey}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteInvite revokes a pending invite: DELETE
// /v1/admin/users-invite/{invite_uuid}. The answer, {"message": "..."},
// carries nothing the provider uses.
func (cl *Client) DeleteInvite(ctx context.Context, id string) error {
	return cl.do(ctx, call{method: http.MethodDelete, path: escapePath("/v1/admin/users-invite", id), audience: adminKey}, nil)
}
