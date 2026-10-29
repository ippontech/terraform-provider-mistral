// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package promptsandskills_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-mistral/internal/acctest"
)

// TestAccSkillResource creates a skill, gives it a second version with a
// text and a binary asset, removes an asset and the description in a third
// version, imports it, and destroys it, against the live API. The empty plan
// after each step checks that the API returns the assets in a form the
// state mapping recognizes.
func TestAccSkillResource(t *testing.T) {
	name := acctest.RandomName("skill")
	config := func(body string) string {
		return fmt.Sprintf(`
resource "mistral_skill" "this" {
  name = %q
  body = "Answer with the result of scripts/count.sh only."
%s
}
`, name, body)
	}
	version := func(v int64) statecheck.StateCheck {
		return statecheck.ExpectKnownValue("mistral_skill.this", tfjsonpath.New("version"), knownvalue.Int64Exact(v))
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: config(""), ConfigStateChecks: []statecheck.StateCheck{version(1)}},
			{
				Config: config(`
  description = "Use it to count the lines of a file."
  assets = {
    "scripts/count.sh" = { content = "wc -l \"$1\"", executable = true }
    "icon.png"         = { content_base64 = "iVBORw0KGgo=" }
  }
  notes = "Add the script"
`),
				ConfigStateChecks: []statecheck.StateCheck{
					version(2),
					statecheck.ExpectKnownValue("mistral_skill.this", tfjsonpath.New("assets").AtMapKey("scripts/count.sh").AtMapKey("executable"), knownvalue.Bool(true)),
				},
			},
			{
				Config: config(`
  assets = {
    "scripts/count.sh" = { content = "wc -l \"$1\"", executable = true }
  }
`),
				ConfigStateChecks: []statecheck.StateCheck{
					version(3),
					statecheck.ExpectKnownValue("mistral_skill.this", tfjsonpath.New("description"), knownvalue.Null()),
					statecheck.ExpectKnownValue("mistral_skill.this", tfjsonpath.New("assets"), knownvalue.MapSizeExact(1)),
				},
			},
			{ResourceName: "mistral_skill.this", ImportState: true, ImportStateVerify: true},
		},
	})
}
