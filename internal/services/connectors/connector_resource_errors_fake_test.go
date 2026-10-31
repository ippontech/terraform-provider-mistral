// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package connectors_test

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-mistral/internal/providertest"
	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

// A create that meets a 5xx is sent once: the connector may exist, and a
// replay would create a second one.
func TestConnectorResourceCreateIsSentOnceOnA5xx(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.FailNext(routeCreate, 500, 1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      connectorConfig(minimalConnector),
			ExpectError: regexp.MustCompile(`Cannot\s+create\s+the\s+connector(.|\n)*500`),
		}},
	})
	f.AssertCalled(routeCreate, 1)
}

// An update is not documented as idempotent: it is not replayed after a
// 5xx.
func TestConnectorResourceUpdateIsSentOnceOnA5xx(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: connectorConfig(minimalConnector)},
			{
				PreConfig:   func() { f.FailNext(routeUpdate, 503, 1) },
				Config:      connectorConfig(minimalConnector + `  title = "DeepWiki"`),
				ExpectError: regexp.MustCompile(`Cannot\s+update\s+the\s+connector(.|\n)*503`),
			},
		},
	})
	f.AssertCalled(routeUpdate, 1)
}

// The create body carries the visibility. Should the API answer another
// one, the share calls apply the planned visibility.
func TestConnectorResourceSharesAConnectorTheCreateLeftPrivate(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.createAnswersPrivate = true

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:           connectorConfig(minimalConnector),
			ConfigPlanChecks: providertest.ExpectAction(connectorAddr, plancheck.ResourceActionCreate),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(connectorAddr, tfjsonpath.New("visibility"), knownvalue.StringExact("shared_workspace")),
			},
		}},
	})
	f.assertWrites(t, routeCreate, routeShare, routeDelete)
}

// When a share call fails after the create, the connector stays in the
// state, tainted: the next apply replaces it.
func TestConnectorResourceKeepsAConnectorWhoseShareFailed(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.createAnswersPrivate = true
	f.FailNextWith(routeShare, 403, "Missing the ShareConnectorToWorkspace permission", 1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config:      connectorConfig(minimalConnector),
				ExpectError: regexp.MustCompile(`Cannot\s+change\s+the\s+visibility(.|\n)*ShareConnectorToWorkspace`),
			},
			{
				Config:           connectorConfig(minimalConnector),
				ConfigPlanChecks: providertest.ExpectAction(connectorAddr, plancheck.ResourceActionDestroyBeforeCreate),
			},
		},
	})
	f.AssertCalled(routeCreate, 2)
	f.AssertCalled(routeDelete, 2)
}

// The share calls answer a message only, so the connector is read back; a
// failure of that read is reported, with the connector kept in the state.
func TestConnectorResourceReportsTheReadAfterAShare(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.createAnswersPrivate = true
	f.FailNext(routeGet, 500, 4)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      connectorConfig(minimalConnector),
			ExpectError: regexp.MustCompile(`Cannot\s+read\s+the\s+connector(.|\n)*500`),
		}},
	})
	f.AssertCalled(routeShare, 1)
	f.AssertCalled(routeDelete, 1)
}

// When the unshare fails, the state keeps what the PATCH answered, with the
// visibility unchanged: the next apply tries the share calls again.
func TestConnectorResourceReportsAFailedUnshare(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	private := strings.Replace(minimalConnector, `"shared_workspace"`, `"private"`, 1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: connectorConfig(minimalConnector)},
			{
				PreConfig:   func() { f.FailNextWith(routeUnshare, 403, "Only the connector's creator can call this endpoint", 1) },
				Config:      connectorConfig(private),
				ExpectError: regexp.MustCompile(`Cannot\s+change\s+the\s+visibility(.|\n)*creator`),
			},
			{
				Config:           connectorConfig(private),
				ConfigPlanChecks: providertest.ExpectAction(connectorAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(connectorAddr, tfjsonpath.New("visibility"), knownvalue.StringExact("private")),
				},
			},
		},
	})
	f.assertWrites(t, routeCreate, routeUpdate, routeUnshare, routeUpdate, routeUnshare, routeDelete)
}

// A failing refresh and a failing delete are reported with the message of
// the API.
func TestConnectorResourceReportsReadAndDeleteErrors(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: connectorConfig(minimalConnector)},
			{
				PreConfig:   func() { f.FailNextWith(routeGet, 401, "Invalid API Key", 1) },
				Config:      connectorConfig(minimalConnector),
				ExpectError: regexp.MustCompile(`Cannot\s+read\s+the\s+connector(.|\n)*Invalid\s+API\s+Key`),
			},
			{
				PreConfig:   func() { f.FailNext(routeDelete, 500, 4) },
				Config:      connectorConfig(minimalConnector),
				Destroy:     true,
				ExpectError: regexp.MustCompile(`Cannot\s+delete\s+the\s+connector(.|\n)*500`),
			},
		},
	})
}

// Invalid arguments fail the plan, before any call.
func TestConnectorResourceValidatesItsArguments(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	required := `
  description = "d"
  server      = "https://a.example"
`
	cases := map[string]struct{ body, err string }{
		"name with a space":  {`name = "deep wiki"` + required + `visibility = "private"`, `1\s+to\s+64\s+letters`},
		"name too long":      {`name = "` + strings.Repeat("a", 65) + `"` + required + `visibility = "private"`, `1\s+to\s+64\s+letters`},
		"unknown visibility": {`name = "a"` + required + `visibility = "public"`, `value\s+must\s+be\s+one\s+of`},
		"shared_global":      {`name = "a"` + required + `visibility = "shared_global"`, `value\s+must\s+be\s+one\s+of`},
		"unknown protocol":   {`name = "a"` + required + `visibility = "private"` + "\n" + `protocol = "sse"`, `value\s+must\s+be\s+one\s+of`},
		"empty title":        {`name = "a"` + required + `visibility = "private"` + "\n" + `title = ""`, `length\s+must\s+be\s+between\s+1\s+and\s+255`},
		"no visibility":      {`name = "a"` + required, `The\s+argument\s+"visibility"\s+is\s+required`},
	}
	// The parallel subtests run once this function returns: the cleanup
	// checks the fake after all of them.
	t.Cleanup(func() { f.AssertCalled("", 0) })
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
				Steps: []resource.TestStep{{
					Config:      connectorConfig(c.body),
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(c.err),
				}},
			})
		})
	}
}

// The share calls answer a message, so the connector is read back by ID;
// another connector answering that read, one named with the ID, is reported,
// and the state keeps the connector the create answered, which the destroy
// then deletes.
func TestConnectorResourceReportsAnotherConnectorReadAfterAShare(t *testing.T) {
	t.Parallel()
	const id = "00000000-0000-4000-8000-000000000002"
	srv := sdktest.NewServer(t)
	srv.Respond(routeCreate, http.StatusCreated, map[string]any{
		"id": id, "name": "deepwiki", "description": "Explores code repositories", "server": "https://mcp.deepwiki.com/mcp",
		"protocol": "mcp", "visibility": private,
	})
	srv.Respond(routeShare, http.StatusOK, map[string]any{"message": "Connector shared"})
	srv.Respond(routeGet, http.StatusOK, map[string]any{"id": "00000000-0000-4000-8000-000000000003", "name": id, "visibility": sharedWorkspace})
	srv.Respond(routeDelete, http.StatusOK, map[string]any{"message": "Connector deleted"})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv),
		Steps: []resource.TestStep{{
			Config:      connectorConfig(minimalConnector),
			ExpectError: regexp.MustCompile(`Cannot\s+read\s+the\s+connector(.|\n)*no\s+connector\s+has\s+the\s+ID\s+"` + id + `"\s+any\s+more`),
		}},
	})
	srv.AssertCalled(routeShare, 1)
	if deletes := srv.Requests(routeDelete); len(deletes) != 1 || deletes[0].Path != "/v1/connectors/"+id {
		t.Errorf("deletes = %v, want one of %s", deletes, id)
	}
}

// Read reads a connector by ID only, so an import by name is refused with a
// pointer to the data source, rather than reported as a missing object.
func TestConnectorResourceRefusesAnImportByName(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: connectorConfig(minimalConnector)},
			{
				ResourceName:  connectorAddr,
				ImportState:   true,
				ImportStateId: "deepwiki",
				ExpectError:   regexp.MustCompile(`Invalid\s+import\s+ID(.|\n)*lowercase\s+UUID;\s+for\s+a\s+connector\s+known\s+by\s+its\s+name,\s+use\s+data\.mistral_connector\.<name>\.id,\s+got\s+"deepwiki"`),
			},
		},
	})
}
