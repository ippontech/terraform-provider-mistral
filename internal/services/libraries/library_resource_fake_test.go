// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package libraries_test

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

const libraryAddr = "mistral_library.this"

// uuid matches the IDs of the fake.
var uuid = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// libraryConfig is the provider block and a library with body.
func libraryConfig(body string) string {
	return providertest.ProviderConfig() + "resource \"mistral_library\" \"this\" {\n" + body + "\n}\n"
}

const minimalLibrary = `  name = "support"`

// TestLibraryResourceLifecycle creates a library, sets then clears its
// description, and imports it by ID and by identity.
func TestLibraryResourceLifecycle(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config:           libraryConfig(minimalLibrary),
				ConfigPlanChecks: providertest.ExpectAction(libraryAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(libraryAddr, tfjsonpath.New("id"), knownvalue.StringRegexp(uuid)),
					statecheck.ExpectKnownValue(libraryAddr, tfjsonpath.New("name"), knownvalue.StringExact("support")),
					statecheck.ExpectKnownValue(libraryAddr, tfjsonpath.New("description"), knownvalue.Null()),
					statecheck.ExpectKnownValue(libraryAddr, tfjsonpath.New("owner_type"), knownvalue.StringExact("Workspace")),
					statecheck.ExpectKnownValue(libraryAddr, tfjsonpath.New("owner_id"), knownvalue.StringExact(workspaceID)),
					statecheck.ExpectKnownValue(libraryAddr, tfjsonpath.New("nb_documents"), knownvalue.Int64Exact(0)),
					statecheck.ExpectKnownValue(libraryAddr, tfjsonpath.New("total_size"), knownvalue.Int64Exact(0)),
					statecheck.ExpectKnownValue(libraryAddr, tfjsonpath.New("created_at"), knownvalue.StringExact(at(1))),
					statecheck.ExpectKnownValue(libraryAddr, tfjsonpath.New("updated_at"), knownvalue.StringExact(at(1))),
				},
			},
			{
				Config: libraryConfig(`
  name        = "support-docs"
  description = "Product documentation for the support agent"
`),
				ConfigPlanChecks: providertest.ExpectAction(libraryAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(libraryAddr, tfjsonpath.New("name"), knownvalue.StringExact("support-docs")),
					statecheck.ExpectKnownValue(libraryAddr, tfjsonpath.New("description"), knownvalue.StringExact("Product documentation for the support agent")),
					statecheck.ExpectKnownValue(libraryAddr, tfjsonpath.New("created_at"), knownvalue.StringExact(at(1))),
					statecheck.ExpectKnownValue(libraryAddr, tfjsonpath.New("updated_at"), knownvalue.StringExact(at(2))),
				},
			},
			{
				// Removing the description clears it.
				Config:           libraryConfig(`  name = "support-docs"`),
				ConfigPlanChecks: providertest.ExpectAction(libraryAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(libraryAddr, tfjsonpath.New("description"), knownvalue.Null()),
					statecheck.ExpectKnownValue(libraryAddr, tfjsonpath.New("updated_at"), knownvalue.StringExact(at(3))),
				},
			},
			{
				ResourceName:      libraryAddr,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				ResourceName:    libraryAddr,
				ImportState:     true,
				ImportStateKind: resource.ImportBlockWithResourceIdentity,
			},
		},
	})

	f.AssertJSONBodies(routeCreateLibrary, `{"name": "support", "description": null, "owner_type": "Workspace"}`)
	f.AssertJSONBodies(routeUpdateLibrary,
		`{"name": "support-docs", "description": "Product documentation for the support agent"}`,
		`{"name": "support-docs", "description": null}`)
	f.AssertCalled(routeDeleteLibrary, 1)
	f.AssertCredential("", sdktest.APIKey)
}

// owner_type is create-only: changing it replaces the library.
func TestLibraryResourceReplacesTheLibraryForAnotherOwner(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: libraryConfig(minimalLibrary)},
			{
				Config:           libraryConfig(minimalLibrary + "\n  owner_type = \"User\""),
				ConfigPlanChecks: providertest.ExpectAction(libraryAddr, plancheck.ResourceActionReplace),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(libraryAddr, tfjsonpath.New("owner_type"), knownvalue.StringExact("User")),
					statecheck.ExpectKnownValue(libraryAddr, tfjsonpath.New("owner_id"), knownvalue.StringExact(userID)),
				},
			},
		},
	})
	f.AssertJSONBodies(routeCreateLibrary,
		`{"name": "support", "description": null, "owner_type": "Workspace"}`,
		`{"name": "support", "description": null, "owner_type": "User"}`)
}

// nb_documents and total_size follow the documents, which other resources of
// the same apply delete: an update must not plan the value of the state.
func TestLibraryResourceCountsFollowTheDocuments(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	guide := writeFile(t, "guide.md", "# Guide\n")
	withDocument := func(name string) string {
		return libraryConfig(`  name = "`+name+`"`) + `
resource "mistral_library_document" "guide" {
  library_id = mistral_library.this.id
  source     = "` + guide + `"
}
`
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: withDocument("support")},
			{
				// The refresh reads the document the first apply uploaded.
				Config: withDocument("support"),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(libraryAddr, tfjsonpath.New("nb_documents"), knownvalue.Int64Exact(1)),
					statecheck.ExpectKnownValue(libraryAddr, tfjsonpath.New("total_size"), knownvalue.Int64Exact(8)),
				},
			},
			{
				// Terraform deletes the document before it updates the library.
				Config: libraryConfig(`  name = "renamed"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(libraryAddr, plancheck.ResourceActionUpdate),
						plancheck.ExpectUnknownValue(libraryAddr, tfjsonpath.New("nb_documents")),
						plancheck.ExpectUnknownValue(libraryAddr, tfjsonpath.New("total_size")),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(libraryAddr, tfjsonpath.New("nb_documents"), knownvalue.Int64Exact(0)),
					statecheck.ExpectKnownValue(libraryAddr, tfjsonpath.New("total_size"), knownvalue.Int64Exact(0)),
				},
			},
		},
	})
}

// A library deleted outside Terraform plans a create.
func TestLibraryResourceRecreatesALibraryDeletedOutsideTerraform(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	var id string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config: libraryConfig(minimalLibrary),
				Check: func(s *terraform.State) error {
					id = s.RootModule().Resources[libraryAddr].Primary.ID
					return nil
				},
			},
			{
				PreConfig:        func() { f.libraries.Delete(id) },
				Config:           libraryConfig(minimalLibrary),
				ConfigPlanChecks: providertest.ExpectAction(libraryAddr, plancheck.ResourceActionCreate),
			},
		},
	})
	f.AssertCalled(routeCreateLibrary, 2)
}

// A library built in AI Studio imports with an import block and plans no
// change: owner_type reads as the default of the schema.
func TestLibraryResourceImportsALibraryCreatedOutsideTerraform(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	id := f.seedLibrary()

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig() + `
import {
  to = mistral_library.this
  id = "` + id + `"
}

resource "mistral_library" "this" {
  name        = "studio"
  description = "Built in AI Studio"
}
`,
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(libraryAddr, plancheck.ResourceActionNoop)},
			},
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(libraryAddr, tfjsonpath.New("id"), knownvalue.StringExact(id)),
			},
		}},
	})
	f.AssertCalled(routeUpdateLibrary, 0)
}

// A create that meets a 5xx is sent once: the library may exist, and a
// replay would create a second one.
func TestLibraryResourceCreateIsSentOnceOnA5xx(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.FailNext(routeCreateLibrary, 500, 1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      libraryConfig(minimalLibrary),
			ExpectError: regexp.MustCompile(`Cannot\s+create\s+the\s+library(.|\n)*500`),
		}},
	})
	f.AssertCalled(routeCreateLibrary, 1)
}

// Errors other than 404 reach the user; an update is sent once.
func TestLibraryResourceReportsErrors(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: libraryConfig(minimalLibrary)},
			{
				PreConfig:   func() { f.FailNextWith(routeGetLibrary, 401, "Unauthorized", 1) },
				Config:      libraryConfig(minimalLibrary),
				ExpectError: regexp.MustCompile(`Cannot\s+read\s+the\s+library(.|\n)*401`),
			},
			{
				PreConfig:   func() { f.FailNext(routeUpdateLibrary, 503, 1) },
				Config:      libraryConfig(`  name = "renamed"`),
				ExpectError: regexp.MustCompile(`Cannot\s+update\s+the\s+library(.|\n)*503`),
			},
			{
				PreConfig:   func() { f.FailNext(routeDeleteLibrary, 500, 4) },
				Config:      libraryConfig(minimalLibrary),
				Destroy:     true,
				ExpectError: regexp.MustCompile(`Cannot\s+delete\s+the\s+library(.|\n)*500`),
			},
		},
	})
	f.AssertCalled(routeUpdateLibrary, 1)
}

// Invalid arguments fail the plan, before any request.
func TestLibraryResourceValidatesItsArguments(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	cases := map[string]struct{ body, err string }{
		"empty name":         {`name = ""`, `string\s+length\s+must\s+be\s+at\s+least\s+1`},
		"empty description":  {minimalLibrary + "\n" + `description = ""`, `string\s+length\s+must\s+be\s+at\s+least\s+1`},
		"unknown owner type": {minimalLibrary + "\n" + `owner_type = "Org"`, `value\s+must\s+be\s+one\s+of`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
				Steps: []resource.TestStep{{
					Config:      libraryConfig(c.body),
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(c.err),
				}},
			})
		})
	}
	f.AssertCalled("", 0)
}

// Importing a library that does not exist reports it, for each resource.
func TestImportOfAMissingObjectReportsIt(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	libraryID := f.seedLibrary()
	guide := writeFile(t, "guide.md", "# Guide\n")
	missing := "00000000-0000-4000-8000-999999999999"

	for name, c := range map[string]struct{ id, block string }{
		"library": {missing, `resource "mistral_library" "this" {
  name = "studio"
}`},
		"share": {libraryID + ":Workspace:" + supportWorkspaceID, shareBlock(`"`+libraryID+`"`, "Workspace", supportWorkspaceID, "Viewer")},
		"document": {libraryID + ":" + missing, `resource "mistral_library_document" "guide" {
  library_id = "` + libraryID + `"
  source     = "` + guide + `"
}`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			addr := map[string]string{"library": libraryAddr, "share": shareAddr, "document": documentAddr}[name]
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
				Steps: []resource.TestStep{{
					Config:      providertest.ProviderConfig() + c.block + "\nimport {\n  to = " + addr + "\n  id = \"" + c.id + "\"\n}\n",
					ExpectError: regexp.MustCompile(`Cannot\s+import\s+non-existent\s+remote\s+object`),
				}},
			})
		})
	}
}
