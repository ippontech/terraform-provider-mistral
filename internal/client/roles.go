// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
)

// Role is an organization or workspace role (RoleOut in the specification,
// https://docs.mistral.ai/api/endpoint/beta/admin/users). Name is the role
// name the role_names fields of the Admin API take, such as member or
// workspace_admin: the guide lists the roles of the response under that
// name (https://docs.mistral.ai/admin/admin-api/manage-groups-roles).
type Role struct {
	ID           string `json:"uuid"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	IsCustomRole bool   `json:"is_custom_role"`
}

// Roles are the roles of the organization (RolesOut).
type Roles struct {
	WorkspaceRoles    []Role `json:"workspace_roles"`
	OrganizationRoles []Role `json:"organization_roles"`
}

// ListRoles returns the organization and workspace roles: GET
// /v1/admin/roles, which is not paginated. The guide says RBAC must be
// enabled for the organization; the answer without it is undocumented.
func (cl *Client) ListRoles(ctx context.Context) (*Roles, error) {
	var out Roles
	if err := cl.do(ctx, call{method: http.MethodGet, path: "/v1/admin/roles", audience: adminKey}, &out); err != nil {
		return nil, err
	}
	if out.WorkspaceRoles == nil {
		out.WorkspaceRoles = []Role{}
	}
	if out.OrganizationRoles == nil {
		out.OrganizationRoles = []Role{}
	}
	return &out, nil
}
