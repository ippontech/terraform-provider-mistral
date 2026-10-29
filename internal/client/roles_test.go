// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"testing"

	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

// ListRoles decodes both lists of roles, custom roles included, with the
// Admin API key.
func TestListRoles(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("GET /v1/admin/roles", http.StatusOK, map[string]any{
		"organization_roles": []any{map[string]any{"uuid": "r-1", "name": "member", "description": "Product usage", "is_custom_role": false}},
		"workspace_roles":    []any{map[string]any{"uuid": "r-2", "name": "auditor", "description": "Reads logs", "is_custom_role": true}},
	})

	got, err := newTestClient(srv).ListRoles(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got.OrganizationRoles) != 1 || got.OrganizationRoles[0] != (Role{ID: "r-1", Name: "member", Description: "Product usage"}) {
		t.Errorf("organization roles = %+v", got.OrganizationRoles)
	}
	if len(got.WorkspaceRoles) != 1 || got.WorkspaceRoles[0] != (Role{ID: "r-2", Name: "auditor", Description: "Reads logs", IsCustomRole: true}) {
		t.Errorf("workspace roles = %+v", got.WorkspaceRoles)
	}
	srv.AssertCredential("GET /v1/admin/roles", sdktest.AdminKey)
}

// An answer without a list reads an empty list, never nil.
func TestListRolesAnswersMissingListsAsEmpty(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("GET /v1/admin/roles", http.StatusOK, map[string]any{})
	got, err := newTestClient(srv).ListRoles(context.Background())
	if err != nil || got.OrganizationRoles == nil || got.WorkspaceRoles == nil {
		t.Errorf("ListRoles = %+v, %v", got, err)
	}
}

// The guide says RBAC must be enabled to list the roles: the error of an
// organization without it reaches the caller as an *Error.
func TestListRolesReturnsTheAPIError(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("GET /v1/admin/roles", http.StatusForbidden, map[string]any{"detail": "RBAC is not enabled"})
	if _, err := newTestClient(srv).ListRoles(context.Background()); StatusCode(err) != http.StatusForbidden {
		t.Errorf("err = %v, want a 403", err)
	}
}
