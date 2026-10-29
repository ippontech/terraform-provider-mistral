// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"testing"

	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

const (
	routeCreateServiceAccount = "POST /v1/service-accounts"
	routeServiceAccount       = "/v1/service-accounts/{service_account_id}"
	routeServiceAccountRoles  = "/v1/service-accounts/{service_account_id}/roles"
	routeAssignableRoles      = "GET /v1/service-accounts/assignable-roles"
)

// serviceAccountBody is a ServiceAccount answer of the specification.
func serviceAccountBody(description any) map[string]any {
	return map[string]any{
		"id": "6f1c1a4e-0000-4000-8000-000000000001", "name": "prod-worker", "customer_id": "c-1",
		"organization_id": "org-1", "workspace_id": "ws-1", "description": description,
		"created_at": "2026-10-29T10:00:00Z", "updated_at": "2026-10-29T10:01:00Z", "deleted_at": nil,
	}
}

// TestCreateServiceAccountSendsItsRoles checks the body of a create, roles
// included, and the decoding of its answer.
func TestCreateServiceAccountSendsItsRoles(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond(routeCreateServiceAccount, http.StatusCreated, serviceAccountBody(nil))

	sa, err := newTestClient(srv).CreateServiceAccount(context.Background(), CreateServiceAccountRequest{
		Name: "prod-worker", WorkspaceID: "ws-1", RoleIDs: []string{"r-1", "r-2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if sa.ID != "6f1c1a4e-0000-4000-8000-000000000001" || sa.WorkspaceID != "ws-1" || sa.OrganizationID != "org-1" ||
		sa.Description != nil || sa.DeletedAt != nil || sa.UpdatedAt.Minute() != 1 {
		t.Errorf("service account = %+v", sa)
	}
	srv.AssertJSONBodies(routeCreateServiceAccount,
		`{"name": "prod-worker", "workspace_id": "ws-1", "description": null, "role_ids": ["r-1", "r-2"]}`)
	srv.AssertCredential("", sdktest.APIKey)
}

// A create that meets a 5xx is sent once: the account may exist.
func TestCreateServiceAccountIsSentOnceOnA5xx(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond(routeCreateServiceAccount, http.StatusCreated, serviceAccountBody(nil))
	srv.FailNext(routeCreateServiceAccount, http.StatusInternalServerError, 1)

	_, err := newTestClient(srv).CreateServiceAccount(context.Background(), CreateServiceAccountRequest{Name: "a", WorkspaceID: "ws-1", RoleIDs: []string{"r-1"}})
	if StatusCode(err) != http.StatusInternalServerError {
		t.Errorf("err = %v, want the 500", err)
	}
	srv.AssertCalled(routeCreateServiceAccount, 1)
}

// TestGetUpdateDeleteServiceAccount checks the other calls on an account: an
// update is not replayed after a 5xx, a delete is.
func TestGetUpdateDeleteServiceAccount(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("GET "+routeServiceAccount, http.StatusOK, serviceAccountBody("Runs the workers"))
	srv.Respond("PATCH "+routeServiceAccount, http.StatusOK, serviceAccountBody(nil))
	srv.Respond("DELETE "+routeServiceAccount, http.StatusNoContent, nil)
	c := newTestClient(srv)
	ctx := context.Background()

	if sa, err := c.GetServiceAccount(ctx, "sa-1"); err != nil || sa.Description == nil || *sa.Description != "Runs the workers" {
		t.Errorf("GetServiceAccount = %+v, %v", sa, err)
	}
	if sa, err := c.UpdateServiceAccount(ctx, "sa-1", UpdateServiceAccountRequest{}); err != nil || sa.Description != nil {
		t.Errorf("UpdateServiceAccount = %+v, %v", sa, err)
	}
	srv.FailNext("PATCH "+routeServiceAccount, http.StatusServiceUnavailable, 1)
	if _, err := c.UpdateServiceAccount(ctx, "sa-1", UpdateServiceAccountRequest{Description: new("d")}); StatusCode(err) != http.StatusServiceUnavailable {
		t.Errorf("an update after a 503 must not be replayed: %v", err)
	}
	srv.FailNext("DELETE "+routeServiceAccount, http.StatusBadGateway, 1)
	if err := c.DeleteServiceAccount(ctx, "sa-1"); err != nil {
		t.Errorf("a delete is retried after a 502: %v", err)
	}

	srv.AssertCalled("PATCH "+routeServiceAccount, 2)
	srv.AssertJSONBodies("PATCH "+routeServiceAccount, `{"description": null}`, `{"description": "d"}`)
	srv.AssertCalled("DELETE "+routeServiceAccount, 2)
	if got := srv.Requests("GET " + routeServiceAccount)[0].Path; got != "/v1/service-accounts/sa-1" {
		t.Errorf("path = %s", got)
	}
}

// TestServiceAccountRoles checks the read and the replace of the roles.
func TestServiceAccountRoles(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	roles := map[string]any{"items": []any{map[string]any{"role_id": "r-1"}, map[string]any{"role_id": "r-2"}}}
	srv.Respond("GET "+routeServiceAccountRoles, http.StatusOK, roles)
	srv.Respond("PUT "+routeServiceAccountRoles, http.StatusOK, roles)
	c := newTestClient(srv)
	ctx := context.Background()

	if got, err := c.ListServiceAccountRoles(ctx, "sa-1"); err != nil || len(got) != 2 || got[0] != "r-1" || got[1] != "r-2" {
		t.Errorf("ListServiceAccountRoles = %v, %v", got, err)
	}
	// The PUT replaces the roles as a whole: a replay after a 5xx sets the
	// same ones.
	srv.FailNext("PUT "+routeServiceAccountRoles, http.StatusServiceUnavailable, 1)
	if got, err := c.SetServiceAccountRoles(ctx, "sa-1", []string{"r-1", "r-2"}); err != nil || len(got) != 2 {
		t.Errorf("SetServiceAccountRoles = %v, %v", got, err)
	}
	srv.AssertJSONBodies("PUT "+routeServiceAccountRoles, `{"role_ids": ["r-1", "r-2"]}`, `{"role_ids": ["r-1", "r-2"]}`)
}

// TestServiceAccountRolesAnswerAnEmptyList checks that no role decodes as an
// empty slice.
func TestServiceAccountRolesAnswerAnEmptyList(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("GET "+routeServiceAccountRoles, http.StatusOK, map[string]any{"items": []any{}})
	got, err := newTestClient(srv).ListServiceAccountRoles(context.Background(), "sa-1")
	if err != nil || got == nil || len(got) != 0 {
		t.Errorf("ListServiceAccountRoles = %#v, %v", got, err)
	}
}

// TestListAssignableServiceAccountRoles checks the query and the decoding of
// every field of a role.
func TestListAssignableServiceAccountRoles(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond(routeAssignableRoles, http.StatusOK, map[string]any{"items": []any{
		map[string]any{"uuid": "r-1", "name": "workflow_executor", "description": "Runs workflows", "is_custom_role": false},
		map[string]any{"uuid": "r-2", "name": "ci", "description": "", "is_custom_role": true},
	}})

	got, err := newTestClient(srv).ListAssignableServiceAccountRoles(context.Background(), "ws-1")
	if err != nil {
		t.Fatal(err)
	}
	want := []AssignableServiceAccountRole{
		{UUID: "r-1", Name: "workflow_executor", Description: "Runs workflows"},
		{UUID: "r-2", Name: "ci", IsCustomRole: true},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("roles = %+v, want %+v", got, want)
	}
	srv.AssertQueries(routeAssignableRoles, "workspace_id=ws-1")
	srv.AssertCredential("", sdktest.APIKey)
}

// TestListAssignableServiceAccountRolesAnswersNullAsEmpty checks that null
// items decode as an empty slice.
func TestListAssignableServiceAccountRolesAnswersNullAsEmpty(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond(routeAssignableRoles, http.StatusOK, map[string]any{"items": nil})
	got, err := newTestClient(srv).ListAssignableServiceAccountRoles(context.Background(), "ws-1")
	if err != nil || got == nil || len(got) != 0 {
		t.Errorf("roles = %#v, %v", got, err)
	}
}

// TestServiceAccountCallsReturnTheAPIError checks that every call returns the
// API error with its status.
func TestServiceAccountCallsReturnTheAPIError(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	notFound := map[string]any{"detail": "Service account not found"}
	srv.Respond("GET "+routeServiceAccount, http.StatusNotFound, notFound)
	srv.Respond("PATCH "+routeServiceAccount, http.StatusNotFound, notFound)
	srv.Respond(routeCreateServiceAccount, http.StatusConflict, map[string]any{"detail": "Name already taken"})
	srv.Respond("GET "+routeServiceAccountRoles, http.StatusNotFound, notFound)
	srv.Respond("PUT "+routeServiceAccountRoles, http.StatusUnprocessableEntity, map[string]any{"detail": []any{
		map[string]any{"loc": []any{"body", "role_ids"}, "msg": "List should have at least 1 item", "type": "too_short"},
	}})
	srv.Respond(routeAssignableRoles, http.StatusForbidden, map[string]any{"detail": "Forbidden"})
	c := newTestClient(srv)
	ctx := context.Background()

	if _, err := c.GetServiceAccount(ctx, "sa-1"); !IsNotFound(err) {
		t.Errorf("GetServiceAccount: %v", err)
	}
	if _, err := c.UpdateServiceAccount(ctx, "sa-1", UpdateServiceAccountRequest{}); !IsNotFound(err) {
		t.Errorf("UpdateServiceAccount: %v", err)
	}
	if _, err := c.CreateServiceAccount(ctx, CreateServiceAccountRequest{}); !IsConflict(err) {
		t.Errorf("CreateServiceAccount: %v", err)
	}
	if _, err := c.ListServiceAccountRoles(ctx, "sa-1"); !IsNotFound(err) {
		t.Errorf("ListServiceAccountRoles: %v", err)
	}
	if _, err := c.SetServiceAccountRoles(ctx, "sa-1", nil); StatusCode(err) != http.StatusUnprocessableEntity {
		t.Errorf("SetServiceAccountRoles: %v", err)
	}
	if _, err := c.ListAssignableServiceAccountRoles(ctx, "ws-1"); StatusCode(err) != http.StatusForbidden {
		t.Errorf("ListAssignableServiceAccountRoles: %v", err)
	}
}
