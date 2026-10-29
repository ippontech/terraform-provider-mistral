// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents_test

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

const agentDataAddr = "data.mistral_agent.studio"

func agentDataConfig(id string) string {
	return providertest.ProviderConfig() + `
data "mistral_agent" "studio" {
  id = "` + id + `"
}
`
}

func TestAgentDataSourceReadsAnAgent(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	id := f.seed(map[string]any{"name": "studio", "model": "mistral-small-latest", "description": "Built in AI Studio", "instructions": ""})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: agentDataConfig(id),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(agentDataAddr, tfjsonpath.New("name"), knownvalue.StringExact("studio")),
				statecheck.ExpectKnownValue(agentDataAddr, tfjsonpath.New("model"), knownvalue.StringExact("mistral-small-latest")),
				statecheck.ExpectKnownValue(agentDataAddr, tfjsonpath.New("description"), knownvalue.StringExact("Built in AI Studio")),
				statecheck.ExpectKnownValue(agentDataAddr, tfjsonpath.New("instructions"), knownvalue.Null()),
				statecheck.ExpectKnownValue(agentDataAddr, tfjsonpath.New("version"), knownvalue.Int64Exact(0)),
				statecheck.ExpectKnownValue(agentDataAddr, tfjsonpath.New("created_at"), knownvalue.StringExact(at(1))),
			},
		}},
	})
	f.AssertCredential(routeGet, sdktest.APIKey)
}

func TestAgentDataSourceNotFound(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      agentDataConfig("ag_missing"),
			ExpectError: regexp.MustCompile(`Agent\s+not\s+found(.|\n)*No\s+agent\s+"ag_missing"`),
		}},
	})
}

func TestAgentDataSourceReportsOtherErrors(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.FailNext(routeGet, 500, 4)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      agentDataConfig("ag_1"),
			ExpectError: regexp.MustCompile(`Cannot\s+read\s+the\s+agent(.|\n)*500`),
		}},
	})
	f.AssertCalled(routeGet, 4)
}
