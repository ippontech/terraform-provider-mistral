// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package roles_test

import (
	"net/http"
	"testing"

	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

const routeListRoles = "GET /v1/admin/roles"

// newFake serves GET /v1/admin/roles with the roles a test passes
// (RolesOut, https://docs.mistral.ai/api/endpoint/beta/admin/users). The
// list is read only and not paginated, so the fake holds no state and no
// behaviour beyond the transport.
func newFake(t *testing.T, organizationRoles, workspaceRoles []any) *sdktest.Server {
	t.Helper()
	srv := sdktest.NewServer(t)
	srv.Respond(routeListRoles, http.StatusOK, map[string]any{
		"organization_roles": organizationRoles,
		"workspace_roles":    workspaceRoles,
	})
	return srv
}

// role is a role as the API returns it.
func role(id, name, description string, custom bool) map[string]any {
	return map[string]any{"uuid": id, "name": name, "description": description, "is_custom_role": custom}
}
