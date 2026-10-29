// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents_test

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

// TestAccAgentResource creates an agent, updates it into a new version with
// tools and sampling arguments, points an alias at each version, imports
// both, and destroys them, against the live API.
func TestAccAgentResource(t *testing.T) {
	name := acctest.RandomName("agent")
	config := func(body string) string {
		return fmt.Sprintf(`
resource "mistral_agent" "this" {
  name  = %q
  model = "mistral-small-latest"
%s
}

resource "mistral_agent_alias" "production" {
  agent_id = mistral_agent.this.id
  alias    = "production"
  version  = mistral_agent.this.version
}

data "mistral_agent" "this" {
  id = mistral_agent.this.id
}
`, name, body)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config(""),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("mistral_agent.this", tfjsonpath.New("version"), knownvalue.Int64Exact(0)),
					statecheck.ExpectKnownValue("mistral_agent.this", tfjsonpath.New("completion_args").AtMapKey("tool_choice"), knownvalue.StringExact("auto")),
					statecheck.ExpectKnownValue("mistral_agent_alias.production", tfjsonpath.New("version"), knownvalue.Int64Exact(0)),
				},
			},
			{
				Config: config(`
  description  = "Acceptance test agent"
  instructions = "Answer in one sentence."
  tools = [
    { type = "web_search" },
    {
      type = "function"
      function = {
        name       = "get_order"
        parameters = jsonencode({ type = "object", properties = { order_id = { type = "string" } } })
      }
    },
  ]
  completion_args = { temperature = 0.2, max_tokens = 256 }
  metadata        = { purpose = "terraform-acceptance-test" }
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("mistral_agent.this", plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("mistral_agent.this", tfjsonpath.New("version"), knownvalue.Int64Exact(1)),
					statecheck.ExpectKnownValue("mistral_agent_alias.production", tfjsonpath.New("version"), knownvalue.Int64Exact(1)),
					statecheck.ExpectKnownValue("data.mistral_agent.this", tfjsonpath.New("version"), knownvalue.Int64Exact(1)),
				},
			},
			{
				// Emptying the optional fields must clear them, not keep them.
				Config: config(""),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("mistral_agent.this", tfjsonpath.New("description"), knownvalue.Null()),
					statecheck.ExpectKnownValue("mistral_agent.this", tfjsonpath.New("tools"), knownvalue.ListExact([]knownvalue.Check{})),
					statecheck.ExpectKnownValue("mistral_agent.this", tfjsonpath.New("metadata"), knownvalue.Null()),
				},
			},
			{ResourceName: "mistral_agent.this", ImportState: true, ImportStateVerify: true},
			{ResourceName: "mistral_agent_alias.production", ImportState: true, ImportStateVerify: true},
		},
	})
}
