// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package serviceaccounts_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-mistral/internal/acctest"
)

// TestAccServiceAccountResource lists the assignable roles of the workspace
// of MISTRAL_API_KEY, creates a service account with the first of them,
// updates its description and its roles, empties the description, imports
// it and destroys it, against the live API. The API takes the workspace ID in
// the body, and no endpoint of the provider reads the workspace of the key:
// acctest.EnvWorkspaceID gives it.
func TestAccServiceAccountResource(t *testing.T) {
	acctest.PreCheck(t)
	workspace := acctest.Fixture(t, acctest.EnvWorkspaceID)
	name := acctest.RandomName("sa")
	config := func(body string) string {
		return fmt.Sprintf(`
data "mistral_assignable_service_account_roles" "this" {
  workspace_id = %q
}

locals {
  role_ids = data.mistral_assignable_service_account_roles.this.roles[*].id
}

resource "mistral_service_account" "this" {
  workspace_id = data.mistral_assignable_service_account_roles.this.workspace_id
  name         = %q
%s
}
`, workspace, name, body)
	}
	addr := "mistral_service_account.this"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				// The empty plan after the apply checks that the create
				// assigned the roles of its body.
				Config: config(`  role_ids = [local.role_ids[0]]`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(addr, tfjsonpath.New("name"), knownvalue.StringExact(name)),
					statecheck.ExpectKnownValue(addr, tfjsonpath.New("workspace_id"), knownvalue.StringExact(workspace)),
					statecheck.ExpectKnownValue(addr, tfjsonpath.New("role_ids"), knownvalue.SetSizeExact(1)),
				},
			},
			{
				Config: config(`
  description = "Terraform acceptance test"
  role_ids    = slice(local.role_ids, 0, min(2, length(local.role_ids)))
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(addr, tfjsonpath.New("description"), knownvalue.StringExact("Terraform acceptance test")),
				},
			},
			{
				// Removing the description must clear it, not keep it.
				Config: config(`  role_ids = [local.role_ids[0]]`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(addr, tfjsonpath.New("description"), knownvalue.Null()),
					statecheck.ExpectKnownValue(addr, tfjsonpath.New("role_ids"), knownvalue.SetSizeExact(1)),
				},
			},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true},
		},
	})
}
