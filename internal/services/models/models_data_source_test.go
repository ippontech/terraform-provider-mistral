// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package models_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-mistral/internal/acctest"
)

// TestAccModelsDataSource lists the models of the test workspace, which has
// at least one Mistral model, against the live API.
func TestAccModelsDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: `data "mistral_models" "all" {}`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.mistral_models.all", tfjsonpath.New("models").AtSliceIndex(0).AtMapKey("id"), knownvalue.NotNull()),
				statecheck.ExpectKnownValue("data.mistral_models.all", tfjsonpath.New("models").AtSliceIndex(0).AtMapKey("capabilities"), knownvalue.NotNull()),
			},
		}},
	})
}
