// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package connectors_test

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

const connectorDataAddr = "data.mistral_connector.github"

// connectorDataConfig looks up the connector name.
func connectorDataConfig(name string) string {
	return providertest.ProviderConfig() + `
data "mistral_connector" "github" {
  name = "` + name + `"
}
`
}

// The data source reads the connector of the name it asks for, by a GET on
// the name, among others.
func TestConnectorDataSourceReadsAConnectorByName(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.seed(map[string]any{"name": "gitlab", "description": "Another connector", "server": "https://gitlab.example/mcp", "visibility": "private"})
	id := f.seed(map[string]any{
		"name": "github", "title": "GitHub", "description": "Issues and pull requests",
		"server": "https://api.githubcopilot.com/mcp/", "visibility": "shared_org", "protocol": "http",
	})
	value := func(attr string, want knownvalue.Check) statecheck.StateCheck {
		return statecheck.ExpectKnownValue(connectorDataAddr, tfjsonpath.New(attr), want)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: connectorDataConfig("github"),
			ConfigStateChecks: []statecheck.StateCheck{
				value("id", knownvalue.StringExact(id)),
				value("name", knownvalue.StringExact("github")),
				value("title", knownvalue.StringExact("GitHub")),
				value("description", knownvalue.StringExact("Issues and pull requests")),
				value("server", knownvalue.StringExact("https://api.githubcopilot.com/mcp/")),
				value("protocol", knownvalue.StringExact("http")),
				value("visibility", knownvalue.StringExact("shared_org")),
				value("created_at", knownvalue.StringExact(at(2))),
				value("modified_at", knownvalue.StringExact(at(2))),
			},
		}},
	})
	if p := f.Requests(routeGet)[0].Path; p != "/v1/connectors/github" {
		t.Errorf("path = %s, want the name", p)
	}
	f.AssertCredential(routeGet, sdktest.APIKey)
}

// A connector Mistral publishes to everyone has no server of its own and
// reads with its null fields as null.
func TestConnectorDataSourceReadsAConnectorMistralPublishes(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.seed(map[string]any{"name": "gmail", "description": "Gmail", "server": nil, "title": nil, "visibility": "shared_global", "protocol": ""})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: connectorDataConfig("gmail"),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(connectorDataAddr, tfjsonpath.New("visibility"), knownvalue.StringExact("shared_global")),
				statecheck.ExpectKnownValue(connectorDataAddr, tfjsonpath.New("server"), knownvalue.Null()),
				statecheck.ExpectKnownValue(connectorDataAddr, tfjsonpath.New("title"), knownvalue.Null()),
				statecheck.ExpectKnownValue(connectorDataAddr, tfjsonpath.New("protocol"), knownvalue.StringExact("mcp")),
			},
		}},
	})
}

// A name no connector has fails with the not-found error.
func TestConnectorDataSourceNotFound(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      connectorDataConfig("missing"),
			ExpectError: regexp.MustCompile(`Connector\s+not\s+found(.|\n)*No\s+connector\s+named\s+"missing"`),
		}},
	})
}

// The API also answers a connector ID; the lookup is by name, so an ID
// finds nothing.
func TestConnectorDataSourceDoesNotLookUpByID(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	id := f.seed(map[string]any{"name": "github", "description": "d", "server": "https://a.example", "visibility": "private"})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      connectorDataConfig(id),
			ExpectError: regexp.MustCompile(`Connector\s+not\s+found`),
		}},
	})
}

// Another error of the API is reported once the retries of the read are
// spent.
func TestConnectorDataSourceReportsOtherErrors(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.FailNext(routeGet, 500, 4)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      connectorDataConfig("github"),
			ExpectError: regexp.MustCompile(`Cannot\s+read\s+the\s+connector(.|\n)*500`),
		}},
	})
	f.AssertCalled(routeGet, 4)
}
