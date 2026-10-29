// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces_test

import (
	"fmt"
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

const workspaceAddr = "mistral_workspace.this"

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// workspaceConfig is a configuration with one mistral_workspace whose
// arguments are body.
func workspaceConfig(body string) string {
	return providertest.ProviderConfig() + "resource \"mistral_workspace\" \"this\" {\n" + body + "\n}\n"
}

// minimalWorkspace sets the required arguments only.
func minimalWorkspace(adminID string) string {
	return fmt.Sprintf("  name          = \"ML\"\n  admin_user_id = %q\n", adminID)
}

// readWorkspaceID reads the ID of mistral_workspace.this into id.
func readWorkspaceID(id *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		*id = s.RootModule().Resources[workspaceAddr].Primary.ID
		return nil
	}
}

// TestWorkspaceResourceLifecycle creates a workspace, sets then empties its
// description and icon, imports it by ID and by identity, and archives it on
// destroy.
func TestWorkspaceResourceLifecycle(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	admin := f.addUser("admin@example.com")
	full := fmt.Sprintf(`
  name          = "ML Research"
  description   = "Models and experiments"
  icon          = "🧪"
  admin_user_id = %q
`, admin)
	var id string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config:           workspaceConfig(minimalWorkspace(admin)),
				ConfigPlanChecks: providertest.ExpectAction(workspaceAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(workspaceAddr, tfjsonpath.New("id"), knownvalue.StringRegexp(uuidPattern)),
					statecheck.ExpectKnownValue(workspaceAddr, tfjsonpath.New("name"), knownvalue.StringExact("ML")),
					statecheck.ExpectKnownValue(workspaceAddr, tfjsonpath.New("description"), knownvalue.StringExact("")),
					statecheck.ExpectKnownValue(workspaceAddr, tfjsonpath.New("icon"), knownvalue.StringExact("")),
					statecheck.ExpectKnownValue(workspaceAddr, tfjsonpath.New("admin_user_id"), knownvalue.StringExact(admin)),
					statecheck.ExpectKnownValue(workspaceAddr, tfjsonpath.New("add_all_org_members"), knownvalue.Bool(false)),
					statecheck.ExpectKnownValue(workspaceAddr, tfjsonpath.New("is_default"), knownvalue.Bool(false)),
				},
				Check: readWorkspaceID(&id),
			},
			{
				Config:           workspaceConfig(full),
				ConfigPlanChecks: providertest.ExpectAction(workspaceAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(workspaceAddr, tfjsonpath.New("name"), knownvalue.StringExact("ML Research")),
					statecheck.ExpectKnownValue(workspaceAddr, tfjsonpath.New("description"), knownvalue.StringExact("Models and experiments")),
					statecheck.ExpectKnownValue(workspaceAddr, tfjsonpath.New("icon"), knownvalue.StringExact("🧪")),
				},
			},
			{
				// Removing description and icon sends their default, "".
				Config:           workspaceConfig(fmt.Sprintf("  name          = \"ML Research\"\n  admin_user_id = %q\n", admin)),
				ConfigPlanChecks: providertest.ExpectAction(workspaceAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(workspaceAddr, tfjsonpath.New("description"), knownvalue.StringExact("")),
					statecheck.ExpectKnownValue(workspaceAddr, tfjsonpath.New("icon"), knownvalue.StringExact("")),
				},
			},
			{
				// The API never returns admin_user_id: an import leaves it null.
				ResourceName:            workspaceAddr,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"admin_user_id"},
			},
			{
				// The import block plans an update that records
				// admin_user_id, and calls nothing.
				ResourceName:       workspaceAddr,
				ImportState:        true,
				ImportStateKind:    resource.ImportBlockWithResourceIdentity,
				ExpectNonEmptyPlan: true,
				ImportPlanChecks: resource.ImportPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(workspaceAddr, plancheck.ResourceActionUpdate),
					plancheck.ExpectKnownValue(workspaceAddr, tfjsonpath.New("admin_user_id"), knownvalue.StringExact(admin)),
					plancheck.ExpectKnownValue(workspaceAddr, tfjsonpath.New("description"), knownvalue.StringExact("")),
				}},
			},
		},
	})

	f.AssertJSONBodies(routeCreateWorkspace,
		fmt.Sprintf(`{"name": "ML", "description": "", "icon": "", "add_all_org_members": false, "admin_user_id": %q}`, admin))
	f.AssertJSONBodies(routeUpdateWorkspace,
		`{"name": "ML Research", "description": "Models and experiments", "icon": "🧪"}`,
		`{"name": "ML Research", "description": "", "icon": ""}`)
	if got := f.Requests(routeUpdateWorkspace)[0].Path; got != "/v1/admin/workspaces/"+id {
		t.Errorf("PATCH path = %s", got)
	}
	// The first read searches by name; the import, which has no name, lists
	// every workspace.
	if got := f.Requests(routeListWorkspaces)[0].Query.Encode(); got != "page=1&page_size=100&search=ML" {
		t.Errorf("first list query = %q", got)
	}
	unfiltered := 0
	for _, r := range f.Requests(routeListWorkspaces) {
		if r.Query.Encode() == "page=1&page_size=100" {
			unfiltered++
		}
	}
	if unfiltered == 0 {
		t.Error("the import never listed every workspace")
	}
	f.AssertCalled(routeArchiveWorkspace, 1)
	if ws, _ := f.workspaces.Get(id); ws["archived"] != true {
		t.Errorf("destroy must archive the workspace: %v", ws)
	}
	f.AssertCredential("", sdktest.AdminKey)
}

// The arguments POST takes alone change in place, with no call: a
// replacement would archive the workspace.
func TestWorkspaceResourceRecordsCreateOnlyArguments(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	admin, other := f.addUser("admin@example.com"), f.addUser("ada@example.com")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: workspaceConfig(minimalWorkspace(admin))},
			{
				Config:           workspaceConfig(minimalWorkspace(other) + "  add_all_org_members = true\n"),
				ConfigPlanChecks: providertest.ExpectAction(workspaceAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(workspaceAddr, tfjsonpath.New("admin_user_id"), knownvalue.StringExact(other)),
					statecheck.ExpectKnownValue(workspaceAddr, tfjsonpath.New("add_all_org_members"), knownvalue.Bool(true)),
				},
			},
		},
	})
	f.AssertCalled(routeCreateWorkspace, 1)
	f.AssertCalled(routeUpdateWorkspace, 0)
	f.AssertCalled(routeArchiveWorkspace, 1)
}

// add_all_org_members makes every member of the organization a member of
// the new workspace.
func TestWorkspaceResourceAddsAllMembers(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	admin, ada := f.addUser("admin@example.com"), f.addUser("ada@example.com")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: workspaceConfig(minimalWorkspace(admin) + "  add_all_org_members = true\n"),
			Check: func(s *terraform.State) error {
				id := s.RootModule().Resources[workspaceAddr].Primary.ID
				if _, ok := f.members.Get(id + ":" + ada); !ok {
					return fmt.Errorf("%s is not a member of %s", ada, id)
				}
				return nil
			},
		}},
	})
	f.AssertJSONBodies(routeCreateWorkspace,
		fmt.Sprintf(`{"name": "ML", "description": "", "icon": "", "add_all_org_members": true, "admin_user_id": %q}`, admin))
}

// An import block brings a workspace created in the Admin Panel under
// Terraform. The first apply records admin_user_id, which the API never
// returns, without calling it.
func TestWorkspaceResourceImportsAWorkspaceCreatedOutsideTerraform(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	admin := f.addUser("admin@example.com")
	id := f.seed("ML", false)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:           workspaceConfig(minimalWorkspace(admin)) + "import {\n  to = mistral_workspace.this\n  id = \"" + id + "\"\n}\n",
			ConfigPlanChecks: providertest.ExpectAction(workspaceAddr, plancheck.ResourceActionUpdate),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(workspaceAddr, tfjsonpath.New("id"), knownvalue.StringExact(id)),
				statecheck.ExpectKnownValue(workspaceAddr, tfjsonpath.New("admin_user_id"), knownvalue.StringExact(admin)),
				statecheck.ExpectKnownValue(workspaceAddr, tfjsonpath.New("add_all_org_members"), knownvalue.Bool(false)),
				statecheck.ExpectKnownValue(workspaceAddr, tfjsonpath.New("icon"), knownvalue.StringExact("")),
			},
		}},
	})
	f.AssertCalled(routeCreateWorkspace, 0)
	f.AssertCalled(routeUpdateWorkspace, 0)
}

// A workspace gone from every list plans a create.
func TestWorkspaceResourceRecreatesAWorkspaceDeletedOutsideTerraform(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	admin := f.addUser("admin@example.com")
	var id string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: workspaceConfig(minimalWorkspace(admin)), Check: readWorkspaceID(&id)},
			{
				PreConfig:        func() { f.workspaces.Delete(id) },
				Config:           workspaceConfig(minimalWorkspace(admin)),
				ConfigPlanChecks: providertest.ExpectAction(workspaceAddr, plancheck.ResourceActionCreate),
			},
		},
	})
	f.AssertCalled(routeCreateWorkspace, 2)
}

// A workspace archived outside Terraform leaves the state, with a warning,
// although the list still returns it with is_archived=true.
func TestWorkspaceResourceForgetsAWorkspaceArchivedOutsideTerraform(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	admin := f.addUser("admin@example.com")
	var id string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: workspaceConfig(minimalWorkspace(admin)), Check: readWorkspaceID(&id)},
			{
				PreConfig:        func() { f.archive(id) },
				Config:           workspaceConfig(minimalWorkspace(admin)),
				ConfigPlanChecks: providertest.ExpectAction(workspaceAddr, plancheck.ResourceActionCreate),
			},
		},
	})
	// The refresh lists by name, then every active workspace, then the
	// archived ones too.
	var sawArchived bool
	for _, r := range f.Requests(routeListWorkspaces) {
		sawArchived = sawArchived || r.Query.Encode() == "is_archived=true&page=1&page_size=100"
	}
	if !sawArchived {
		t.Error("the refresh never listed the archived workspaces")
	}
	f.AssertCalled(routeCreateWorkspace, 2)
	// The archived workspace is not archived again.
	f.AssertCalled(routeArchiveWorkspace, 1)
}

// A workspace renamed in the Admin Panel escapes the search by name: the
// refresh finds it in the whole list, and the apply renames it back.
func TestWorkspaceResourceRenamesBackAWorkspaceRenamedOutsideTerraform(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	admin := f.addUser("admin@example.com")
	var id string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: workspaceConfig(minimalWorkspace(admin)), Check: readWorkspaceID(&id)},
			{
				PreConfig:        func() { f.workspaces.Patch(id, map[string]any{"name": "Renamed"}) },
				Config:           workspaceConfig(minimalWorkspace(admin)),
				ConfigPlanChecks: providertest.ExpectAction(workspaceAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(workspaceAddr, tfjsonpath.New("name"), knownvalue.StringExact("ML")),
				},
			},
		},
	})
	f.AssertJSONBodies(routeUpdateWorkspace, `{"name": "ML", "description": "", "icon": ""}`)
}

// The search by name can match more than a page: findWorkspace walks them.
func TestWorkspaceResourceFindsItsWorkspaceOnALaterPage(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	admin := f.addUser("admin@example.com")
	f.seedMany("ML", 120)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:           workspaceConfig(minimalWorkspace(admin)),
			ConfigPlanChecks: providertest.ExpectAction(workspaceAddr, plancheck.ResourceActionCreate),
		}},
	})
	var secondPage int
	for _, r := range f.Requests(routeListWorkspaces) {
		if r.Query.Encode() == "page=2&page_size=100&search=ML" {
			secondPage++
		}
	}
	if secondPage == 0 {
		t.Errorf("no read reached page 2 of the search")
	}
}

// A create that meets a 5xx is sent once: the workspace may exist, and a
// replay would create a second one.
func TestWorkspaceResourceCreateIsSentOnceOnA5xx(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.FailNext(routeCreateWorkspace, 500, 1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      workspaceConfig(minimalWorkspace(sdktest.NewUUID())),
			ExpectError: regexp.MustCompile(`Cannot\s+create\s+the\s+workspace(.|\n)*500`),
		}},
	})
	f.AssertCalled(routeCreateWorkspace, 1)
}

// Update, read and archive errors of the API reach the user.
func TestWorkspaceResourceReportsErrors(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	admin := f.addUser("admin@example.com")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: workspaceConfig(minimalWorkspace(admin))},
			{
				PreConfig:   func() { f.FailNextWith(routeUpdateWorkspace, 409, "A Workspace with this name already exists", 1) },
				Config:      workspaceConfig(fmt.Sprintf("  name          = \"Taken\"\n  admin_user_id = %q\n", admin)),
				ExpectError: regexp.MustCompile(`Cannot\s+update\s+the\s+workspace(.|\n)*409`),
			},
			{
				PreConfig:   func() { f.FailNext(routeListWorkspaces, 500, 4) },
				Config:      workspaceConfig(minimalWorkspace(admin)),
				ExpectError: regexp.MustCompile(`Cannot\s+read\s+the\s+workspace(.|\n)*500`),
			},
			{
				PreConfig:   func() { f.FailNext(routeArchiveWorkspace, 500, 4) },
				Config:      workspaceConfig(minimalWorkspace(admin)),
				Destroy:     true,
				ExpectError: regexp.MustCompile(`Cannot\s+archive\s+the\s+workspace(.|\n)*500`),
			},
		},
	})
}

// The default workspace cannot be archived: destroying it fails, and a
// removed block takes it out of the state instead, as its page says.
func TestWorkspaceResourceCannotArchiveTheDefaultWorkspace(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	admin := f.addUser("admin@example.com")
	id := f.seed("Default Workspace", true)
	config := workspaceConfig(fmt.Sprintf("  name          = \"Default Workspace\"\n  admin_user_id = %q\n", admin))

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config: config + "import {\n  to = mistral_workspace.this\n  id = \"" + id + "\"\n}\n",
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(workspaceAddr, tfjsonpath.New("is_default"), knownvalue.Bool(true)),
				},
			},
			{
				Config:      config,
				Destroy:     true,
				ExpectError: regexp.MustCompile(`Cannot\s+archive\s+the\s+workspace(.|\n)*default\s+Workspace\s+cannot\s+be\s+archived`),
			},
			{
				Config: providertest.ProviderConfig() + "removed {\n  from = mistral_workspace.this\n  lifecycle {\n    destroy = false\n  }\n}\n",
			},
		},
	})
	if ws, _ := f.workspaces.Get(id); ws["archived"] == true {
		t.Error("the default workspace was archived")
	}
}

// Invalid arguments fail at plan time, before any call.
func TestWorkspaceResourceValidatesItsArguments(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	cases := map[string]struct{ body, err string }{
		"empty name":       {"  name = \"\"\n  admin_user_id = \"u\"", `character\s+count\s+must\s+be\s+between\s+1\s+and\s+255`},
		"long icon":        {"  name = \"ML\"\n  admin_user_id = \"u\"\n  icon = \"abc\"", `character\s+count\s+must\s+be\s+at\s+most\s+2`},
		"long description": {"  name = \"ML\"\n  admin_user_id = \"u\"\n  description = \"" + strings.Repeat("a", 256) + "\"", `at\s+most\s+255`},
		"no admin":         {"  name = \"ML\"", `"admin_user_id"\s+is\s+required`},
		"empty admin":      {"  name = \"ML\"\n  admin_user_id = \"\"", `string\s+length\s+must\s+be\s+at\s+least\s+1`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
				Steps: []resource.TestStep{{
					Config:      workspaceConfig(c.body),
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(c.err),
				}},
			})
		})
	}
	f.AssertCalled("", 0)
}

// Without the Admin API key, the workspace types fail at plan time and call
// nothing.
func TestWorkspaceResourceNeedsTheAdminKey(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: `
provider "mistral" {
  api_key       = "` + sdktest.TestAPIKey + `"
  admin_api_key = ""
}

resource "mistral_workspace" "this" {
  name          = "ML"
  admin_user_id = "u"
}
`,
			ExpectError: regexp.MustCompile(`Missing\s+Mistral\s+credential(.|\n)*"admin_api_key"`),
		}},
	})
	f.AssertCalled("", 0)
}

// Importing an ID no workspace has fails as Terraform words it, not with
// a framework error about the missing identity.
func TestWorkspaceResourceImportOfAMissingWorkspace(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:        workspaceConfig(minimalWorkspace("u")),
			ResourceName:  workspaceAddr,
			ImportState:   true,
			ImportStateId: sdktest.NewUUID(),
			ExpectError:   regexp.MustCompile(`Cannot\s+import\s+non-existent\s+remote\s+object`),
		}},
	})
	f.AssertCalled(routeCreateWorkspace, 0)
}
