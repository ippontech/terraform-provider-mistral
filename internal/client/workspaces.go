// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// workspacePageSize is the page size of GET /v1/admin/workspaces: the
// maximum the specification allows, so that an organization at the limit of
// 500 active workspaces takes five pages.
const workspacePageSize = 100

// Workspace is a workspace of the organization: WorkspaceOUT in the
// specification, and WorkspaceEnrichedOUT for a create, whose extra fields
// the provider does not map
// (https://docs.mistral.ai/admin/admin-api/manage-workspaces).
type Workspace struct {
	ID          string  `json:"uuid"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Icon        *string `json:"icon"`
	IsDefault   bool    `json:"is_default"`
}

// CreateWorkspaceRequest is the body of a create (AdminWorkspaceIn). Every
// field is sent: description and icon default to "" at the API, which the
// provider sends as such.
type CreateWorkspaceRequest struct {
	Name             string `json:"name"`
	Description      string `json:"description"`
	Icon             string `json:"icon"`
	AddAllOrgMembers bool   `json:"add_all_org_members"`
	// AdminUserID is the user granted the Workspace Admin role at creation.
	AdminUserID string `json:"admin_user_id"`
}

// UpdateWorkspaceRequest is the body of an update (UpdateWorkspaceIN). Every
// field is nullable in the specification, which reads as "left as it is":
// the provider sends all three, with "" rather than null for an empty
// description or icon, so that an update can clear them.
type UpdateWorkspaceRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
}

// WorkspaceFilter narrows GET /v1/admin/workspaces.
type WorkspaceFilter struct {
	// Search keeps the workspaces whose name matches it; "" keeps them all.
	Search string
	// IncludeArchived adds the archived workspaces, which the list leaves out
	// by default (is_archived=true).
	IncludeArchived bool
}

// query returns the filter as query parameters, without the paging ones.
func (f WorkspaceFilter) query() url.Values {
	q := url.Values{}
	if f.Search != "" {
		q.Set("search", f.Search)
	}
	if f.IncludeArchived {
		q.Set("is_archived", "true")
	}
	return q
}

// CreateWorkspace creates a workspace: POST /v1/admin/workspaces. It is not
// idempotent.
func (cl *Client) CreateWorkspace(ctx context.Context, req CreateWorkspaceRequest) (*Workspace, error) {
	var out Workspace
	if err := cl.do(ctx, call{method: http.MethodPost, path: "/v1/admin/workspaces", body: req, audience: adminKey}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateWorkspace sets the name, description and icon of a workspace: PATCH
// /v1/admin/workspaces/{workspace_uuid}. It is not documented as idempotent,
// so a 5xx is not replayed.
func (cl *Client) UpdateWorkspace(ctx context.Context, id string, req UpdateWorkspaceRequest) (*Workspace, error) {
	var out Workspace
	if err := cl.do(ctx, call{method: http.MethodPatch, path: escapePath("/v1/admin/workspaces", id), body: req, audience: adminKey}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ArchiveWorkspace archives a workspace: DELETE
// /v1/admin/workspaces/{workspace_uuid}, which the specification describes as
// "Archive a Workspace". The workspace guide says that archiving "cannot be
// undone" and that the default workspace cannot be archived
// (https://docs.mistral.ai/admin/workspaces/your-first-workspace).
func (cl *Client) ArchiveWorkspace(ctx context.Context, id string) error {
	return cl.do(ctx, call{method: http.MethodDelete, path: escapePath("/v1/admin/workspaces", id), audience: adminKey}, nil)
}

// ListWorkspaces returns every workspace f keeps, walking every page of GET
// /v1/admin/workspaces.
func (cl *Client) ListWorkspaces(ctx context.Context, f WorkspaceFilter) ([]Workspace, error) {
	out, err := collect(func(visit func(Workspace) bool) error { return cl.walkWorkspaces(ctx, f, visit) })
	if out == nil {
		out = []Workspace{}
	}
	return out, err
}

// FindWorkspace returns the first workspace f keeps that match accepts, or
// nil, reading only the pages it needs.
func (cl *Client) FindWorkspace(ctx context.Context, f WorkspaceFilter, match func(Workspace) bool) (*Workspace, error) {
	return find(func(visit func(Workspace) bool) error { return cl.walkWorkspaces(ctx, f, visit) }, match)
}

// walkWorkspaces walks GET /v1/admin/workspaces, paged by number from 1 with
// the total of the matching workspaces in every answer.
func (cl *Client) walkWorkspaces(ctx context.Context, f WorkspaceFilter, visit func(Workspace) bool) error {
	fetch := func(ctx context.Context, page int) ([]Workspace, int, error) {
		q := f.query()
		q.Set("page", strconv.Itoa(page))
		q.Set("page_size", strconv.Itoa(workspacePageSize))
		var out struct {
			Total int         `json:"total"`
			Items []Workspace `json:"items"`
		}
		err := cl.do(ctx, call{method: http.MethodGet, path: "/v1/admin/workspaces", query: q, audience: adminKey}, &out)
		return out.Items, out.Total, err
	}
	return walkPages(ctx, 1, workspacePageSize, fetch, visit)
}

// WorkspaceMember is one user of an add-or-update of workspace members
// (AdminWorkspaceMemberSingleIN), with the role names the workspace guide
// recommends over the deprecated role and role_name fields.
type WorkspaceMember struct {
	UserID    string   `json:"user_uuid"`
	RoleNames []string `json:"role_names"`
}

// PutWorkspaceMembers adds the members to the workspace, or sets the roles of
// those already in it: PATCH /v1/admin/workspaces/{workspace_uuid}/users.
// The workspace guide documents it as idempotent
// (https://docs.mistral.ai/admin/admin-api/manage-workspaces), so it is
// retried like a read. POST .../add-users, which only adds, is not used: an
// update needs this call anyway, and that one is not documented as
// idempotent.
func (cl *Client) PutWorkspaceMembers(ctx context.Context, workspaceID string, members []WorkspaceMember) error {
	body := struct {
		Members []WorkspaceMember `json:"members"`
	}{members}
	return cl.do(ctx, call{
		method: http.MethodPatch, path: escapePath("/v1/admin/workspaces", workspaceID, "users"),
		body: body, audience: adminKey, idempotent: true,
	}, nil)
}

// RemoveWorkspaceMembers removes the users from the workspace and returns
// those the API could not remove (not_deleted_members): DELETE
// /v1/admin/workspaces/{workspace_uuid}/remove-users, which takes its list in
// the body.
func (cl *Client) RemoveWorkspaceMembers(ctx context.Context, workspaceID string, userIDs []string) ([]string, error) {
	type member struct {
		UserID string `json:"user_uuid"`
	}
	body := struct {
		Members []member `json:"members"`
	}{}
	for _, id := range userIDs {
		body.Members = append(body.Members, member{id})
	}
	var out struct {
		NotDeleted []string `json:"not_deleted_members"`
	}
	err := cl.do(ctx, call{
		method: http.MethodDelete, path: escapePath("/v1/admin/workspaces", workspaceID, "remove-users"),
		body: body, audience: adminKey,
	}, &out)
	return out.NotDeleted, err
}
