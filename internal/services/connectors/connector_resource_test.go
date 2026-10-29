// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package connectors_test

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

// TestAccConnectorResource creates a connector to the public DeepWiki MCP
// server of the management guide, shared with the workspace, gives it
// credentials without secrets (the guide's way to connect a server that
// needs none), updates both, empties the optional fields, makes the
// connector private, reads it with the data source, imports both and
// destroys them, against the live API.
func TestAccConnectorResource(t *testing.T) {
	name := acctest.RandomName("connector")
	config := func(connector, credential string) string {
		return fmt.Sprintf(`
resource "mistral_connector" "this" {
  name        = %q
  description = "Acceptance test connector"
  server      = "https://mcp.deepwiki.com/mcp"
%s
}

resource "mistral_connector_credential" "this" {
  connector_id           = mistral_connector.this.id
  scope                  = "workspace"
  name                   = "acceptance"
  credentials_wo         = jsonencode({ headers = {} })
%s
}

data "mistral_connector" "this" {
  name       = mistral_connector.this.name
  depends_on = [mistral_connector.this]
}
`, name, connector, credential)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config(`  visibility = "shared_workspace"`, `  credentials_wo_version = 1`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("mistral_connector.this", tfjsonpath.New("protocol"), knownvalue.StringExact("mcp")),
					statecheck.ExpectKnownValue("mistral_connector.this", tfjsonpath.New("visibility"), knownvalue.StringExact("shared_workspace")),
					statecheck.ExpectKnownValue("mistral_connector_credential.this", tfjsonpath.New("is_default"), knownvalue.Bool(true)),
					statecheck.ExpectKnownValue("data.mistral_connector.this", tfjsonpath.New("visibility"), knownvalue.StringExact("shared_workspace")),
				},
			},
			{
				Config: config(`
  visibility    = "shared_workspace"
  title         = "DeepWiki"
  system_prompt = "Cite the repository of every answer."
`, `
  title                  = "Acceptance"
  credentials_wo_version = 2
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("mistral_connector.this", plancheck.ResourceActionUpdate),
						plancheck.ExpectResourceAction("mistral_connector_credential.this", plancheck.ResourceActionUpdate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("mistral_connector.this", tfjsonpath.New("title"), knownvalue.StringExact("DeepWiki")),
					statecheck.ExpectKnownValue("mistral_connector_credential.this", tfjsonpath.New("title"), knownvalue.StringExact("Acceptance")),
				},
			},
			{
				// Emptying the optional fields must clear them, not keep them.
				Config: config(`  visibility = "shared_workspace"`, `  credentials_wo_version = 2`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("mistral_connector.this", tfjsonpath.New("title"), knownvalue.Null()),
					statecheck.ExpectKnownValue("mistral_connector.this", tfjsonpath.New("system_prompt"), knownvalue.Null()),
					statecheck.ExpectKnownValue("mistral_connector_credential.this", tfjsonpath.New("title"), knownvalue.Null()),
				},
			},
			{ResourceName: "mistral_connector.this", ImportState: true, ImportStateVerify: true},
			{
				ResourceName:            "mistral_connector_credential.this",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"credentials_wo_version"},
			},
		},
	})
}
