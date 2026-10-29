// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-mistral/internal/providertest"
)

// An agent changed in AI Studio shows as drift, and the apply writes the
// configuration back as a new version.
func TestAgentResourceRevertsAChangeMadeOutsideTerraform(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	var id string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config: agentConfig(minimalAgent),
				Check: func(s *terraform.State) error {
					id = s.RootModule().Resources[agentAddr].Primary.ID
					return nil
				},
			},
			{
				PreConfig: func() {
					f.agents.Patch(id, map[string]any{
						"instructions": "Changed in AI Studio", "version": 1, "versions": []any{0, 1},
						"tools": []any{map[string]any{"type": "web_search"}},
					})
				},
				Config:           agentConfig(minimalAgent),
				ConfigPlanChecks: providertest.ExpectAction(agentAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(agentAddr, tfjsonpath.New("instructions"), knownvalue.Null()),
					statecheck.ExpectKnownValue(agentAddr, tfjsonpath.New("tools"), knownvalue.ListExact([]knownvalue.Check{})),
					statecheck.ExpectKnownValue(agentAddr, tfjsonpath.New("version"), knownvalue.Int64Exact(2)),
				},
			},
		},
	})
}

// An agent created outside Terraform imports with an import block and plans
// no change: the fields its card leaves out read as the defaults of the
// schema, and a metadata value of another JSON type reads as its JSON text.
func TestAgentResourceImportsAnAgentCreatedOutsideTerraform(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	id := f.seed(map[string]any{
		"name": "studio", "model": "mistral-small-latest",
		"tools": []any{map[string]any{"type": "function", "function": map[string]any{
			"name": "ping", "parameters": map[string]any{"type": "object"},
		}}},
		"metadata": map[string]any{"tier": 2, "owner": "ops"},
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig() + `
import {
  to = mistral_agent.this
  id = "` + id + `"
}

resource "mistral_agent" "this" {
  name     = "studio"
  model    = "mistral-small-latest"
  tools    = [{ type = "function", function = { name = "ping", parameters = jsonencode({ type = "object" }) } }]
  metadata = { tier = "2", owner = "ops" }
}
`,
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(agentAddr, plancheck.ResourceActionNoop)},
			},
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(agentAddr, tfjsonpath.New("metadata").AtMapKey("tier"), knownvalue.StringExact("2")),
				statecheck.ExpectKnownValue(agentAddr, tfjsonpath.New("tools").AtSliceIndex(0).AtMapKey("function").AtMapKey("strict"), knownvalue.Bool(false)),
			},
		}},
	})
	f.AssertCalled(routeUpdate, 0)
}
