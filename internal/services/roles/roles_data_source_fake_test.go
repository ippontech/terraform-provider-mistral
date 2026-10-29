// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package roles_test

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-mistral/internal/providertest"
	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

const rolesAddr = "data.mistral_roles.all"

var rolesConfig = providertest.ProviderConfig() + `
data "mistral_roles" "all" {}
`

// Both lists keep the order of the API, with exact values, a custom role
// included; the call takes no query and the Admin API key.
func TestRolesDataSourceListsBothScopes(t *testing.T) {
	t.Parallel()
	srv := newFake(t,
		[]any{
			role("00000000-0000-4000-8000-0000000000a1", "member", "Product usage", false),
			role("00000000-0000-4000-8000-0000000000a3", "organization_admin", "Full control of the Organization", false),
		},
		[]any{
			role("00000000-0000-4000-8000-0000000000b1", "workspace_admin", "Workspace administration", false),
			role("00000000-0000-4000-8000-0000000000b9", "auditor", "Reads the observability traces", true),
		})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv),
		Steps: []resource.TestStep{{
			Config: rolesConfig,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(rolesAddr, tfjsonpath.New("organization_roles"), knownvalue.ListExact([]knownvalue.Check{
					knownvalue.ObjectExact(map[string]knownvalue.Check{
						"id": knownvalue.StringExact("00000000-0000-4000-8000-0000000000a1"), "name": knownvalue.StringExact("member"),
						"description": knownvalue.StringExact("Product usage"), "is_custom": knownvalue.Bool(false),
					}),
					knownvalue.ObjectExact(map[string]knownvalue.Check{
						"id": knownvalue.StringExact("00000000-0000-4000-8000-0000000000a3"), "name": knownvalue.StringExact("organization_admin"),
						"description": knownvalue.StringExact("Full control of the Organization"), "is_custom": knownvalue.Bool(false),
					}),
				})),
				statecheck.ExpectKnownValue(rolesAddr, tfjsonpath.New("workspace_roles"), knownvalue.ListExact([]knownvalue.Check{
					knownvalue.ObjectExact(map[string]knownvalue.Check{
						"id": knownvalue.StringExact("00000000-0000-4000-8000-0000000000b1"), "name": knownvalue.StringExact("workspace_admin"),
						"description": knownvalue.StringExact("Workspace administration"), "is_custom": knownvalue.Bool(false),
					}),
					knownvalue.ObjectExact(map[string]knownvalue.Check{
						"id": knownvalue.StringExact("00000000-0000-4000-8000-0000000000b9"), "name": knownvalue.StringExact("auditor"),
						"description": knownvalue.StringExact("Reads the observability traces"), "is_custom": knownvalue.Bool(true),
					}),
				})),
			},
		}},
	})
	srv.AssertCredential("", sdktest.AdminKey)
	if q := srv.Requests(routeListRoles)[0].Query.Encode(); q != "" {
		t.Errorf("query = %q, want none", q)
	}
}

// An organization without roles to list, or an answer without the lists,
// reads empty lists.
func TestRolesDataSourceListsNoRole(t *testing.T) {
	t.Parallel()
	srv := newFake(t, nil, []any{})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv),
		Steps: []resource.TestStep{{
			Config: rolesConfig,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(rolesAddr, tfjsonpath.New("organization_roles"), knownvalue.ListExact([]knownvalue.Check{})),
				statecheck.ExpectKnownValue(rolesAddr, tfjsonpath.New("workspace_roles"), knownvalue.ListExact([]knownvalue.Check{})),
			},
		}},
	})
}

// The guide says RBAC must be enabled for the organization; the answer
// without it is undocumented, and reaches the user as it comes.
func TestRolesDataSourceReportsErrors(t *testing.T) {
	t.Parallel()
	srv := newFake(t, nil, nil)
	srv.FailNextWith(routeListRoles, 403, "RBAC is not enabled for this organization", 1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv),
		Steps: []resource.TestStep{{
			Config:      rolesConfig,
			ExpectError: regexp.MustCompile(`Cannot\s+list\s+the\s+roles(.|\n)*403(.|\n)*RBAC\s+is\s+not\s+enabled`),
		}},
	})
}
