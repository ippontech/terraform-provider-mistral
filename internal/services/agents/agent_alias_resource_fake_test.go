// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents_test

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-mistral/internal/providertest"
	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

const aliasAddr = "mistral_agent_alias.production"

func aliasConfig(agentBody string) string {
	return agentConfig(agentBody) + `
resource "mistral_agent_alias" "production" {
  agent_id = mistral_agent.this.id
  alias    = "production"
  version  = mistral_agent.this.version
}
`
}

func TestAgentAliasResourceLifecycle(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	var agentID string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config:           aliasConfig(minimalAgent),
				ConfigPlanChecks: providertest.ExpectAction(aliasAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(aliasAddr, tfjsonpath.New("version"), knownvalue.Int64Exact(0)),
					statecheck.ExpectKnownValue(aliasAddr, tfjsonpath.New("id"), knownvalue.StringRegexp(regexp.MustCompile(`^ag_.+:production$`))),
					statecheck.ExpectKnownValue(aliasAddr, tfjsonpath.New("created_at"), knownvalue.StringExact(at(2))),
				},
				Check: func(s *terraform.State) error {
					agentID = s.RootModule().Resources[agentAddr].Primary.ID
					return nil
				},
			},
			{
				// The agent update creates version 1; the alias follows it.
				Config:           aliasConfig(minimalAgent + `  description = "v1"`),
				ConfigPlanChecks: providertest.ExpectAction(aliasAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(aliasAddr, tfjsonpath.New("version"), knownvalue.Int64Exact(1)),
					statecheck.ExpectKnownValue(aliasAddr, tfjsonpath.New("created_at"), knownvalue.StringExact(at(2))),
					statecheck.ExpectKnownValue(aliasAddr, tfjsonpath.New("updated_at"), knownvalue.StringExact(at(4))),
				},
			},
			{
				ResourceName:      aliasAddr,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				ResourceName:    aliasAddr,
				ImportState:     true,
				ImportStateKind: resource.ImportBlockWithResourceIdentity,
			},
		},
	})

	f.AssertQueries(routePutAlias, "alias=production&version=0", "alias=production&version=1")
	f.AssertQueries(routeDeleteAlias, "alias=production")
	if want := "/v1/agents/" + agentID + "/aliases"; f.Requests(routePutAlias)[0].Path != want {
		t.Errorf("PUT path = %s, want %s", f.Requests(routePutAlias)[0].Path, want)
	}
	f.AssertCredential("", sdktest.APIKey)
}

// The PUT is an upsert: a create must not take over an alias that exists
// outside Terraform.
func TestAgentAliasResourceRefusesToTakeOverAnAlias(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	id := f.seed(map[string]any{"name": "studio", "model": "mistral-small-latest"})
	f.aliases.Put(map[string]any{"key": id + ":production", "agent_id": id, "alias": "production", "version": 0})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig() + `
resource "mistral_agent_alias" "production" {
  agent_id = "` + id + `"
  alias    = "production"
  version  = 0
}
`,
			ExpectError: regexp.MustCompile(`Agent\s+alias\s+already\s+exists(.|\n)*import\s+block\s+\(id\s+=\s+"ag_`),
		}},
	})
	f.AssertCalled(routePutAlias, 0)
}

func TestAgentAliasResourceRecreatesAnAliasDeletedOutsideTerraform(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	var key string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config: aliasConfig(minimalAgent),
				Check: func(s *terraform.State) error {
					key = s.RootModule().Resources[aliasAddr].Primary.ID
					return nil
				},
			},
			{
				PreConfig:        func() { f.aliases.Delete(key) },
				Config:           aliasConfig(minimalAgent),
				ConfigPlanChecks: providertest.ExpectAction(aliasAddr, plancheck.ResourceActionCreate),
			},
		},
	})
}

// When the agent is gone, so is the alias: the list answers 404 and Read
// removes the alias from the state.
func TestAgentAliasResourceFollowsItsAgentDeletedOutsideTerraform(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	var agentID string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config: aliasConfig(minimalAgent),
				Check: func(s *terraform.State) error {
					agentID = s.RootModule().Resources[agentAddr].Primary.ID
					return nil
				},
			},
			{
				PreConfig:        func() { f.agents.Delete(agentID) },
				Config:           aliasConfig(minimalAgent),
				ConfigPlanChecks: providertest.ExpectAction(aliasAddr, plancheck.ResourceActionCreate),
			},
		},
	})
}

func TestAgentAliasResourceRejectsAMalformedImportID(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: aliasConfig(minimalAgent)},
			{
				ResourceName:  aliasAddr,
				ImportState:   true,
				ImportStateId: "production",
				ExpectError:   regexp.MustCompile(`Invalid\s+import\s+ID(.|\n)*<agent_id>:<alias>`),
			},
		},
	})
}

func TestAgentAliasResourceReportsErrors(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config: aliasConfig(minimalAgent) + "\n# version 7 does not exist\n" + `resource "mistral_agent_alias" "missing" {
  agent_id = mistral_agent.this.id
  alias    = "missing"
  version  = 7
}
`,
				ExpectError: regexp.MustCompile(`Cannot\s+point\s+the\s+agent\s+alias(.|\n)*404`),
			},
			{
				PreConfig:   func() { f.FailNext(routeListAliases, 500, 4) },
				Config:      aliasConfig(minimalAgent),
				ExpectError: regexp.MustCompile(`Cannot\s+read\s+the\s+agent\s+alias(.|\n)*500`),
			},
		},
	})
}
