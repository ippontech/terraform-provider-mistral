// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package models_test

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-mistral/internal/providertest"
	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

const modelsAddr = "data.mistral_models.all"

var modelsConfig = providertest.ProviderConfig() + `
data "mistral_models" "all" {}
`

func TestModelsDataSourceListsEveryModel(t *testing.T) {
	t.Parallel()
	srv := newFake(t, medium(), fineTuned())

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv),
		Steps: []resource.TestStep{{
			Config: modelsConfig,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(modelsAddr, tfjsonpath.New("models"), knownvalue.ListSizeExact(2)),
				statecheck.ExpectKnownValue(modelsAddr, tfjsonpath.New("models").AtSliceIndex(0).AtMapKey("id"), knownvalue.StringExact("mistral-medium-2604")),
				statecheck.ExpectKnownValue(modelsAddr, tfjsonpath.New("models").AtSliceIndex(0).AtMapKey("aliases"),
					knownvalue.ListExact([]knownvalue.Check{knownvalue.StringExact("mistral-medium-latest")})),
				statecheck.ExpectKnownValue(modelsAddr, tfjsonpath.New("models").AtSliceIndex(0).AtMapKey("capabilities").AtMapKey("function_calling"), knownvalue.Bool(true)),
				statecheck.ExpectKnownValue(modelsAddr, tfjsonpath.New("models").AtSliceIndex(1).AtMapKey("id"), knownvalue.StringExact("ft:open-mistral-7b:abc:20240514:7e77")),
				statecheck.ExpectKnownValue(modelsAddr, tfjsonpath.New("models").AtSliceIndex(1).AtMapKey("base_model"), knownvalue.StringExact("open-mistral-7b")),
				statecheck.ExpectKnownValue(modelsAddr, tfjsonpath.New("models").AtSliceIndex(1).AtMapKey("created_at"), knownvalue.StringExact("2024-05-14T15:20:00Z")),
			},
		}},
	})
	srv.AssertCredential(routeList, sdktest.APIKey)
	srv.AssertCalled(routeGet, 0)
}

func TestModelsDataSourceEmptyList(t *testing.T) {
	t.Parallel()
	srv := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv),
		Steps: []resource.TestStep{{
			Config: modelsConfig,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(modelsAddr, tfjsonpath.New("models"), knownvalue.ListExact([]knownvalue.Check{})),
			},
		}},
	})
}

func TestModelsDataSourceReportsErrorsAfterRetries(t *testing.T) {
	t.Parallel()
	srv := newFake(t, medium())
	srv.FailNext(routeList, 503, 4)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv),
		Steps: []resource.TestStep{{
			Config:      modelsConfig,
			ExpectError: regexp.MustCompile(`Cannot\s+list\s+the\s+models(.|\n)*503\s+Service\s+Unavailable`),
		}},
	})
	srv.AssertCalled(routeList, 4)
}

func TestModelsDataSourceNeedsTheAPIKey(t *testing.T) {
	t.Parallel()
	srv := newFake(t, medium())

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv),
		Steps: []resource.TestStep{{
			Config: `
provider "mistral" {
  api_key       = ""
  admin_api_key = "` + sdktest.TestAdminKey + `"
}

data "mistral_models" "all" {}
`,
			ExpectError: regexp.MustCompile(`Missing\s+Mistral\s+credential(.|\n)*"api_key"`),
		}},
	})
	srv.AssertCalled(routeList, 0)
}
