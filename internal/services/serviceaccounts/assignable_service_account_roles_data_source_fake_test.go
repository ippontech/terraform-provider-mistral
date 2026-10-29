// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package serviceaccounts_test

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-mistral/internal/providertest"
	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

const rolesAddr = "data.mistral_assignable_service_account_roles.this"

// rolesConfig is a configuration with the data source on workspace.
func rolesConfig(workspace string) string {
	return providertest.ProviderConfig() + `
data "mistral_assignable_service_account_roles" "this" {
  workspace_id = "` + workspace + `"
}
`
}

// roleCheck is the exact check of one role of the list.
func roleCheck(id, name, description string, custom bool) knownvalue.Check {
	return knownvalue.ObjectExact(map[string]knownvalue.Check{
		"id":          knownvalue.StringExact(id),
		"name":        knownvalue.StringExact(name),
		"description": knownvalue.StringExact(description),
		"custom":      knownvalue.Bool(custom),
	})
}

// TestAssignableServiceAccountRolesDataSourceListsTheRoles checks every
// attribute of every role, a custom one included, in the order of the API.
func TestAssignableServiceAccountRolesDataSourceListsTheRoles(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.assignable[workspaceID] = append(f.assignable[workspaceID], assignableRole("44444444-4444-4444-8444-444444444444", "ci-deployer", "", true))

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: rolesConfig(workspaceID),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(rolesAddr, tfjsonpath.New("workspace_id"), knownvalue.StringExact(workspaceID)),
				statecheck.ExpectKnownValue(rolesAddr, tfjsonpath.New("roles"), knownvalue.ListExact([]knownvalue.Check{
					roleCheck(roleMember, "member", "No role-granted permission.", false),
					roleCheck(roleExecutor, "workflow_executor", "Registers and runs workflows.", false),
					roleCheck(roleDev, "dev", "Access to Studio.", false),
					roleCheck("44444444-4444-4444-8444-444444444444", "ci-deployer", "", true),
				})),
			},
		}},
	})
	// Every plan and apply of the step reads the data source, each time with
	// the query.
	query := "workspace_id=" + workspaceID
	f.AssertQueries(routeAssignableRoles, query, query, query)
	f.AssertCredential(routeAssignableRoles, sdktest.APIKey)
}

// TestAssignableServiceAccountRolesDataSourceEmptyList checks that a
// workspace without roles reads as an empty list, not a null one.
func TestAssignableServiceAccountRolesDataSourceEmptyList(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: rolesConfig("9e8d7c6b-5a49-4382-9170-6f5e4d3c2b1a"),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(rolesAddr, tfjsonpath.New("roles"), knownvalue.ListExact([]knownvalue.Check{})),
			},
		}},
	})
}

// The 404 of a workspace the key cannot see is our reading: the
// specification documents no error but the 422.
func TestAssignableServiceAccountRolesDataSourceWorkspaceNotFound(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.FailNextWith(routeAssignableRoles, 404, "Workspace not found", 1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      rolesConfig("missing"),
			ExpectError: regexp.MustCompile(`Workspace\s+not\s+found(.|\n)*No\s+workspace\s+"missing"\s+is\s+visible`),
		}},
	})
	f.AssertCalled(routeAssignableRoles, 1)
}

// TestAssignableServiceAccountRolesDataSourceReportsErrorsAfterRetries checks
// that a read is retried on a 5xx, then reported.
func TestAssignableServiceAccountRolesDataSourceReportsErrorsAfterRetries(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.FailNext(routeAssignableRoles, 503, 4)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      rolesConfig(workspaceID),
			ExpectError: regexp.MustCompile(`Cannot\s+list\s+the\s+assignable\s+service\s+account\s+roles(.|\n)*503`),
		}},
	})
	f.AssertCalled(routeAssignableRoles, 4)
}

// The role_ids of a service account come from the data source by name, as
// in the example of the Registry page.
func TestAssignableServiceAccountRolesDataSourceFeedsTheRoleIDs(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: rolesConfig(workspaceID) + `
resource "mistral_service_account" "worker" {
  workspace_id = data.mistral_assignable_service_account_roles.this.workspace_id
  name         = "prod-worker"
  role_ids     = [for r in data.mistral_assignable_service_account_roles.this.roles : r.id if r.name == "workflow_executor"]
}
`,
			ConfigPlanChecks: providertest.ExpectAction(accountAddr, plancheck.ResourceActionCreate),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(accountAddr, tfjsonpath.New("role_ids"), roleSet(roleExecutor)),
			},
		}},
	})
}

// TestAssignableServiceAccountRolesDataSourceNeedsTheAPIKey checks that the
// data source asks for the standard key, before any call.
func TestAssignableServiceAccountRolesDataSourceNeedsTheAPIKey(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: `
provider "mistral" {
  api_key       = ""
  admin_api_key = "` + sdktest.TestAdminKey + `"
}

data "mistral_assignable_service_account_roles" "this" {
  workspace_id = "` + workspaceID + `"
}
`,
			ExpectError: regexp.MustCompile(`Missing\s+Mistral\s+credential(.|\n)*"api_key"`),
		}},
	})
	f.AssertCalled("", 0)
}
