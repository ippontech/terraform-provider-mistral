// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-mistral/internal/providertest"
	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

const (
	workspaceDataAddr  = "data.mistral_workspace.ml"
	workspacesDataAddr = "data.mistral_workspaces.all"
)

// workspaceDataConfig is a configuration that reads the workspace named name.
func workspaceDataConfig(name string) string {
	return providertest.ProviderConfig() + fmt.Sprintf("data \"mistral_workspace\" \"ml\" {\n  name = %q\n}\n", name)
}

var workspacesDataConfig = providertest.ProviderConfig() + "data \"mistral_workspaces\" \"all\" {}\n"

// The lookup keeps the exact name among the matches of the search, and reads
// every attribute.
func TestWorkspaceDataSourceReadsAWorkspaceByName(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.seed("ML Ops", false)
	id := f.workspaces.Create(map[string]any{"name": "ML", "description": "Research", "icon": "🧪", "is_default": false})["uuid"].(string)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: workspaceDataConfig("ML"),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(workspaceDataAddr, tfjsonpath.New("id"), knownvalue.StringExact(id)),
				statecheck.ExpectKnownValue(workspaceDataAddr, tfjsonpath.New("name"), knownvalue.StringExact("ML")),
				statecheck.ExpectKnownValue(workspaceDataAddr, tfjsonpath.New("description"), knownvalue.StringExact("Research")),
				statecheck.ExpectKnownValue(workspaceDataAddr, tfjsonpath.New("icon"), knownvalue.StringExact("🧪")),
				statecheck.ExpectKnownValue(workspaceDataAddr, tfjsonpath.New("is_default"), knownvalue.Bool(false)),
			},
		}},
	})
	assertFirstListQueries(t, f, "page=1&page_size=100&search=ML")
	f.AssertCredential("", sdktest.AdminKey)
}

// The search matches part of a name: an exact match after 120 partial ones
// sits on page 2.
func TestWorkspaceDataSourceWalksThePagesOfTheSearch(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.seedMany("ML", 120)
	id := f.seed("ML", true)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: workspaceDataConfig("ML"),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(workspaceDataAddr, tfjsonpath.New("id"), knownvalue.StringExact(id)),
				statecheck.ExpectKnownValue(workspaceDataAddr, tfjsonpath.New("is_default"), knownvalue.Bool(true)),
				statecheck.ExpectKnownValue(workspaceDataAddr, tfjsonpath.New("icon"), knownvalue.StringExact("")),
			},
		}},
	})
	assertFirstListQueries(t, f, "page=1&page_size=100&search=ML", "page=2&page_size=100&search=ML")
}

// Neither a partial match nor an archived workspace answers a name.
func TestWorkspaceDataSourceNotFound(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.seed("ML Ops", false)
	f.archive(f.seed("Research", false))

	for _, name := range []string{"ML", "Research"} {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
			Steps: []resource.TestStep{{
				Config:      workspaceDataConfig(name),
				ExpectError: regexp.MustCompile(`Workspace\s+not\s+found(.|\n)*No\s+active\s+workspace(.|\n)*named\s+"` + name + `"`),
			}},
		})
	}
}

// A list that keeps failing after the retries reaches the user.
func TestWorkspaceDataSourceReportsErrors(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.FailNext(routeListWorkspaces, 500, 4)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      workspaceDataConfig("ML"),
			ExpectError: regexp.MustCompile(`Cannot\s+read\s+the\s+workspace(.|\n)*500`),
		}},
	})
	f.AssertCalled(routeListWorkspaces, 4)
}

// The list holds every active workspace with its attributes, and leaves the
// archived ones out.
func TestWorkspacesDataSourceListsTheActiveWorkspaces(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	defaultID := f.seed("Default Workspace", true)
	f.archive(f.seed("Old", false))
	mlID := f.workspaces.Create(map[string]any{"name": "ML", "description": "Research", "icon": nil, "is_default": false})["uuid"].(string)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: workspacesDataConfig,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(workspacesDataAddr, tfjsonpath.New("workspaces"), knownvalue.ListExact([]knownvalue.Check{
					knownvalue.ObjectExact(map[string]knownvalue.Check{
						"id": knownvalue.StringExact(defaultID), "name": knownvalue.StringExact("Default Workspace"),
						"description": knownvalue.StringExact(""), "icon": knownvalue.StringExact(""), "is_default": knownvalue.Bool(true),
					}),
					knownvalue.ObjectExact(map[string]knownvalue.Check{
						"id": knownvalue.StringExact(mlID), "name": knownvalue.StringExact("ML"),
						"description": knownvalue.StringExact("Research"), "icon": knownvalue.StringExact(""), "is_default": knownvalue.Bool(false),
					}),
				})),
			},
		}},
	})
	assertFirstListQueries(t, f, "page=1&page_size=100")
	f.AssertCredential("", sdktest.AdminKey)
}

// An answer with no workspace is an empty list, not null.
func TestWorkspacesDataSourceEmptyList(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: workspacesDataConfig,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(workspacesDataAddr, tfjsonpath.New("workspaces"), knownvalue.ListExact([]knownvalue.Check{})),
			},
		}},
	})
}

// 250 workspaces take three pages of 100.
func TestWorkspacesDataSourceWalksEveryPage(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.seedMany("team", 250)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: workspacesDataConfig,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(workspacesDataAddr, tfjsonpath.New("workspaces"), knownvalue.ListSizeExact(250)),
				statecheck.ExpectKnownValue(workspacesDataAddr, tfjsonpath.New("workspaces").AtSliceIndex(249).AtMapKey("name"), knownvalue.StringExact("team-249")),
			},
		}},
	})
	assertFirstListQueries(t, f, "page=1&page_size=100", "page=2&page_size=100", "page=3&page_size=100")
}

// A list that keeps failing is retried, then reaches the user.
func TestWorkspacesDataSourceReportsErrorsAfterRetries(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.FailNext(routeListWorkspaces, 503, 4)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      workspacesDataConfig,
			ExpectError: regexp.MustCompile(`Cannot\s+list\s+the\s+workspaces(.|\n)*503`),
		}},
	})
	f.AssertCalled(routeListWorkspaces, 4)
}

// assertFirstListQueries checks the queries of the workspace lists of the
// first read of a data source: Terraform reads it again at each plan of a
// step.
func assertFirstListQueries(t *testing.T, f *fake, want ...string) {
	t.Helper()
	got := f.Requests(routeListWorkspaces)
	if len(got) < len(want) {
		t.Fatalf("the workspaces were listed %d times, want at least %d", len(got), len(want))
	}
	for i, w := range want {
		if q := got[i].Query.Encode(); q != w {
			t.Errorf("list %d has query %q, want %q", i, q, w)
		}
	}
	if len(got) > len(want) && got[len(want)].Query.Get("page") != "1" {
		t.Errorf("the first read did not end after %d pages: request %d has query %q", len(want), len(want), got[len(want)].Query.Encode())
	}
}
