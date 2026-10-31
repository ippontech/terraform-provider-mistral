// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package connectors_test

import (
	"fmt"
	"net/http"
	"regexp"
	"slices"
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

const connectorAddr = "mistral_connector.this"

// uuid matches the IDs of sdktest.NewUUID, the shape of a connector ID.
var uuid = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-8[0-9a-f]{3}-[0-9a-f]{12}$`)

// connectorConfig is a configuration with one mistral_connector whose
// arguments are body.
func connectorConfig(body string) string {
	return providertest.ProviderConfig() + "resource \"mistral_connector\" \"this\" {\n" + body + "\n}\n"
}

// minimalConnector sets the required arguments only.
const minimalConnector = `
  name        = "deepwiki"
  description = "Explores code repositories"
  server      = "https://mcp.deepwiki.com/mcp"
  visibility  = "shared_workspace"
`

// fullConnector renames the connector, sets every optional argument, and
// shares it with the organization.
const fullConnector = `
  name          = "deepwiki_docs"
  title         = "DeepWiki"
  description   = "Explores code repositories and their docs"
  server        = "https://mcp.deepwiki.com/sse"
  visibility    = "shared_org"
  icon_url      = "https://deepwiki.com/icon.png"
  system_prompt = "Cite the repository of every answer."
`

// trimmedConnector empties the optional arguments and makes the connector
// private.
const trimmedConnector = `
  name        = "deepwiki_docs"
  description = "Explores code repositories and their docs"
  server      = "https://mcp.deepwiki.com/sse"
  visibility  = "private"
`

// Exact bodies of the lifecycle: the create carries the visibility, the
// updates every field of the MCP update schema, null included, and never
// the visibility.
const (
	createBody = `{"protocol": "mcp", "name": "deepwiki", "title": null, "description": "Explores code repositories",
		"server": "https://mcp.deepwiki.com/mcp", "icon_url": null, "system_prompt": null, "visibility": "shared_workspace"}`
	fullBody = `{"protocol": "mcp", "name": "deepwiki_docs", "title": "DeepWiki", "description": "Explores code repositories and their docs",
		"server": "https://mcp.deepwiki.com/sse", "icon_url": "https://deepwiki.com/icon.png", "system_prompt": "Cite the repository of every answer."}`
	trimmedBody = `{"protocol": "mcp", "name": "deepwiki_docs", "title": null, "description": "Explores code repositories and their docs",
		"server": "https://mcp.deepwiki.com/sse", "icon_url": null, "system_prompt": null}`
	minimalBody = `{"protocol": "mcp", "name": "deepwiki", "title": null, "description": "Explores code repositories",
		"server": "https://mcp.deepwiki.com/mcp", "icon_url": null, "system_prompt": null}`
)

// writes returns the route of every request but the reads, in order.
func (f *fake) writes() []string {
	var out []string
	for _, r := range f.Requests("") {
		if r.Method != http.MethodGet {
			out = append(out, r.Route)
		}
	}
	return out
}

// assertWrites fails the test unless the fake received exactly these
// writes, in this order.
func (f *fake) assertWrites(t *testing.T, want ...string) {
	t.Helper()
	if got := f.writes(); !slices.Equal(got, want) {
		t.Errorf("writes:\n  got  %q\n  want %q", got, want)
	}
}

// Create, a full update that shares the connector with the organization, a
// trimmed update that makes it private, a return to the workspace, then both
// imports.
func TestConnectorResourceLifecycle(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	value := func(attr string, want knownvalue.Check) statecheck.StateCheck {
		return statecheck.ExpectKnownValue(connectorAddr, tfjsonpath.New(attr), want)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config:           connectorConfig(minimalConnector),
				ConfigPlanChecks: providertest.ExpectAction(connectorAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					value("id", knownvalue.StringRegexp(uuid)),
					value("name", knownvalue.StringExact("deepwiki")),
					value("protocol", knownvalue.StringExact("mcp")),
					value("visibility", knownvalue.StringExact("shared_workspace")),
					value("title", knownvalue.Null()),
					value("icon_url", knownvalue.Null()),
					value("system_prompt", knownvalue.Null()),
					value("created_at", knownvalue.StringExact(at(1))),
					value("modified_at", knownvalue.StringExact(at(1))),
				},
			},
			{
				Config:           connectorConfig(fullConnector),
				ConfigPlanChecks: providertest.ExpectAction(connectorAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					value("name", knownvalue.StringExact("deepwiki_docs")),
					value("title", knownvalue.StringExact("DeepWiki")),
					value("server", knownvalue.StringExact("https://mcp.deepwiki.com/sse")),
					value("visibility", knownvalue.StringExact("shared_org")),
					value("icon_url", knownvalue.StringExact("https://deepwiki.com/icon.png")),
					value("system_prompt", knownvalue.StringExact("Cite the repository of every answer.")),
					value("created_at", knownvalue.StringExact(at(1))),
					value("modified_at", knownvalue.StringExact(at(2))),
				},
			},
			{
				Config:           connectorConfig(trimmedConnector),
				ConfigPlanChecks: providertest.ExpectAction(connectorAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					value("visibility", knownvalue.StringExact("private")),
					value("title", knownvalue.Null()),
					value("icon_url", knownvalue.Null()),
					value("system_prompt", knownvalue.Null()),
					value("modified_at", knownvalue.StringExact(at(3))),
				},
			},
			{
				Config:           connectorConfig(minimalConnector),
				ConfigPlanChecks: providertest.ExpectAction(connectorAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					value("name", knownvalue.StringExact("deepwiki")),
					value("visibility", knownvalue.StringExact("shared_workspace")),
				},
			},
			{
				ResourceName:      connectorAddr,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				ResourceName:    connectorAddr,
				ImportState:     true,
				ImportStateKind: resource.ImportBlockWithResourceIdentity,
			},
		},
	})

	f.AssertJSONBodies(routeCreate, createBody)
	f.AssertJSONBodies(routeUpdate, fullBody, trimmedBody, minimalBody)
	// A shared connector goes back to private before it is shared again.
	f.assertWrites(t,
		routeCreate,
		routeUpdate, routeUnshare, routeShareOrg,
		routeUpdate, routeUnshareOrg,
		routeUpdate, routeShare,
		routeDelete)
	f.AssertCredential("", sdktest.APIKey)
	for _, r := range f.Requests(routeGet) {
		if len(r.Query) != 0 {
			t.Errorf("GET %s sent the query %q", r.Path, r.Query.Encode())
		}
	}
}

// A connector deleted outside Terraform plans a create.
func TestConnectorResourceRecreatesAConnectorDeletedOutsideTerraform(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	var id string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config: connectorConfig(minimalConnector),
				Check: func(s *terraform.State) error {
					id = s.RootModule().Resources[connectorAddr].Primary.ID
					return nil
				},
			},
			{
				PreConfig:        func() { f.connectors.Delete(id) },
				Config:           connectorConfig(minimalConnector),
				ConfigPlanChecks: providertest.ExpectAction(connectorAddr, plancheck.ResourceActionCreate),
			},
		},
	})
	f.AssertCalled(routeCreate, 2)
	f.AssertCalled(routeDelete, 1)
}

// A connector changed in AI Studio, unshared there too, shows as drift: the
// apply writes the configuration back and shares it again.
func TestConnectorResourceRevertsAChangeMadeOutsideTerraform(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	var id string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config: connectorConfig(minimalConnector),
				Check: func(s *terraform.State) error {
					id = s.RootModule().Resources[connectorAddr].Primary.ID
					return nil
				},
			},
			{
				PreConfig: func() {
					f.connectors.Patch(id, map[string]any{"description": "Changed in AI Studio", "title": "Studio", "visibility": "private"})
				},
				Config:           connectorConfig(minimalConnector),
				ConfigPlanChecks: providertest.ExpectAction(connectorAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(connectorAddr, tfjsonpath.New("description"), knownvalue.StringExact("Explores code repositories")),
					statecheck.ExpectKnownValue(connectorAddr, tfjsonpath.New("title"), knownvalue.Null()),
					statecheck.ExpectKnownValue(connectorAddr, tfjsonpath.New("visibility"), knownvalue.StringExact("shared_workspace")),
				},
			},
		},
	})
	f.assertWrites(t, routeCreate, routeUpdate, routeShare, routeDelete)
}

// A connector built in AI Studio imports with an import block and plans no
// change when the configuration matches it.
func TestConnectorResourceImportsAConnectorBuiltInStudio(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	id := f.seed(map[string]any{
		"name": "github", "title": "GitHub", "description": "Issues and pull requests", "server": "https://api.githubcopilot.com/mcp/",
		"visibility": "shared_workspace", "protocol": "http", "icon_url": nil, "system_prompt": "",
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig() + `
import {
  to = mistral_connector.this
  id = "` + id + `"
}

resource "mistral_connector" "this" {
  name        = "github"
  title       = "GitHub"
  description = "Issues and pull requests"
  server      = "https://api.githubcopilot.com/mcp/"
  protocol    = "http"
  visibility  = "shared_workspace"
}
`,
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(connectorAddr, plancheck.ResourceActionNoop)},
			},
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(connectorAddr, tfjsonpath.New("system_prompt"), knownvalue.Null()),
				statecheck.ExpectKnownValue(connectorAddr, tfjsonpath.New("created_at"), knownvalue.StringExact(at(1))),
			},
		}},
	})
	f.AssertCalled(routeUpdate, 0)
}

// The protocol cannot change in place, nor the system prompt of an HTTP
// connector, whose update schema has no system_prompt; an MCP connector
// updates its system prompt in place.
func TestConnectorResourceReplacesWhatTheUpdateCannotChange(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	httpConnector := func(prompt, description string) string {
		return connectorConfig(`
  name          = "orders"
  description   = "` + description + `"
  server        = "https://orders.example.com/api"
  protocol      = "http"
  visibility    = "private"
  system_prompt = "` + prompt + `"
`)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: httpConnector("Answer briefly.", "Orders")},
			{
				Config:           httpConnector("Answer briefly.", "Orders and returns"),
				ConfigPlanChecks: providertest.ExpectAction(connectorAddr, plancheck.ResourceActionUpdate),
			},
			{
				Config:           httpConnector("Answer in detail.", "Orders and returns"),
				ConfigPlanChecks: providertest.ExpectAction(connectorAddr, plancheck.ResourceActionDestroyBeforeCreate),
			},
			{
				Config: connectorConfig(`
  name          = "orders"
  description   = "Orders and returns"
  server        = "https://orders.example.com/api"
  visibility    = "private"
  system_prompt = "Answer in detail."
`),
				ConfigPlanChecks: providertest.ExpectAction(connectorAddr, plancheck.ResourceActionDestroyBeforeCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(connectorAddr, tfjsonpath.New("protocol"), knownvalue.StringExact("mcp")),
				},
			},
			{
				Config: connectorConfig(`
  name          = "orders"
  description   = "Orders and returns"
  server        = "https://orders.example.com/api"
  visibility    = "private"
  system_prompt = "Answer briefly."
`),
				ConfigPlanChecks: providertest.ExpectAction(connectorAddr, plancheck.ResourceActionUpdate),
			},
		},
	})
	f.AssertJSONBodies(routeUpdate,
		`{"protocol": "http", "name": "orders", "title": null, "description": "Orders and returns",
			"server": "https://orders.example.com/api", "icon_url": null}`,
		`{"protocol": "mcp", "name": "orders", "title": null, "description": "Orders and returns",
			"server": "https://orders.example.com/api", "icon_url": null, "system_prompt": "Answer briefly."}`)
	f.AssertCalled(routeCreate, 3)
}

// Once the connector is deleted, a connector named with its ID answers the
// GET of that ID: the refresh takes the connector for gone and plans a
// create, instead of writing the other connector into the state and updating
// it.
func TestConnectorResourceTakesAConnectorNamedWithItsIDForGone(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	var id, squatter string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config: connectorConfig(minimalConnector),
				Check: func(s *terraform.State) error {
					id = s.RootModule().Resources[connectorAddr].Primary.ID
					return nil
				},
			},
			{
				PreConfig:        func() { squatter = f.squat(id) },
				Config:           connectorConfig(minimalConnector),
				ConfigPlanChecks: providertest.ExpectAction(connectorAddr, plancheck.ResourceActionCreate),
				Check: func(s *terraform.State) error {
					if got := s.RootModule().Resources[connectorAddr].Primary.ID; got == id || got == squatter {
						return fmt.Errorf("the state holds the connector %s, want a new one", got)
					}
					return nil
				},
			},
		},
	})
	f.AssertCalled(routeUpdate, 0)
	if c, ok := f.connectors.Get(squatter); !ok || c["name"] != id {
		t.Errorf("the other connector is %v, want it unchanged", c)
	}
}
