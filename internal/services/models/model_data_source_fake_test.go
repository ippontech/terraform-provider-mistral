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

const modelAddr = "data.mistral_model.this"

func modelConfig(id string) string {
	return providertest.ProviderConfig() + `
data "mistral_model" "this" {
  id = "` + id + `"
}
`
}

func TestModelDataSourceReadsEveryField(t *testing.T) {
	t.Parallel()
	srv := newFake(t, medium(), fineTuned())

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv),
		Steps: []resource.TestStep{{
			Config: modelConfig("mistral-medium-2604"),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(modelAddr, tfjsonpath.New("id"), knownvalue.StringExact("mistral-medium-2604")),
				statecheck.ExpectKnownValue(modelAddr, tfjsonpath.New("canonical_id"), knownvalue.StringExact("mistral-medium-2604")),
				statecheck.ExpectKnownValue(modelAddr, tfjsonpath.New("name"), knownvalue.StringExact("Mistral Medium 3.5")),
				statecheck.ExpectKnownValue(modelAddr, tfjsonpath.New("description"), knownvalue.StringExact("Frontier model")),
				statecheck.ExpectKnownValue(modelAddr, tfjsonpath.New("type"), knownvalue.StringExact("base")),
				statecheck.ExpectKnownValue(modelAddr, tfjsonpath.New("owned_by"), knownvalue.StringExact("mistralai")),
				statecheck.ExpectKnownValue(modelAddr, tfjsonpath.New("created_at"), knownvalue.StringExact("2026-04-24T03:06:40Z")),
				statecheck.ExpectKnownValue(modelAddr, tfjsonpath.New("max_context_length"), knownvalue.Int64Exact(131072)),
				statecheck.ExpectKnownValue(modelAddr, tfjsonpath.New("aliases"), knownvalue.ListExact([]knownvalue.Check{knownvalue.StringExact("mistral-medium-latest")})),
				statecheck.ExpectKnownValue(modelAddr, tfjsonpath.New("deprecation"), knownvalue.Null()),
				statecheck.ExpectKnownValue(modelAddr, tfjsonpath.New("deprecation_replacement_model"), knownvalue.Null()),
				statecheck.ExpectKnownValue(modelAddr, tfjsonpath.New("default_temperature"), knownvalue.Float64Exact(0.3)),
				statecheck.ExpectKnownValue(modelAddr, tfjsonpath.New("base_model"), knownvalue.Null()),
				statecheck.ExpectKnownValue(modelAddr, tfjsonpath.New("archived"), knownvalue.Null()),
				statecheck.ExpectKnownValue(modelAddr, tfjsonpath.New("capabilities").AtMapKey("vision"), knownvalue.Bool(true)),
				statecheck.ExpectKnownValue(modelAddr, tfjsonpath.New("capabilities").AtMapKey("ocr"), knownvalue.Bool(false)),
			},
		}},
	})
	srv.AssertCredential(routeGet, sdktest.APIKey)
	srv.AssertCalled(routeList, 0)
}

func TestModelDataSourceResolvesAnAlias(t *testing.T) {
	t.Parallel()
	srv := newFake(t, medium())

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv),
		Steps: []resource.TestStep{{
			Config: modelConfig("mistral-medium-latest"),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(modelAddr, tfjsonpath.New("id"), knownvalue.StringExact("mistral-medium-latest")),
				statecheck.ExpectKnownValue(modelAddr, tfjsonpath.New("canonical_id"), knownvalue.StringExact("mistral-medium-2604")),
			},
		}},
	})
}

func TestModelDataSourceReadsAFineTunedDeprecatedModel(t *testing.T) {
	t.Parallel()
	srv := newFake(t, fineTuned())

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv),
		Steps: []resource.TestStep{{
			Config: modelConfig("ft:open-mistral-7b:abc:20240514:7e77"),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(modelAddr, tfjsonpath.New("type"), knownvalue.StringExact("fine-tuned")),
				statecheck.ExpectKnownValue(modelAddr, tfjsonpath.New("base_model"), knownvalue.StringExact("open-mistral-7b")),
				statecheck.ExpectKnownValue(modelAddr, tfjsonpath.New("archived"), knownvalue.Bool(false)),
				statecheck.ExpectKnownValue(modelAddr, tfjsonpath.New("aliases"), knownvalue.ListExact([]knownvalue.Check{})),
				statecheck.ExpectKnownValue(modelAddr, tfjsonpath.New("name"), knownvalue.Null()),
				statecheck.ExpectKnownValue(modelAddr, tfjsonpath.New("deprecation"), knownvalue.StringExact("2026-12-31T00:00:00Z")),
				statecheck.ExpectKnownValue(modelAddr, tfjsonpath.New("deprecation_replacement_model"), knownvalue.StringExact("mistral-small-latest")),
			},
		}},
	})
}

func TestModelDataSourceNotFound(t *testing.T) {
	t.Parallel()
	srv := newFake(t, medium())

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv),
		Steps: []resource.TestStep{{
			Config:      modelConfig("no-such-model"),
			ExpectError: regexp.MustCompile(`Model\s+not\s+found(.|\n)*No\s+model\s+"no-such-model"`),
		}},
	})
}

func TestModelDataSourceReportsOtherErrors(t *testing.T) {
	t.Parallel()
	srv := newFake(t, medium())
	srv.FailNextWith(routeGet, 401, "Invalid API Key", 1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv),
		Steps: []resource.TestStep{{
			Config:      modelConfig("mistral-medium-2604"),
			ExpectError: regexp.MustCompile(`Cannot\s+read\s+the\s+model(.|\n)*401\s+Unauthorized:\s+Invalid\s+API\s+Key`),
		}},
	})
}

// A card with only the fields the specification requires maps its absent
// fields to null, and its absent aliases to an empty list.
func TestModelDataSourceReadsAMinimalCard(t *testing.T) {
	t.Parallel()
	srv := newFake(t, map[string]any{"id": "codestral-2508", "capabilities": map[string]any{}})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv),
		Steps: []resource.TestStep{{
			Config: modelConfig("codestral-2508"),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(modelAddr, tfjsonpath.New("aliases"), knownvalue.ListExact([]knownvalue.Check{})),
				statecheck.ExpectKnownValue(modelAddr, tfjsonpath.New("type"), knownvalue.Null()),
				statecheck.ExpectKnownValue(modelAddr, tfjsonpath.New("owned_by"), knownvalue.Null()),
				statecheck.ExpectKnownValue(modelAddr, tfjsonpath.New("created_at"), knownvalue.Null()),
				statecheck.ExpectKnownValue(modelAddr, tfjsonpath.New("default_temperature"), knownvalue.Null()),
				statecheck.ExpectKnownValue(modelAddr, tfjsonpath.New("capabilities").AtMapKey("completion_chat"), knownvalue.Bool(false)),
			},
		}},
	})
}
