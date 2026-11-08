// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package promptsandskills_test

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/ippontech/terraform-provider-mistral/internal/providertest"
	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

// skillAddr is the address of the skill the tests manage.
const skillAddr = "mistral_skill.this"

// skillConfig is a configuration with one mistral_skill of body.
func skillConfig(body string) string {
	return providertest.ProviderConfig() + "resource \"mistral_skill\" \"this\" {\n" + body + "\n}\n"
}

// minimalSkill sets the name and the body only.
const minimalSkill = `
  name = "extract-invoices"
  body = "Extract the totals of the invoice."
`

// The steps of the lifecycle: assets and the workspace scope (fullSkill), a
// removed asset key and description (trimmedSkill), then the notes removed
// alone with the private scope (privateSkill).
const (
	fullSkill = `
  name        = "extract-invoices"
  description = "Use it on invoices in PDF."
  body        = "Extract the totals of the invoice."
  assets = {
    "scripts/extract.py" = { content = "print('total')", executable = true }
    "logo.png"           = { content_base64 = "iVBORw0KGgo=" }
    "README.md"          = { content = "" }
  }
  notes         = "Add the scripts"
  sharing_scope = "workspace"
`
	trimmedSkill = `
  name = "extract-invoices"
  body = "Extract the totals of the invoice."
  assets = {
    "scripts/extract.py" = { content = "print('total')" }
    "logo.png"           = { content_base64 = "iVBORw0KGgo=" }
  }
  notes         = "Add the scripts"
  sharing_scope = "workspace"
`
	privateSkill = `
  name = "extract-invoices"
  body = "Extract the totals of the invoice."
  assets = {
    "scripts/extract.py" = { content = "print('total')" }
    "logo.png"           = { content_base64 = "iVBORw0KGgo=" }
  }
`
)

// assetIs is the exact value of one asset in the state.
func assetIs(content, contentBase64 knownvalue.Check, executable bool) knownvalue.Check {
	return knownvalue.ObjectExact(map[string]knownvalue.Check{
		"content": content, "content_base64": contentBase64, "executable": knownvalue.Bool(executable),
	})
}

// TestSkillResourceLifecycle takes a skill through a version with assets, a
// version that removes an asset key, and a change of notes and sharing that
// creates no version, with the exact body of each call, then imports it by
// ID and by identity.
func TestSkillResourceLifecycle(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config:           skillConfig(minimalSkill),
				ConfigPlanChecks: providertest.ExpectAction(skillAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: stateIs(skillAddr, map[string]knownvalue.Check{
					"name":               knownvalue.StringExact("extract-invoices"),
					"body":               knownvalue.StringExact("Extract the totals of the invoice."),
					"description":        knownvalue.Null(),
					"assets":             knownvalue.Null(),
					"notes":              knownvalue.Null(),
					"version":            knownvalue.Int64Exact(1),
					"sharing_scope":      knownvalue.StringExact("private"),
					"workspace_relation": knownvalue.Null(),
					"created_at":         knownvalue.StringExact(at(1)),
					"updated_at":         knownvalue.StringExact(at(1)),
				}),
			},
			{
				Config:           skillConfig(fullSkill),
				ConfigPlanChecks: expectNewVersion(skillAddr),
				ConfigStateChecks: stateIs(skillAddr, map[string]knownvalue.Check{
					"version":     knownvalue.Int64Exact(2),
					"description": knownvalue.StringExact("Use it on invoices in PDF."),
					"assets": knownvalue.MapExact(map[string]knownvalue.Check{
						"scripts/extract.py": assetIs(knownvalue.StringExact("print('total')"), knownvalue.Null(), true),
						"logo.png":           assetIs(knownvalue.Null(), knownvalue.StringExact("iVBORw0KGgo="), false),
						"README.md":          assetIs(knownvalue.StringExact(""), knownvalue.Null(), false),
					}),
					"notes":              knownvalue.StringExact("Add the scripts"),
					"sharing_scope":      knownvalue.StringExact("workspace"),
					"workspace_relation": knownvalue.StringExact("writer"),
					"updated_at":         knownvalue.StringExact(at(3)),
				}),
			},
			{
				// A removed asset key, description and executable flag: a
				// version, and no update of the skill.
				Config:           skillConfig(trimmedSkill),
				ConfigPlanChecks: expectNewVersion(skillAddr),
				ConfigStateChecks: stateIs(skillAddr, map[string]knownvalue.Check{
					"version":     knownvalue.Int64Exact(3),
					"description": knownvalue.Null(),
					"assets": knownvalue.MapExact(map[string]knownvalue.Check{
						"scripts/extract.py": assetIs(knownvalue.StringExact("print('total')"), knownvalue.Null(), false),
						"logo.png":           assetIs(knownvalue.Null(), knownvalue.StringExact("iVBORw0KGgo="), false),
					}),
				}),
			},
			{
				// The notes removed alone, and the scope back to private: the
				// update of the skill first, since the sharing narrows, then
				// the notes of version 3.
				Config:           skillConfig(privateSkill),
				ConfigPlanChecks: expectSameVersion(skillAddr, 3),
				ConfigStateChecks: stateIs(skillAddr, map[string]knownvalue.Check{
					"version":            knownvalue.Int64Exact(3),
					"notes":              knownvalue.Null(),
					"sharing_scope":      knownvalue.StringExact("private"),
					"workspace_relation": knownvalue.Null(),
					"created_at":         knownvalue.StringExact(at(1)),
					"updated_at":         knownvalue.StringExact(at(6)),
				}),
			},
			{
				ResourceName:      skillAddr,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				ResourceName:    skillAddr,
				ImportState:     true,
				ImportStateKind: resource.ImportBlockWithResourceIdentity,
			},
		},
	})

	f.AssertJSONBodies(routeSkillCreate, `{
		"name": "extract-invoices", "notes": null, "sharingScope": "private", "workspaceRelation": null,
		"definition": {"description": "", "body": "Extract the totals of the invoice.", "assets": {}}
	}`)
	f.AssertJSONBodies(routeSkillVersion, `{
		"notes": "Add the scripts",
		"definition": {"description": "Use it on invoices in PDF.", "body": "Extract the totals of the invoice.", "assets": {
			"scripts/extract.py": {"textContent": "print('total')", "isExecutable": true},
			"logo.png": {"rawContent": "iVBORw0KGgo=", "isExecutable": false},
			"README.md": {"textContent": "", "isExecutable": false}
		}}
	}`, `{
		"notes": "Add the scripts",
		"definition": {"description": "", "body": "Extract the totals of the invoice.", "assets": {
			"scripts/extract.py": {"textContent": "print('total')", "isExecutable": false},
			"logo.png": {"rawContent": "iVBORw0KGgo=", "isExecutable": false}
		}}
	}`)
	f.AssertJSONBodies(routeSkillUpdate,
		`{"sharingScope": "workspace", "workspaceRelation": "writer"}`,
		`{"sharingScope": "private", "workspaceRelation": null}`)
	f.AssertJSONBodies(routeSkillNotes, `{"notes": ""}`)
	f.AssertCalled(routeSkillDelete, 1)
	f.AssertCredential("", sdktest.APIKey)
}

// A skill deleted outside Terraform plans a create.
func TestSkillResourceRecreatesASkillDeletedOutsideTerraform(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	var id string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config: skillConfig(minimalSkill),
				Check: func(s *terraform.State) error {
					id = s.RootModule().Resources[skillAddr].Primary.ID
					return nil
				},
			},
			{
				PreConfig:        func() { f.skills.objects.Delete(id) },
				Config:           skillConfig(minimalSkill),
				ConfigPlanChecks: providertest.ExpectAction(skillAddr, plancheck.ResourceActionCreate),
			},
		},
	})
	f.AssertCalled(routeSkillCreate, 2)
}

// A version created in Studio shows as drift, and the apply writes the
// configuration back as one more version.
func TestSkillResourceRevertsAVersionCreatedOutsideTerraform(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	var id string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config: skillConfig(minimalSkill),
				Check: func(s *terraform.State) error {
					id = s.RootModule().Resources[skillAddr].Primary.ID
					return nil
				},
			},
			{
				PreConfig: func() {
					f.skills.addVersion(id, map[string]any{"body": "Changed in Studio", "assets": map[string]any{"x.txt": map[string]any{"textContent": "x"}}}, "")
				},
				Config:            skillConfig(minimalSkill),
				ConfigPlanChecks:  expectNewVersion(skillAddr),
				ConfigStateChecks: stateIs(skillAddr, map[string]knownvalue.Check{"version": knownvalue.Int64Exact(3), "assets": knownvalue.Null()}),
			},
		},
	})
}

// A skill created outside Terraform imports with an import block and plans
// no change to its definition: an absent isExecutable reads as false, and a
// binary asset keeps the base64 text the API returns.
func TestSkillResourceImportsASkillCreatedOutsideTerraform(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	id := f.skills.seed(map[string]any{"name": "studio-skill", "sharingScope": "workspace"}, map[string]any{
		"body": "From Studio",
		"assets": map[string]any{
			"run.sh":  map[string]any{"textContent": "echo hi", "isExecutable": true},
			"data.db": map[string]any{"rawContent": "-_8"},
		},
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config: providertest.ProviderConfig() + `
import {
  to = mistral_skill.this
  id = "` + id + `"
}

resource "mistral_skill" "this" {
  name = "studio-skill"
  body = "From Studio"
  assets = {
    "run.sh"  = { content = "echo hi", executable = true }
    "data.db" = { content_base64 = "-_8" }
  }
  sharing_scope      = "workspace"
  workspace_relation = "reader"
}
`,
				// The API does not return the relation: the import leaves it
				// unset, and the apply sets the configured one.
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(skillAddr, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: stateIs(skillAddr, map[string]knownvalue.Check{
					"version":            knownvalue.Int64Exact(1),
					"workspace_relation": knownvalue.StringExact("reader"),
				}),
			},
		},
	})
	f.AssertCalled(routeSkillVersion, 0)
	f.AssertJSONBodies(routeSkillUpdate, `{"sharingScope": "workspace", "workspaceRelation": "reader"}`)
}

// A failed call reaches the user with the status of the API, and a create
// or a new version that meets a 5xx is sent once.
func TestSkillResourceErrors(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				PreConfig:   func() { f.FailNext(routeSkillCreate, 502, 1) },
				Config:      skillConfig(minimalSkill),
				ExpectError: regexp.MustCompile(`Cannot\s+create\s+the\s+skill(.|\n)*502`),
			},
			{
				PreConfig:   func() { f.FailNextWith(routeSkillCreate, 409, "Skill exists", 1) },
				Config:      skillConfig(minimalSkill),
				ExpectError: regexp.MustCompile(`skill\s+named\s+"extract-invoices"\s+already\s+exists`),
			},
			{Config: skillConfig(minimalSkill)},
			{
				PreConfig:   func() { f.FailNext(routeSkillVersion, 500, 1) },
				Config:      skillConfig(fullSkill),
				ExpectError: regexp.MustCompile(`Cannot\s+create\s+a\s+version\s+of\s+the\s+skill(.|\n)*500`),
			},
			{
				PreConfig:   func() { f.FailNextWith(routeSkillGet, 403, "Forbidden", 1) },
				Config:      skillConfig(minimalSkill),
				ExpectError: regexp.MustCompile(`Cannot\s+read\s+the\s+skill(.|\n)*403`),
			},
			{
				PreConfig:   func() { f.FailNextWith(routeSkillUpdate, 400, "Bad scope", 1) },
				Config:      skillConfig(minimalSkill + `  sharing_scope = "workspace"`),
				ExpectError: regexp.MustCompile(`Cannot\s+update\s+the\s+skill(.|\n)*400`),
			},
			{
				PreConfig:   func() { f.FailNext(routeSkillDelete, 500, 4) },
				Config:      skillConfig(minimalSkill),
				Destroy:     true,
				ExpectError: regexp.MustCompile(`Cannot\s+delete\s+the\s+skill(.|\n)*500`),
			},
		},
	})
	f.AssertCalled(routeSkillCreate, 3)
	f.AssertCalled(routeSkillVersion, 1)
}

// An invalid configuration fails at plan time, before any call.
func TestSkillResourceValidatesTheConfiguration(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	cases := map[string]struct{ body, err string }{
		"asset with both contents": {
			`assets = { "a.txt" = { content = "a", content_base64 = "YQ==" } }`, `Invalid\s+asset(.|\n)*exactly\s+one`,
		},
		"asset with no content": {
			`assets = { "a.txt" = { executable = true } }`, `Invalid\s+asset`,
		},
		"content that is not base64": {
			`assets = { "a.bin" = { content_base64 = "not base64!" } }`, `Invalid\s+base64`,
		},
		"empty assets": {
			`assets = {}`, `map\s+must\s+contain\s+at\s+least\s+1`,
		},
		"relation with the private scope": {
			`workspace_relation = "reader"`, `Unexpected\s+workspace_relation`,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
				Steps: []resource.TestStep{{
					Config:      skillConfig(minimalSkill + c.body),
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(c.err),
				}},
			})
		})
	}
	f.AssertCalled("", 0)
}

// An asset whose content is unknown at plan time, from another resource, is
// checked at apply time.
func TestSkillResourceAcceptsAssetsUnknownAtPlanTime(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: skillConfig(minimalSkill) + `
resource "mistral_skill" "other" {
  name   = "other"
  body   = "Other."
  assets = { "id.txt" = { content = mistral_skill.this.id } }
}
`,
		}},
	})
}

// The configurations of the sharing tests: each one after sharedSkill
// changes the body and the sharing in one apply.
const (
	sharedSkill = `
  name          = "extract-invoices"
  body          = "Extract the totals of the invoice."
  sharing_scope = "workspace"
`
	unsharedSkill = `
  name = "extract-invoices"
  body = "Extract the totals and the VAT of the invoice."
`
	reSharedSkill = `
  name               = "extract-invoices"
  body               = "Extract the totals, the VAT and the due date of the invoice."
  sharing_scope      = "workspace"
  workspace_relation = "reader"
`
)

// An apply that changes the body and narrows the sharing, from the
// workspace to private, updates the skill before it creates the version, so
// that the new body is never readable by the workspace. One that widens the
// sharing creates the version first.
func TestSkillResourceNarrowsTheSharingBeforeCreatingAVersion(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: skillConfig(sharedSkill)},
			{
				Config:           skillConfig(unsharedSkill),
				ConfigPlanChecks: expectNewVersion(skillAddr),
				ConfigStateChecks: stateIs(skillAddr, map[string]knownvalue.Check{
					"version":            knownvalue.Int64Exact(2),
					"body":               knownvalue.StringExact("Extract the totals and the VAT of the invoice."),
					"sharing_scope":      knownvalue.StringExact("private"),
					"workspace_relation": knownvalue.Null(),
				}),
			},
			{
				Config:           skillConfig(reSharedSkill),
				ConfigPlanChecks: expectNewVersion(skillAddr),
				ConfigStateChecks: stateIs(skillAddr, map[string]knownvalue.Check{
					"version":            knownvalue.Int64Exact(3),
					"body":               knownvalue.StringExact("Extract the totals, the VAT and the due date of the invoice."),
					"sharing_scope":      knownvalue.StringExact("workspace"),
					"workspace_relation": knownvalue.StringExact("reader"),
				}),
			},
		},
	})

	f.assertWrites(routeSkillCreate,
		routeSkillUpdate, routeSkillVersion,
		routeSkillVersion, routeSkillUpdate,
		routeSkillDelete)
	f.AssertJSONBodies(routeSkillUpdate,
		`{"sharingScope": "private", "workspaceRelation": null}`,
		`{"sharingScope": "workspace", "workspaceRelation": "reader"}`)
	f.AssertJSONBodies(routeSkillVersion,
		`{"definition": {"description": "", "body": "Extract the totals and the VAT of the invoice.", "assets": {}}, "notes": null}`,
		`{"definition": {"description": "", "body": "Extract the totals, the VAT and the due date of the invoice.", "assets": {}}, "notes": null}`)
}

// When the update that narrows the sharing fails, nothing else is sent: the
// earlier body stays the latest version. When the version fails after that
// update, the diagnostic says the sharing is applied, and the next plan
// holds the version only.
func TestSkillResourceCreatesNoVersionWhenNarrowingTheSharingFails(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	var id string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config: skillConfig(sharedSkill),
				Check: func(s *terraform.State) error {
					id = s.RootModule().Resources[skillAddr].Primary.ID
					return nil
				},
			},
			{
				PreConfig: func() { f.FailNext(routeSkillUpdate, 503, 1) },
				Config:    skillConfig(unsharedSkill),
				ExpectError: regexp.MustCompile(`Cannot\s+update\s+the\s+skill(.|\n)*503(.|\n)*` +
					`Not\s+attempted:\s+a\s+new\s+version\s+with\s+the\s+planned\s+definition\.`),
			},
			{
				PreConfig: func() {
					f.skills.assertLatest(id, 1, map[string]any{"body": "Extract the totals of the invoice."})
					f.FailNextWith(routeSkillVersion, 400, "Body refused", 1)
				},
				Config: skillConfig(unsharedSkill),
				ExpectError: regexp.MustCompile(`Cannot\s+create\s+a\s+version\s+of\s+the\s+skill(.|\n)*Body\s+refused(.|\n)*` +
					`Already\s+applied:\s+the\s+sharing\s+of\s+the\s+skill\.`),
			},
			{
				Config:           skillConfig(unsharedSkill),
				ConfigPlanChecks: expectNewVersion(skillAddr),
				ConfigStateChecks: stateIs(skillAddr, map[string]knownvalue.Check{
					"version":       knownvalue.Int64Exact(2),
					"sharing_scope": knownvalue.StringExact("private"),
				}),
			},
		},
	})

	f.assertWrites(routeSkillCreate,
		routeSkillUpdate,
		routeSkillUpdate, routeSkillVersion,
		routeSkillVersion,
		routeSkillDelete)
}

// When the version fails after the update that makes the workspace readers,
// the state records reader, which no answer holds. A configuration set back
// to writers and to the earlier body then plans the update of the skill and
// sends writer, instead of planning nothing while the API keeps reader.
func TestSkillResourceRecordsTheRelationOfAnUpdateBeforeAFailedVersion(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: skillConfig(sharedSkill)},
			{
				PreConfig: func() { f.FailNextWith(routeSkillVersion, 400, "Body refused", 1) },
				Config:    skillConfig(reSharedSkill),
				ExpectError: regexp.MustCompile(`Cannot\s+create\s+a\s+version\s+of\s+the\s+skill(.|\n)*Body\s+refused(.|\n)*` +
					`Already\s+applied:\s+the\s+sharing\s+of\s+the\s+skill\.`),
			},
			{
				Config:           skillConfig(sharedSkill),
				ConfigPlanChecks: expectSameVersion(skillAddr, 1),
				ConfigStateChecks: stateIs(skillAddr, map[string]knownvalue.Check{
					"version":            knownvalue.Int64Exact(1),
					"body":               knownvalue.StringExact("Extract the totals of the invoice."),
					"sharing_scope":      knownvalue.StringExact("workspace"),
					"workspace_relation": knownvalue.StringExact("writer"),
				}),
			},
		},
	})

	f.assertWrites(routeSkillCreate,
		routeSkillUpdate, routeSkillVersion,
		routeSkillUpdate,
		routeSkillDelete)
	f.AssertJSONBodies(routeSkillUpdate,
		`{"sharingScope": "workspace", "workspaceRelation": "reader"}`,
		`{"sharingScope": "workspace", "workspaceRelation": "writer"}`)
}
