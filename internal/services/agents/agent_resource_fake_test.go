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

const agentAddr = "mistral_agent.this"

func agentConfig(body string) string {
	return providertest.ProviderConfig() + "resource \"mistral_agent\" \"this\" {\n" + body + "\n}\n"
}

const minimalAgent = `
  name  = "support"
  model = "mistral-medium-latest"
`

// fullAgent sets every argument. json_schema.schema is a heredoc with its own
// spacing and key order: the API returns it reformatted, and the state keeps
// the configured text.
const fullAgent = `
  name         = "support"
  model        = "mistral-medium-latest"
  description  = "Answers support questions"
  instructions = "Answer from the documentation only."
  tools = [
    { type = "web_search" },
    {
      type               = "document_library"
      library_ids        = ["lib_1"]
      tool_configuration = { requires_confirmation = ["search"] }
    },
    {
      type = "function"
      function = {
        name       = "lookup_order"
        strict     = true
        parameters = jsonencode({ type = "object", properties = { order_id = { type = "string" } } })
      }
    },
  ]
  completion_args = {
    temperature = 0.3
    max_tokens  = 512
    stop        = ["END"]
    response_format = {
      type = "json_schema"
      json_schema = {
        name   = "answer"
        schema = <<-EOT
          { "type": "object",  "properties": { "text": { "type": "string" } } }
        EOT
      }
    }
  }
  handoffs        = ["ag_escalation"]
  metadata        = { team = "support", env = "prod" }
  version_message = "Add the tools"
`

// trimmedAgent empties description and handoffs, removes a metadata key, a
// tool and the response format, and changes the temperature.
const trimmedAgent = `
  name         = "support"
  model        = "mistral-medium-latest"
  instructions = "Answer from the documentation only."
  tools = [
    {
      type               = "document_library"
      library_ids        = ["lib_1"]
      tool_configuration = { requires_confirmation = ["search"] }
    },
    {
      type = "function"
      function = {
        name       = "lookup_order"
        strict     = true
        parameters = jsonencode({ type = "object", properties = { order_id = { type = "string" } } })
      }
    },
  ]
  completion_args = {
    temperature = 0.5
    max_tokens  = 512
    stop        = ["END"]
  }
  metadata        = { team = "support" }
  version_message = "Trim the tools"
`

const (
	nullArgs = `"stop": null, "presence_penalty": null, "frequency_penalty": null, "top_p": null, "random_seed": null, "reasoning_effort": null`

	createBody = `{
		"name": "support", "model": "mistral-medium-latest", "description": null, "instructions": null,
		"tools": [], "handoffs": null, "metadata": null, "version_message": null,
		"completion_args": {` + nullArgs + `, "temperature": null, "max_tokens": null, "response_format": null, "tool_choice": "auto"}
	}`

	lookupOrder = `{"type": "function", "function": {"name": "lookup_order", "description": "", "strict": true,
		"parameters": {"type": "object", "properties": {"order_id": {"type": "string"}}}}}`
	library = `{"type": "document_library", "library_ids": ["lib_1"], "tool_configuration": {"requires_confirmation": ["search"]}}`

	fullBody = `{
		"name": "support", "model": "mistral-medium-latest", "description": "Answers support questions",
		"instructions": "Answer from the documentation only.",
		"tools": [{"type": "web_search"}, ` + library + `, ` + lookupOrder + `],
		"completion_args": {"stop": ["END"], "presence_penalty": null, "frequency_penalty": null, "temperature": 0.3, "top_p": null,
			"max_tokens": 512, "random_seed": null, "reasoning_effort": null, "tool_choice": "auto",
			"response_format": {"type": "json_schema", "json_schema": {"name": "answer", "strict": false,
				"schema": {"type": "object", "properties": {"text": {"type": "string"}}}}}},
		"handoffs": ["ag_escalation"], "metadata": {"team": "support", "env": "prod"}, "version_message": "Add the tools"
	}`

	trimmedBody = `{
		"name": "support", "model": "mistral-medium-latest", "description": null,
		"instructions": "Answer from the documentation only.",
		"tools": [` + library + `, ` + lookupOrder + `],
		"completion_args": {"stop": ["END"], "presence_penalty": null, "frequency_penalty": null, "temperature": 0.5, "top_p": null,
			"max_tokens": 512, "random_seed": null, "reasoning_effort": null, "tool_choice": "auto", "response_format": null},
		"handoffs": null, "metadata": {"team": "support"}, "version_message": "Trim the tools"
	}`
)

func TestAgentResourceLifecycle(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config:           agentConfig(minimalAgent),
				ConfigPlanChecks: providertest.ExpectAction(agentAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(agentAddr, tfjsonpath.New("id"), knownvalue.StringRegexp(regexp.MustCompile(`^ag_`))),
					statecheck.ExpectKnownValue(agentAddr, tfjsonpath.New("version"), knownvalue.Int64Exact(0)),
					statecheck.ExpectKnownValue(agentAddr, tfjsonpath.New("tools"), knownvalue.ListExact([]knownvalue.Check{})),
					statecheck.ExpectKnownValue(agentAddr, tfjsonpath.New("completion_args").AtMapKey("tool_choice"), knownvalue.StringExact("auto")),
					statecheck.ExpectKnownValue(agentAddr, tfjsonpath.New("completion_args").AtMapKey("temperature"), knownvalue.Null()),
					statecheck.ExpectKnownValue(agentAddr, tfjsonpath.New("description"), knownvalue.Null()),
					statecheck.ExpectKnownValue(agentAddr, tfjsonpath.New("metadata"), knownvalue.Null()),
					statecheck.ExpectKnownValue(agentAddr, tfjsonpath.New("created_at"), knownvalue.StringExact(at(1))),
					statecheck.ExpectKnownValue(agentAddr, tfjsonpath.New("updated_at"), knownvalue.StringExact(at(1))),
				},
			},
			{
				Config:           agentConfig(fullAgent),
				ConfigPlanChecks: providertest.ExpectAction(agentAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(agentAddr, tfjsonpath.New("version"), knownvalue.Int64Exact(1)),
					statecheck.ExpectKnownValue(agentAddr, tfjsonpath.New("tools").AtSliceIndex(2).AtMapKey("function").AtMapKey("description"), knownvalue.StringExact("")),
					statecheck.ExpectKnownValue(agentAddr, tfjsonpath.New("tools").AtSliceIndex(2).AtMapKey("function").AtMapKey("parameters"),
						knownvalue.StringExact(`{"properties":{"order_id":{"type":"string"}},"type":"object"}`)),
					statecheck.ExpectKnownValue(agentAddr, tfjsonpath.New("tools").AtSliceIndex(0).AtMapKey("tool_configuration"), knownvalue.Null()),
					statecheck.ExpectKnownValue(agentAddr, tfjsonpath.New("completion_args").AtMapKey("response_format").AtMapKey("json_schema").AtMapKey("schema"),
						knownvalue.StringExact(`{ "type": "object",  "properties": { "text": { "type": "string" } } }`+"\n")),
					statecheck.ExpectKnownValue(agentAddr, tfjsonpath.New("metadata"), knownvalue.MapExact(map[string]knownvalue.Check{
						"team": knownvalue.StringExact("support"), "env": knownvalue.StringExact("prod"),
					})),
					statecheck.ExpectKnownValue(agentAddr, tfjsonpath.New("created_at"), knownvalue.StringExact(at(1))),
					statecheck.ExpectKnownValue(agentAddr, tfjsonpath.New("updated_at"), knownvalue.StringExact(at(2))),
				},
			},
			{
				Config:           agentConfig(trimmedAgent),
				ConfigPlanChecks: providertest.ExpectAction(agentAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(agentAddr, tfjsonpath.New("version"), knownvalue.Int64Exact(2)),
					statecheck.ExpectKnownValue(agentAddr, tfjsonpath.New("description"), knownvalue.Null()),
					statecheck.ExpectKnownValue(agentAddr, tfjsonpath.New("handoffs"), knownvalue.Null()),
					statecheck.ExpectKnownValue(agentAddr, tfjsonpath.New("tools"), knownvalue.ListSizeExact(2)),
					statecheck.ExpectKnownValue(agentAddr, tfjsonpath.New("completion_args").AtMapKey("response_format"), knownvalue.Null()),
					statecheck.ExpectKnownValue(agentAddr, tfjsonpath.New("completion_args").AtMapKey("temperature"), knownvalue.Float64Exact(0.5)),
					statecheck.ExpectKnownValue(agentAddr, tfjsonpath.New("metadata"), knownvalue.MapExact(map[string]knownvalue.Check{
						"team": knownvalue.StringExact("support"),
					})),
				},
			},
			{
				ResourceName:      agentAddr,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				ResourceName:    agentAddr,
				ImportState:     true,
				ImportStateKind: resource.ImportBlockWithResourceIdentity,
			},
		},
	})

	f.AssertJSONBodies(routeCreate, createBody)
	f.AssertJSONBodies(routeUpdate, fullBody, trimmedBody)
	f.AssertCredential("", sdktest.APIKey)
}

func TestAgentResourceRecreatesAnAgentDeletedOutsideTerraform(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	var id string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config: agentConfig(minimalAgent),
				Check: func(s *terraform.State) error {
					id = s.RootModule().Resources[agentAddr].Primary.ID
					return nil
				},
			},
			{
				PreConfig:        func() { f.agents.Delete(id) },
				Config:           agentConfig(minimalAgent),
				ConfigPlanChecks: providertest.ExpectAction(agentAddr, plancheck.ResourceActionCreate),
			},
		},
	})
	f.AssertCalled(routeCreate, 2)
	f.AssertCalled(routeDelete, 1)
}
