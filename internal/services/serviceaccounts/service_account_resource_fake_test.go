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
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-mistral/internal/providertest"
	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

const accountAddr = "mistral_service_account.worker"

// uuid matches the IDs the fake gives, as the API's are UUIDs.
var uuid = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// accountConfig is a configuration with the service account "worker" in the
// workspace of the tests, body giving its other arguments.
func accountConfig(body string) string {
	return providertest.ProviderConfig() + `
resource "mistral_service_account" "worker" {
  workspace_id = "` + workspaceID + `"
` + body + `
}
`
}

const minimalAccount = `
  name     = "prod-worker"
  role_ids = ["` + roleExecutor + `"]
`

// roleSet is the exact check of a role_ids set.
func roleSet(ids ...string) knownvalue.Check {
	checks := make([]knownvalue.Check, 0, len(ids))
	for _, id := range ids {
		checks = append(checks, knownvalue.StringExact(id))
	}
	return knownvalue.SetExact(checks)
}

// The lifecycle changes the description and the roles together, then the
// description alone (emptied), then the roles alone: each update sends only
// the call of what changed.
func TestServiceAccountResourceLifecycle(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config:           accountConfig(minimalAccount),
				ConfigPlanChecks: providertest.ExpectAction(accountAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(accountAddr, tfjsonpath.New("id"), knownvalue.StringRegexp(uuid)),
					statecheck.ExpectKnownValue(accountAddr, tfjsonpath.New("name"), knownvalue.StringExact("prod-worker")),
					statecheck.ExpectKnownValue(accountAddr, tfjsonpath.New("workspace_id"), knownvalue.StringExact(workspaceID)),
					statecheck.ExpectKnownValue(accountAddr, tfjsonpath.New("description"), knownvalue.Null()),
					statecheck.ExpectKnownValue(accountAddr, tfjsonpath.New("role_ids"), roleSet(roleExecutor)),
					statecheck.ExpectKnownValue(accountAddr, tfjsonpath.New("organization_id"), knownvalue.StringExact(organizationID)),
					statecheck.ExpectKnownValue(accountAddr, tfjsonpath.New("created_at"), knownvalue.StringExact(at(1))),
					statecheck.ExpectKnownValue(accountAddr, tfjsonpath.New("updated_at"), knownvalue.StringExact(at(1))),
				},
			},
			{
				Config: accountConfig(`
  name        = "prod-worker"
  description = "Runs the workflow workers"
  role_ids    = ["` + roleDev + `", "` + roleExecutor + `"]
`),
				ConfigPlanChecks: providertest.ExpectAction(accountAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(accountAddr, tfjsonpath.New("description"), knownvalue.StringExact("Runs the workflow workers")),
					statecheck.ExpectKnownValue(accountAddr, tfjsonpath.New("role_ids"), roleSet(roleExecutor, roleDev)),
					statecheck.ExpectKnownValue(accountAddr, tfjsonpath.New("created_at"), knownvalue.StringExact(at(1))),
					statecheck.ExpectKnownValue(accountAddr, tfjsonpath.New("updated_at"), knownvalue.StringExact(at(2))),
				},
			},
			{
				Config: accountConfig(`
  name     = "prod-worker"
  role_ids = ["` + roleDev + `", "` + roleExecutor + `"]
`),
				ConfigPlanChecks: providertest.ExpectAction(accountAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(accountAddr, tfjsonpath.New("description"), knownvalue.Null()),
					statecheck.ExpectKnownValue(accountAddr, tfjsonpath.New("updated_at"), knownvalue.StringExact(at(3))),
				},
			},
			{
				Config: accountConfig(`
  name     = "prod-worker"
  role_ids = ["` + roleDev + `"]
`),
				ConfigPlanChecks: providertest.ExpectAction(accountAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(accountAddr, tfjsonpath.New("role_ids"), roleSet(roleDev)),
					statecheck.ExpectKnownValue(accountAddr, tfjsonpath.New("description"), knownvalue.Null()),
					statecheck.ExpectKnownValue(accountAddr, tfjsonpath.New("updated_at"), knownvalue.StringExact(at(3))),
				},
			},
			{
				ResourceName:      accountAddr,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				ResourceName:    accountAddr,
				ImportState:     true,
				ImportStateKind: resource.ImportBlockWithResourceIdentity,
			},
		},
	})

	f.AssertJSONBodies(routeCreate, `{"name": "prod-worker", "workspace_id": "`+workspaceID+`", "description": null, "role_ids": ["`+roleExecutor+`"]}`)
	f.AssertJSONBodies(routeUpdate, `{"description": "Runs the workflow workers"}`, `{"description": null}`)
	// The role IDs are sent sorted, whatever the order of the configuration.
	f.AssertJSONBodies(routeSetRoles, `{"role_ids": ["`+roleExecutor+`", "`+roleDev+`"]}`, `{"role_ids": ["`+roleDev+`"]}`)
	f.AssertCalled(routeDelete, 1)
	f.AssertCredential("", sdktest.APIKey)
}

// name and workspace_id cannot be updated: a change replaces the account,
// which the create then makes with its description and every role.
func TestServiceAccountResourceReplacesOnANameOrWorkspaceChange(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	const otherWorkspace = "9e8d7c6b-5a49-4382-9170-6f5e4d3c2b1a"
	full := func(name string) string {
		return `
  name        = "` + name + `"
  description = "Runs the workflow workers"
  role_ids    = ["` + roleDev + `", "` + roleExecutor + `"]
`
	}
	var firstID string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config:           accountConfig(full("prod-worker")),
				ConfigPlanChecks: providertest.ExpectAction(accountAddr, plancheck.ResourceActionCreate),
				Check: func(s *terraform.State) error {
					firstID = s.RootModule().Resources[accountAddr].Primary.ID
					return nil
				},
			},
			{
				Config:           accountConfig(full("batch-worker")),
				ConfigPlanChecks: providertest.ExpectAction(accountAddr, plancheck.ResourceActionDestroyBeforeCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(accountAddr, tfjsonpath.New("name"), knownvalue.StringExact("batch-worker")),
					statecheck.ExpectKnownValue(accountAddr, tfjsonpath.New("role_ids"), roleSet(roleExecutor, roleDev)),
				},
			},
			{
				Config: providertest.ProviderConfig() + `
resource "mistral_service_account" "worker" {
  workspace_id = "` + otherWorkspace + `"
` + full("batch-worker") + `
}
`,
				ConfigPlanChecks: providertest.ExpectAction(accountAddr, plancheck.ResourceActionDestroyBeforeCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(accountAddr, tfjsonpath.New("workspace_id"), knownvalue.StringExact(otherWorkspace)),
				},
			},
		},
	})

	if _, ok := f.accounts.Get(firstID); ok {
		t.Errorf("the first account %s was not deleted", firstID)
	}
	body := func(name, workspace string) string {
		return `{"name": "` + name + `", "workspace_id": "` + workspace + `", "description": "Runs the workflow workers",
			"role_ids": ["` + roleExecutor + `", "` + roleDev + `"]}`
	}
	f.AssertJSONBodies(routeCreate, body("prod-worker", workspaceID), body("batch-worker", workspaceID), body("batch-worker", otherWorkspace))
	f.AssertCalled(routeUpdate, 0)
	f.AssertCalled(routeSetRoles, 0)
}

// TestServiceAccountResourceRecreatesAnAccountDeletedOutsideTerraform checks
// that a 404 on refresh removes the account, so the plan creates it again.
func TestServiceAccountResourceRecreatesAnAccountDeletedOutsideTerraform(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	var id string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config: accountConfig(minimalAccount),
				Check: func(s *terraform.State) error {
					id = s.RootModule().Resources[accountAddr].Primary.ID
					return nil
				},
			},
			{
				PreConfig:        func() { f.accounts.Delete(id) },
				Config:           accountConfig(minimalAccount),
				ConfigPlanChecks: providertest.ExpectAction(accountAddr, plancheck.ResourceActionCreate),
			},
		},
	})
	f.AssertCalled(routeCreate, 2)
}

// An account the API still returns with a deleted_at, or whose roles answer
// 404, is gone: the refresh plans a create.
func TestServiceAccountResourceTreatsADeletedAccountAsGone(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	var id string
	expectCreate := resource.ConfigPlanChecks{
		PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectResourceAction(accountAddr, plancheck.ResourceActionCreate)},
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config: accountConfig(minimalAccount),
				Check: func(s *terraform.State) error {
					id = s.RootModule().Resources[accountAddr].Primary.ID
					return nil
				},
			},
			{
				PreConfig: func() { f.FailNextWith(routeListRoles, 404, "Service account not found", 1) },
				Config:    accountConfig(minimalAccount),
				PlanOnly:  true, ExpectNonEmptyPlan: true, ConfigPlanChecks: expectCreate,
			},
			{
				PreConfig: func() { f.accounts.Patch(id, map[string]any{"deleted_at": at(5)}) },
				Config:    accountConfig(minimalAccount),
				PlanOnly:  true, ExpectNonEmptyPlan: true, ConfigPlanChecks: expectCreate,
			},
		},
	})
}

// An account created in the Admin Console imports with an import block and
// plans no change: an empty description reads as null.
func TestServiceAccountResourceImportsAnAccountCreatedOutsideTerraform(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	id := f.seed(workspaceID, "console-worker", "", roleExecutor, roleDev)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig() + `
import {
  to = mistral_service_account.worker
  id = "` + id + `"
}

resource "mistral_service_account" "worker" {
  workspace_id = "` + workspaceID + `"
  name         = "console-worker"
  role_ids     = ["` + roleExecutor + `", "` + roleDev + `"]
}
`,
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(accountAddr, plancheck.ResourceActionNoop)},
			},
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(accountAddr, tfjsonpath.New("id"), knownvalue.StringExact(id)),
				statecheck.ExpectKnownValue(accountAddr, tfjsonpath.New("description"), knownvalue.Null()),
			},
		}},
	})
	f.AssertCalled(routeUpdate, 0)
	f.AssertCalled(routeSetRoles, 0)
}

// An account that holds no role, as the service accounts guide says a new
// one made in the Admin Console does, reads with null role_ids: the plan sets
// the configured roles.
func TestServiceAccountResourceSetsTheRolesOfAnAccountWithoutRoles(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	id := f.seed(workspaceID, "console-worker", nil)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig() + `
import {
  to = mistral_service_account.worker
  id = "` + id + `"
}

resource "mistral_service_account" "worker" {
  workspace_id = "` + workspaceID + `"
  name         = "console-worker"
  role_ids     = ["` + roleExecutor + `"]
}
`,
			ConfigPlanChecks: providertest.ExpectAction(accountAddr, plancheck.ResourceActionUpdate),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(accountAddr, tfjsonpath.New("role_ids"), roleSet(roleExecutor)),
			},
		}},
	})
	f.AssertJSONBodies(routeSetRoles, `{"role_ids": ["`+roleExecutor+`"]}`)
	f.AssertCalled(routeUpdate, 0)
}
