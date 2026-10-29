// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"net/url"
	"time"
)

// ServiceAccount is a non-human identity of a workspace (ServiceAccount in
// the specification, https://docs.mistral.ai/api/endpoint/beta/service_accounts).
// Its roles are read and set with their own calls.
type ServiceAccount struct {
	ID             string     `json:"id"`
	Name           string     `json:"name"`
	OrganizationID string     `json:"organization_id"`
	WorkspaceID    string     `json:"workspace_id"`
	Description    *string    `json:"description"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	DeletedAt      *time.Time `json:"deleted_at"`
}

// CreateServiceAccountRequest is the body of a create. RoleIDs takes 1 to 14
// workspace role IDs: the specification reads an empty list as the member
// role, which grants no permission.
type CreateServiceAccountRequest struct {
	Name        string   `json:"name"`
	WorkspaceID string   `json:"workspace_id"`
	Description *string  `json:"description"`
	RoleIDs     []string `json:"role_ids"`
}

// UpdateServiceAccountRequest is the body of an update, which can change the
// description only. A null description clears it.
type UpdateServiceAccountRequest struct {
	Description *string `json:"description"`
}

// AssignableServiceAccountRole is a workspace role that a service account can
// hold.
type AssignableServiceAccountRole struct {
	UUID         string `json:"uuid"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	IsCustomRole bool   `json:"is_custom_role"`
}

// serviceAccountRoles is the answer of the roles calls,
// ListServiceAccountRolesResponse.
type serviceAccountRoles struct {
	Items []struct {
		RoleID string `json:"role_id"`
	} `json:"items"`
}

// ids returns the role IDs of the answer, in its order, and an empty slice
// for no role.
func (r serviceAccountRoles) ids() []string {
	out := make([]string, 0, len(r.Items))
	for _, it := range r.Items {
		out = append(out, it.RoleID)
	}
	return out
}

// serviceAccountsPath is the collection of service accounts. It sits outside
// /v1/admin and takes the standard key of the workspace.
const serviceAccountsPath = "/v1/service-accounts"

// CreateServiceAccount creates a service account with its roles: POST
// /v1/service-accounts, which answers 201. It is not idempotent.
func (cl *Client) CreateServiceAccount(ctx context.Context, req CreateServiceAccountRequest) (*ServiceAccount, error) {
	var out ServiceAccount
	if err := cl.do(ctx, call{method: http.MethodPost, path: serviceAccountsPath, body: req, audience: workspaceKey}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetServiceAccount returns a service account: GET
// /v1/service-accounts/{service_account_id}.
func (cl *Client) GetServiceAccount(ctx context.Context, id string) (*ServiceAccount, error) {
	var out ServiceAccount
	if err := cl.do(ctx, call{method: http.MethodGet, path: escapePath(serviceAccountsPath, id), audience: workspaceKey}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateServiceAccount sets the description of a service account: PATCH
// /v1/service-accounts/{service_account_id}. It is not marked idempotent: the
// API does not document it as an upsert.
func (cl *Client) UpdateServiceAccount(ctx context.Context, id string, req UpdateServiceAccountRequest) (*ServiceAccount, error) {
	var out ServiceAccount
	if err := cl.do(ctx, call{method: http.MethodPatch, path: escapePath(serviceAccountsPath, id), body: req, audience: workspaceKey}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteServiceAccount deletes a service account and its credentials: DELETE
// /v1/service-accounts/{service_account_id}, which answers 204.
func (cl *Client) DeleteServiceAccount(ctx context.Context, id string) error {
	return cl.do(ctx, call{method: http.MethodDelete, path: escapePath(serviceAccountsPath, id), audience: workspaceKey}, nil)
}

// ListServiceAccountRoles returns the IDs of the workspace roles a service
// account holds: GET /v1/service-accounts/{service_account_id}/roles, which
// is not paginated.
func (cl *Client) ListServiceAccountRoles(ctx context.Context, id string) ([]string, error) {
	var out serviceAccountRoles
	if err := cl.do(ctx, call{method: http.MethodGet, path: escapePath(serviceAccountsPath, id, "roles"), audience: workspaceKey}, &out); err != nil {
		return nil, err
	}
	return out.ids(), nil
}

// SetServiceAccountRoles replaces the workspace roles of a service account
// with roleIDs, 1 to 14 of them, and returns the roles it then holds: PUT
// /v1/service-accounts/{service_account_id}/roles. A full replace, so a
// replay sets the same roles.
func (cl *Client) SetServiceAccountRoles(ctx context.Context, id string, roleIDs []string) ([]string, error) {
	var out serviceAccountRoles
	// SetServiceAccountRolesRequest: role_ids is its only field.
	body := map[string][]string{"role_ids": roleIDs}
	if err := cl.do(ctx, call{method: http.MethodPut, path: escapePath(serviceAccountsPath, id, "roles"), body: body, audience: workspaceKey}, &out); err != nil {
		return nil, err
	}
	return out.ids(), nil
}

// ListAssignableServiceAccountRoles returns the workspace roles a service
// account of the workspace can hold: GET
// /v1/service-accounts/assignable-roles?workspace_id=, which is not
// paginated.
func (cl *Client) ListAssignableServiceAccountRoles(ctx context.Context, workspaceID string) ([]AssignableServiceAccountRole, error) {
	// ListAssignableServiceAccountRolesResponse.
	var out struct {
		Items []AssignableServiceAccountRole `json:"items"`
	}
	q := url.Values{"workspace_id": {workspaceID}}
	if err := cl.do(ctx, call{method: http.MethodGet, path: serviceAccountsPath + "/assignable-roles", query: q, audience: workspaceKey}, &out); err != nil {
		return nil, err
	}
	// An answer without items is an empty list, not an absent one.
	if out.Items == nil {
		out.Items = []AssignableServiceAccountRole{}
	}
	return out.Items, nil
}
