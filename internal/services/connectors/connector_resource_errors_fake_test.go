// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package connectors_test

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
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
				ExpectError: regexp.MustCompile(`Cannot\s+change\s+the\s+visibility(.|\n)*The\s+connector\s+is\s+created,\s+with\s+the\s+visibility\s+private(.|\n)*ShareConnectorToWorkspace`),
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

// expectBefore checks the value an attribute of the connector holds before
// a plan. With the refresh off, it is what the previous apply left in the
// state, failed or not.
type expectBefore struct {
	attribute, want string
}

// CheckPlan fails unless the attribute of the planned change of the
// connector held want before the plan.
func (e expectBefore) CheckPlan(_ context.Context, req plancheck.CheckPlanRequest, resp *plancheck.CheckPlanResponse) {
	for _, rc := range req.Plan.ResourceChanges {
		if rc.Address != connectorAddr {
			continue
		}
		before, _ := rc.Change.Before.(map[string]any)
		if got := before[e.attribute]; got != e.want {
			resp.Error = fmt.Errorf("%s.%s before the plan = %#v, want %q", connectorAddr, e.attribute, got, e.want)
		}
		return
	}
	resp.Error = fmt.Errorf("no planned change for %s", connectorAddr)
}

// noRefresh plans without a refresh, as a saved plan or -refresh=false
// does: the plan then starts from what the previous apply left in the state.
var noRefresh = &resource.AdditionalCLIOptions{Plan: resource.PlanOptions{NoRefresh: true}}

// expectUpdateFrom checks that the plan updates the connector from the
// visibility and the server the previous apply left in the state, and that
// the plan after the apply is empty.
func expectUpdateFrom(visibility, server string) resource.ConfigPlanChecks {
	checks := providertest.ExpectAction(connectorAddr, plancheck.ResourceActionUpdate)
	checks.PreApply = append(checks.PreApply, expectBefore{"visibility", visibility}, expectBefore{"server", server})
	return checks
}

// expectMoved checks that the state holds the new server with the
// visibility want.
func expectMoved(want string) []statecheck.StateCheck {
	return []statecheck.StateCheck{
		statecheck.ExpectKnownValue(connectorAddr, tfjsonpath.New("server"), knownvalue.StringExact(newServer)),
		statecheck.ExpectKnownValue(connectorAddr, tfjsonpath.New("visibility"), knownvalue.StringExact(want)),
	}
}

// expectConnector fails the test unless the fake holds the connector id
// with this server and visibility.
func (f *fake) expectConnector(t *testing.T, id, server, visibility string) {
	t.Helper()
	if got, _ := f.connectors.Get(id); got["server"] != server || got["visibility"] != visibility {
		t.Errorf("the connector is %v, want the server %s with the visibility %s", got, server, visibility)
	}
}

// keepID returns a check that stores the ID of the connector in id.
func keepID(id *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		*id = s.RootModule().Resources[connectorAddr].Primary.ID
		return nil
	}
}

// When the unshare of a narrowing is refused, no PATCH follows: the
// connector keeps its old server, which the members of the scope the plan
// removes could already use, and the state keeps its visibility. The next
// apply unshares again before the PATCH.
func TestConnectorResourceSendsNoContentWhenTheUnshareFails(t *testing.T) {
	t.Parallel()
	unshares := map[string]struct{ from, route string }{
		"from shared_org":       {sharedOrg, routeUnshareOrg},
		"from shared_workspace": {sharedWorkspace, routeUnshare},
	}
	for name, unshare := range unshares {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFake(t)
			var id string

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
				AdditionalCLIOptions:     noRefresh,
				Steps: []resource.TestStep{
					{Config: movedConnector(oldServer, unshare.from), Check: keepID(&id)},
					{
						PreConfig: func() { f.FailNextWith(unshare.route, 403, "Only the connector's creator can call this endpoint", 1) },
						Config:    movedConnector(newServer, private),
						ExpectError: regexp.MustCompile(`Cannot\s+change\s+the\s+visibility(.|\n)*No\s+other\s+argument\s+was\s+sent:\s+the\s+connector\s+keeps\s+its\s+content,\s+with\s+the\s+visibility\s+` +
							unshare.from + `(.|\n)*creator`),
					},
					{
						PreConfig:         func() { f.expectConnector(t, id, oldServer, unshare.from) },
						Config:            movedConnector(newServer, private),
						ConfigPlanChecks:  expectUpdateFrom(unshare.from, oldServer),
						ConfigStateChecks: expectMoved(private),
					},
				},
			})
			f.assertWrites(t, routeCreate, unshare.route, unshare.route, routeUpdate, routeDelete)
		})
	}
}

// A move from the organization to the workspace goes through private. When
// its share fails, no PATCH follows either, and the state records the
// connector as private with its old server: the next apply, a widening from
// private, sends the PATCH, then the share.
func TestConnectorResourceRecordsAHalfDoneNarrowing(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	var id string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		AdditionalCLIOptions:     noRefresh,
		Steps: []resource.TestStep{
			{Config: movedConnector(oldServer, sharedOrg), Check: keepID(&id)},
			{
				PreConfig: func() {
					f.FailNextWith(routeShare, 403, "Only the connector's creator can share it with the workspace", 1)
				},
				Config: movedConnector(newServer, sharedWorkspace),
				ExpectError: regexp.MustCompile(
					`Cannot\s+change\s+the\s+visibility(.|\n)*No\s+other\s+argument\s+was\s+sent:\s+the\s+connector\s+keeps\s+its\s+content,\s+with\s+the\s+visibility\s+private(.|\n)*creator`),
			},
			{
				PreConfig:         func() { f.expectConnector(t, id, oldServer, private) },
				Config:            movedConnector(newServer, sharedWorkspace),
				ConfigPlanChecks:  expectUpdateFrom(private, oldServer),
				ConfigStateChecks: expectMoved(sharedWorkspace),
			},
		},
	})
	f.assertWrites(t, routeCreate, routeUnshareOrg, routeShare, routeUpdate, routeShare, routeDelete)
}

// When the PATCH fails after a narrowing, the state records the new
// visibility with the old server, so that the next plan changes the server
// alone, and the error says so. A key that does not see private connectors
// answers 404 to the PATCH that follows a move to private: the error names
// the access scope it lacks.
func TestConnectorResourceRecordsANarrowingWhoseUpdateFailed(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	var id string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		AdditionalCLIOptions:     noRefresh,
		Steps: []resource.TestStep{
			{Config: movedConnector(oldServer, sharedWorkspace), Check: keepID(&id)},
			{
				PreConfig: func() { f.FailNextWith(routeUpdate, 404, "Connector not found", 1) },
				Config:    movedConnector(newServer, private),
				ExpectError: regexp.MustCompile(`Cannot\s+update\s+the\s+connector(.|\n)*The\s+visibility\s+of\s+the\s+connector\s+is\s+already\s+private,\s+but\s+its\s+other\s+arguments\s+were\s+not\s+changed` +
					`(.|\n)*404\s+Not\s+Found:\s+Connector\s+not\s+found(.|\n)*"Shared\s+connectors\s+only",\s+does\s+not\s+see\s+private\s+connectors`),
			},
			{
				PreConfig:         func() { f.expectConnector(t, id, oldServer, private) },
				Config:            movedConnector(newServer, private),
				ConfigPlanChecks:  expectUpdateFrom(private, oldServer),
				ConfigStateChecks: expectMoved(private),
			},
		},
	})
	f.assertWrites(t, routeCreate, routeUnshare, routeUpdate, routeUpdate, routeDelete)
}

// A widening sends the PATCH first. When its share fails, the error says the
// new content is live with the visibility the connector kept, private here
// since the move from the workspace to the organization unshares first, and
// the state records that visibility with the new server.
func TestConnectorResourceReportsTheContentAppliedBeforeAFailedShare(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	var id string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		AdditionalCLIOptions:     noRefresh,
		Steps: []resource.TestStep{
			{Config: movedConnector(oldServer, sharedWorkspace), Check: keepID(&id)},
			{
				PreConfig: func() {
					f.FailNextWith(routeShareOrg, 403, "Only organization admins can share a connector with the organization", 1)
				},
				Config: movedConnector(newServer, sharedOrg),
				ExpectError: regexp.MustCompile(
					`Cannot\s+change\s+the\s+visibility(.|\n)*The\s+other\s+arguments\s+are\s+already\s+updated,\s+with\s+the\s+visibility\s+private(.|\n)*organization\s+admins`),
			},
			{
				PreConfig:         func() { f.expectConnector(t, id, newServer, private) },
				Config:            movedConnector(newServer, sharedOrg),
				ConfigPlanChecks:  expectUpdateFrom(private, newServer),
				ConfigStateChecks: expectMoved(sharedOrg),
			},
		},
	})
	f.assertWrites(t, routeCreate, routeUpdate, routeUnshare, routeShareOrg, routeUpdate, routeShareOrg, routeDelete)
}

// A connector Mistral publishes answers shared_global, a visibility no share
// call leaves: it counts as the widest, so an update that changes its
// server fails at the move, before any call, and never sends the PATCH.
func TestConnectorResourceSendsNoContentToAConnectorSharedGlobally(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	id := f.seed(map[string]any{
		"name": "deepwiki", "description": "Explores code repositories", "server": oldServer, "visibility": "shared_global",
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: movedConnector(newServer, sharedOrg) + `
import {
  to = mistral_connector.this
  id = "` + id + `"
}
`,
			ExpectError: regexp.MustCompile(`Cannot\s+change\s+the\s+visibility(.|\n)*No\s+other\s+argument\s+was\s+sent(.|\n)*no\s+share\s+call\s+for\s+the\s+connector\s+visibility\s+"shared_global"`),
		}},
	})
	f.AssertCalled(routeUpdate, 0)
	f.assertWrites(t, routeDelete)
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
