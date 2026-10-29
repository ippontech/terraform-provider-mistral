// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package models_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/compare"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-mistral/internal/acctest"
)

// TestAccModelDataSource reads an alias, then the model it resolves to by
// its canonical ID, against the live API.
func TestAccModelDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: `
data "mistral_model" "alias" {
  id = "mistral-small-latest"
}

data "mistral_model" "canonical" {
  id = data.mistral_model.alias.canonical_id
}
`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.mistral_model.alias", tfjsonpath.New("id"), knownvalue.StringExact("mistral-small-latest")),
				statecheck.ExpectKnownValue("data.mistral_model.alias", tfjsonpath.New("owned_by"), knownvalue.StringExact("mistralai")),
				statecheck.ExpectKnownValue("data.mistral_model.alias", tfjsonpath.New("capabilities").AtMapKey("completion_chat"), knownvalue.Bool(true)),
				statecheck.CompareValuePairs(
					"data.mistral_model.alias", tfjsonpath.New("canonical_id"),
					"data.mistral_model.canonical", tfjsonpath.New("id"),
					compare.ValuesSame()),
				statecheck.CompareValuePairs(
					"data.mistral_model.canonical", tfjsonpath.New("id"),
					"data.mistral_model.canonical", tfjsonpath.New("canonical_id"),
					compare.ValuesSame()),
			},
		}},
	})
}
