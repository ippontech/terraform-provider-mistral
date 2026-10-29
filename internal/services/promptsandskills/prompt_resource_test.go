// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package promptsandskills_test

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

// TestAccPromptResource creates a prompt, gives it a second version with a
// variable and display fields, shares it with the workspace as reader,
// updates its notes alone, empties its optional fields, imports it, and
// destroys it, against the live API. Each step checks one reading of the
// specification that the fake encodes: see the package CLAUDE.md.
func TestAccPromptResource(t *testing.T) {
	name := acctest.RandomName("prompt")
	config := func(body string) string {
		return fmt.Sprintf(`
resource "mistral_prompt" "this" {
  name = %q
%s
}
`, name, body)
	}
	version := func(v int64) statecheck.StateCheck {
		return statecheck.ExpectKnownValue("mistral_prompt.this", tfjsonpath.New("version"), knownvalue.Int64Exact(v))
	}
	update := func() resource.ConfigPlanChecks {
		return resource.ConfigPlanChecks{
			PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("mistral_prompt.this", plancheck.ResourceActionUpdate)},
		}
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				// The first version is 1, and an absent scope reads as private.
				Config: config(`  content = "You are a terse assistant."`),
				ConfigStateChecks: []statecheck.StateCheck{
					version(1),
					statecheck.ExpectKnownValue("mistral_prompt.this", tfjsonpath.New("sharing_scope"), knownvalue.StringExact("private")),
				},
			},
			{
				Config: config(`
  title              = "Acceptance test prompt"
  description        = "Created by TestAccPromptResource."
  content            = "You are a terse assistant for {{language}}."
  variables          = [{ name = "language" }]
  notes              = "Add the language"
  sharing_scope      = "workspace"
  workspace_relation = "reader"
`),
				ConfigPlanChecks:  update(),
				ConfigStateChecks: []statecheck.StateCheck{version(2)},
			},
			{
				// New notes alone update version 2 and create no version.
				Config: config(`
  title              = "Acceptance test prompt"
  description        = "Created by TestAccPromptResource."
  content            = "You are a terse assistant for {{language}}."
  variables          = [{ name = "language" }]
  notes              = "Pin the language"
  sharing_scope      = "workspace"
  workspace_relation = "reader"
`),
				ConfigPlanChecks:  update(),
				ConfigStateChecks: []statecheck.StateCheck{version(2)},
			},
			{
				// Emptying the optional fields must clear them: "" on the
				// update of the prompt, an empty variables list in a version.
				Config:           config(`  content = "You are a terse assistant."`),
				ConfigPlanChecks: update(),
				ConfigStateChecks: []statecheck.StateCheck{
					version(3),
					statecheck.ExpectKnownValue("mistral_prompt.this", tfjsonpath.New("title"), knownvalue.Null()),
					statecheck.ExpectKnownValue("mistral_prompt.this", tfjsonpath.New("variables"), knownvalue.Null()),
					statecheck.ExpectKnownValue("mistral_prompt.this", tfjsonpath.New("sharing_scope"), knownvalue.StringExact("private")),
				},
			},
			{ResourceName: "mistral_prompt.this", ImportState: true, ImportStateVerify: true},
		},
	})
}
