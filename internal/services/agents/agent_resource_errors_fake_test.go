// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents_test

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/ippontech/terraform-provider-mistral/internal/providertest"
)

// A create that meets a 5xx is sent once: the agent may exist, and a replay
// would create a second one.
func TestAgentResourceCreateIsSentOnceOnA5xx(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.FailNext(routeCreate, 500, 1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      agentConfig(minimalAgent),
			ExpectError: regexp.MustCompile(`Cannot\s+create\s+the\s+agent(.|\n)*500`),
		}},
	})
	f.AssertCalled(routeCreate, 1)
}

func TestAgentResourceUpdateIsSentOnceOnA5xx(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: agentConfig(minimalAgent)},
			{
				PreConfig:   func() { f.FailNext(routeUpdate, 503, 1) },
				Config:      agentConfig(minimalAgent + `  description = "changed"`),
				ExpectError: regexp.MustCompile(`Cannot\s+update\s+the\s+agent(.|\n)*503`),
			},
		},
	})
	f.AssertCalled(routeUpdate, 1)
}

func TestAgentResourceReportsReadErrors(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: agentConfig(minimalAgent)},
			{
				PreConfig:   func() { f.FailNextWith(routeGet, 401, "Unauthorized", 1) },
				Config:      agentConfig(minimalAgent),
				ExpectError: regexp.MustCompile(`Cannot\s+read\s+the\s+agent(.|\n)*401`),
			},
		},
	})
}

func TestAgentResourceValidatesTheUnions(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	cases := map[string]struct{ body, err string }{
		"function without function": {
			`tools = [{ type = "function" }]`, `Missing\s+function`,
		},
		"library ids on a web search": {
			`tools = [{ type = "web_search", library_ids = ["lib_1"] }]`, `Unexpected\s+library_ids(.|\n)*web_search\s+tool`,
		},
		"connector without connector_id": {
			`tools = [{ type = "connector" }]`, `Missing\s+connector_id`,
		},
		"unknown tool": {
			`tools = [{ type = "mcp" }]`, `value\s+must\s+be\s+one\s+of`,
		},
		"parameters that are not an object": {
			`tools = [{ type = "function", function = { name = "f", parameters = "[1]" } }]`, `Invalid\s+JSON\s+object`,
		},
		"json_schema format without a schema": {
			`completion_args = { response_format = { type = "json_schema" } }`, `Missing\s+json_schema`,
		},
		"text format with a schema": {
			`completion_args = { response_format = { type = "text", json_schema = { name = "a", schema = "{}" } } }`, `Unexpected\s+json_schema`,
		},
		"empty metadata": {
			`metadata = {}`, `map\s+must\s+contain\s+at\s+least\s+1`,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
				Steps: []resource.TestStep{{
					Config:      agentConfig(minimalAgent + c.body),
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(c.err),
				}},
			})
		})
	}
	f.AssertCalled("", 0)
}

func TestAgentResourceReportsDeleteErrors(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: aliasConfig(minimalAgent)},
			{
				PreConfig:   func() { f.FailNext(routeDeleteAlias, 500, 4) },
				Config:      agentConfig(minimalAgent),
				ExpectError: regexp.MustCompile(`Cannot\s+delete\s+the\s+agent\s+alias(.|\n)*500`),
			},
			{
				PreConfig:   func() { f.FailNext(routeDelete, 500, 4) },
				Config:      agentConfig(minimalAgent),
				Destroy:     true,
				ExpectError: regexp.MustCompile(`Cannot\s+delete\s+the\s+agent(.|\n)*500`),
			},
		},
	})
}

func TestAgentAliasResourceReportsWriteErrors(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				PreConfig:   func() { f.FailNextWith(routeListAliases, 403, "Forbidden", 1) },
				Config:      aliasConfig(minimalAgent),
				ExpectError: regexp.MustCompile(`Cannot\s+create\s+the\s+agent\s+alias(.|\n)*403`),
			},
			{Config: aliasConfig(minimalAgent)},
			{
				PreConfig:   func() { f.FailNextWith(routePutAlias, 422, "Invalid version", 1) },
				Config:      aliasConfig(minimalAgent + `  description = "v1"`),
				ExpectError: regexp.MustCompile(`Cannot\s+point\s+the\s+agent\s+alias(.|\n)*422`),
			},
		},
	})
}

// A tool whose fields depend on a resource created in the same apply is
// checked at apply time.
func TestAgentResourceAcceptsToolsUnknownAtPlanTime(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: aliasConfig(minimalAgent) + `
resource "mistral_agent" "router" {
  name     = "router"
  model    = "mistral-small-latest"
  handoffs = [mistral_agent.this.id]
  tools    = [{ type = "document_library", library_ids = [mistral_agent.this.id] }]
  completion_args = {
    response_format = { type = mistral_agent.this.name == "" ? "text" : "json_object" }
  }
}
`,
		}},
	})
}
