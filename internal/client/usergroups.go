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

// userGroupsPath is the root of the user groups endpoints of the Admin API
// (https://docs.mistral.ai/api/endpoint/beta/admin/user-groups).
const userGroupsPath = "/v1/admin/user-groups"

// userGroupPageSize is the page size of the three user groups lists: the
// largest the specification allows (page_size has "maximum: 100"), so a walk
// over a large organization takes as few calls as it can.
const userGroupPageSize = 100

// UserGroup is a user group of the organization (AdminUserGroupOut).
type UserGroup struct {
	ID          string  `json:"uuid"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	// ExternallyManaged is set on a group that an identity provider (SSO or
	// SCIM) owns: "Externally managed users and groups cannot be edited
	// directly in Mistral for fields owned by the identity provider"
	// (https://docs.mistral.ai/admin/identity-access/groups).
	ExternallyManaged *bool `json:"externally_managed"`
	// OrganizationRole is the organization role the group gives its members.
	// The specification types it as a free string: a role name, a legacy
	// letter (A, M, B) or a role UUID.
	OrganizationRole *string `json:"organization_role"`
	// ParentGroupIDs are the groups this group is directly nested inside.
	ParentGroupIDs []string `json:"parent_group_ids"`
}

// CreateUserGroupRequest is the body of a create (AdminUserGroupIn).
type CreateUserGroupRequest struct {
	Name        string  `json:"name"`
	Description *string `json:"description"`
	// ParentGroupIDs is sent as [] for a top-level group, never null: the
	// specification types it as an array, and documents "Omit or pass an
	// empty list to create a top-level group".
	ParentGroupIDs []string `json:"parent_group_ids"`
}

// UpdateUserGroupRequest is the body of an update (AdminUpdateUserGroupIn).
// Every field is sent with a value: the specification reads a null or absent
// field as "leave unchanged", so the update clears a field with an empty
// value instead of a null.
type UpdateUserGroupRequest struct {
	Name string `json:"name"`
	// Description is "" to clear the description: "Pass an empty string to
	// clear it; omit the field or pass null to leave it unchanged."
	Description string `json:"description"`
	// ParentGroupIDs is the "full replacement list of parent group UUIDs";
	// [] removes every parent.
	ParentGroupIDs []string `json:"parent_group_ids"`
}

// UserGroupMember is one member of a user group (AdminUserGroupMemberOut).
type UserGroupMember struct {
	UserID string  `json:"user_uuid"`
	Name   *string `json:"name"`
	Email  *string `json:"email"`
}

// UserGroupWorkspace is the access of a user group to one workspace, with the
// roles its members get there (GroupWorkspaceAssignmentOut). The deprecated
// role_uuid and role_name, which repeat the first entry of roles, are left
// out.
type UserGroupWorkspace struct {
	WorkspaceID   string          `json:"workspace_uuid"`
	WorkspaceName string          `json:"workspace_name"`
	Roles         []UserGroupRole `json:"roles"`
	Created       time.Time       `json:"created"`
}

// UserGroupRole is a workspace role of an assignment (WorkspaceRoleRef).
type UserGroupRole struct {
	Name string `json:"name"`
	UUID string `json:"uuid"`
}

// userGroupWorkspaceRequest is the body of an assignment: POST
// .../workspaces (AdminAssignGroupToWorkspaceIn) with WorkspaceID, PATCH
// .../workspaces/{workspace_uuid} (AdminUpdateGroupWorkspaceAssignmentIn)
// without. The roles go by name in role_names: the specification makes
// role_names and roles mutually exclusive, and deprecates the singular role.
type userGroupWorkspaceRequest struct {
	WorkspaceID string   `json:"workspace_uuid,omitempty"`
	RoleNames   []string `json:"role_names"`
}

// userGroupUsersRequest is the body that adds users to a group and removes
// them (AdminAssignUsersToGroupIn): the provider sends one user at a time.
type userGroupUsersRequest struct {
	UserIDs []string `json:"user_uuids"`
}

// CreateUserGroup creates a user group: POST /v1/admin/user-groups. It is not
// idempotent: a replay after a 5xx could create a second group.
func (cl *Client) CreateUserGroup(ctx context.Context, req CreateUserGroupRequest) (*UserGroup, error) {
	if req.ParentGroupIDs == nil {
		req.ParentGroupIDs = []string{}
	}
	var out UserGroup
	if err := cl.do(ctx, call{method: http.MethodPost, path: userGroupsPath, body: req, audience: adminKey}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetUserGroup returns a user group: GET /v1/admin/user-groups/{group_uuid}.
func (cl *Client) GetUserGroup(ctx context.Context, id string) (*UserGroup, error) {
	var out UserGroup
	if err := cl.do(ctx, call{method: http.MethodGet, path: escapePath(userGroupsPath, id), audience: adminKey}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateUserGroup replaces the name, description and parent groups of a user
// group: PATCH /v1/admin/user-groups/{group_uuid}. The API does not document
// it as idempotent, so a 5xx is not replayed.
func (cl *Client) UpdateUserGroup(ctx context.Context, id string, req UpdateUserGroupRequest) (*UserGroup, error) {
	if req.ParentGroupIDs == nil {
		req.ParentGroupIDs = []string{}
	}
	var out UserGroup
	if err := cl.do(ctx, call{method: http.MethodPatch, path: escapePath(userGroupsPath, id), body: req, audience: adminKey}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteUserGroup deletes a user group: DELETE
// /v1/admin/user-groups/{group_uuid}.
func (cl *Client) DeleteUserGroup(ctx context.Context, id string) error {
	return cl.do(ctx, call{method: http.MethodDelete, path: escapePath(userGroupsPath, id), audience: adminKey}, nil)
}

// SetUserGroupOrganizationRole sets the organization role a group gives its
// members, by name (member, billing_manager, organization_admin): PATCH
// /v1/admin/user-groups/{group_uuid}/organization-role, which answers 204.
func (cl *Client) SetUserGroupOrganizationRole(ctx context.Context, id, role string) error {
	body := map[string]string{"organization_role": role}
	return cl.do(ctx, call{method: http.MethodPatch, path: escapePath(userGroupsPath, id, "organization-role"), body: body, audience: adminKey}, nil)
}

// ListUserGroups returns every user group whose name matches search, every
// page of GET /v1/admin/user-groups?search=. The specification says only
// that search "filter[s] user groups by name": the caller compares the names
// itself. An empty search lists every group.
func (cl *Client) ListUserGroups(ctx context.Context, search string) ([]UserGroup, error) {
	fetch := func(ctx context.Context, page int) ([]UserGroup, int, error) {
		q := userGroupPage(page)
		if search != "" {
			q.Set("search", search)
		}
		var out struct {
			Total *int        `json:"total"`
			Items []UserGroup `json:"items"`
		}
		err := cl.do(ctx, call{method: http.MethodGet, path: userGroupsPath, query: q, audience: adminKey}, &out)
		return out.Items, pageTotal(out.Total), err
	}
	groups, err := collect(func(visit func(UserGroup) bool) error {
		return walkPages(ctx, 1, userGroupPageSize, fetch, visit)
	})
	if err != nil {
		return nil, err
	}
	if groups == nil {
		groups = []UserGroup{}
	}
	return groups, nil
}

// AddUserGroupMember adds one user to a group: POST
// /v1/admin/user-groups/{group_uuid}/members with a list of one user, which
// answers 204. It is not documented as idempotent.
func (cl *Client) AddUserGroupMember(ctx context.Context, groupID, userID string) error {
	body := userGroupUsersRequest{UserIDs: []string{userID}}
	return cl.do(ctx, call{method: http.MethodPost, path: escapePath(userGroupsPath, groupID, "members"), body: body, audience: adminKey}, nil)
}

// RemoveUserGroupMember removes one user from a group: DELETE
// /v1/admin/user-groups/{group_uuid}/members with a list of one user in the
// body. The user stays in the organization.
func (cl *Client) RemoveUserGroupMember(ctx context.Context, groupID, userID string) error {
	body := userGroupUsersRequest{UserIDs: []string{userID}}
	return cl.do(ctx, call{method: http.MethodDelete, path: escapePath(userGroupsPath, groupID, "members"), body: body, audience: adminKey}, nil)
}

// FindUserGroupMember returns the member userID of a group, nil when the
// user is not in it, and a 404 error when the group does not exist.
//
// The API has no read of one member: this walks every page of GET
// /v1/admin/user-groups/{group_uuid}/members. It is the one function to
// replace when Mistral adds that route (CLAUDE.md, "Credentials and the
// API").
func (cl *Client) FindUserGroupMember(ctx context.Context, groupID, userID string) (*UserGroupMember, error) {
	fetch := func(ctx context.Context, page int) ([]UserGroupMember, int, error) {
		var out struct {
			Total   *int              `json:"total"`
			Members []UserGroupMember `json:"members"`
		}
		err := cl.do(ctx, call{method: http.MethodGet, path: escapePath(userGroupsPath, groupID, "members"), query: userGroupPage(page), audience: adminKey}, &out)
		return out.Members, pageTotal(out.Total), err
	}
	return find(func(visit func(UserGroupMember) bool) error {
		return walkPages(ctx, 1, userGroupPageSize, fetch, visit)
	}, func(m UserGroupMember) bool { return m.UserID == userID })
}

// AssignUserGroupWorkspace gives a group access to a workspace with roles:
// POST /v1/admin/user-groups/{group_uuid}/workspaces, which answers 201 with
// no body. It is not documented as idempotent.
func (cl *Client) AssignUserGroupWorkspace(ctx context.Context, groupID, workspaceID string, roleNames []string) error {
	body := userGroupWorkspaceRequest{WorkspaceID: workspaceID, RoleNames: roleNames}
	return cl.do(ctx, call{method: http.MethodPost, path: escapePath(userGroupsPath, groupID, "workspaces"), body: body, audience: adminKey}, nil)
}

// UpdateUserGroupWorkspace replaces the roles of a group in a workspace:
// PATCH /v1/admin/user-groups/{group_uuid}/workspaces/{workspace_uuid}, which
// answers 204. It is not documented as idempotent.
func (cl *Client) UpdateUserGroupWorkspace(ctx context.Context, groupID, workspaceID string, roleNames []string) error {
	body := userGroupWorkspaceRequest{RoleNames: roleNames}
	return cl.do(ctx, call{method: http.MethodPatch, path: escapePath(userGroupsPath, groupID, "workspaces", workspaceID), body: body, audience: adminKey}, nil)
}

// RemoveUserGroupWorkspace removes the access of a group to a workspace:
// DELETE /v1/admin/user-groups/{group_uuid}/workspaces/{workspace_uuid}. Its
// members keep the roles they hold there directly or through another group
// (https://docs.mistral.ai/admin/identity-access/groups).
func (cl *Client) RemoveUserGroupWorkspace(ctx context.Context, groupID, workspaceID string) error {
	return cl.do(ctx, call{method: http.MethodDelete, path: escapePath(userGroupsPath, groupID, "workspaces", workspaceID), audience: adminKey}, nil)
}

// FindUserGroupWorkspace returns the assignment of a group to a workspace,
// nil when the group has no access to it, and a 404 error when the group
// does not exist.
//
// The API has no read of one assignment: this walks every page of GET
// /v1/admin/user-groups/{group_uuid}/workspaces. It is the one function to
// replace when Mistral adds that route.
func (cl *Client) FindUserGroupWorkspace(ctx context.Context, groupID, workspaceID string) (*UserGroupWorkspace, error) {
	fetch := func(ctx context.Context, page int) ([]UserGroupWorkspace, int, error) {
		var out struct {
			Total *int                 `json:"total"`
			Items []UserGroupWorkspace `json:"items"`
		}
		err := cl.do(ctx, call{method: http.MethodGet, path: escapePath(userGroupsPath, groupID, "workspaces"), query: userGroupPage(page), audience: adminKey}, &out)
		return out.Items, pageTotal(out.Total), err
	}
	return find(func(visit func(UserGroupWorkspace) bool) error {
		return walkPages(ctx, 1, userGroupPageSize, fetch, visit)
	}, func(a UserGroupWorkspace) bool { return a.WorkspaceID == workspaceID })
}

// userGroupPage is the query of one page of a user groups list, whose first
// page is 1.
func userGroupPage(page int) url.Values {
	return url.Values{"page": {strconv.Itoa(page)}, "page_size": {strconv.Itoa(userGroupPageSize)}}
}
