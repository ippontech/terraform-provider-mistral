// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package promptsandskills_test

import (
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

// promptAddr is the address of the prompt the tests manage.
const promptAddr = "mistral_prompt.this"

// promptConfig is a configuration with one mistral_prompt of body.
func promptConfig(body string) string {
	return providertest.ProviderConfig() + "resource \"mistral_prompt\" \"this\" {\n" + body + "\n}\n"
}

// minimalPrompt sets the required arguments only.
const minimalPrompt = `
  name    = "coding-assistant"
  content = "You are a coding assistant."
`

// The steps of the lifecycle, each changing one part of the prompt: the
// version (fullPrompt, trimmedPrompt), the prompt alone (retitledPrompt), or
// the notes of the latest version alone (renotedPrompt, unnotedPrompt).
const (
	fullPrompt = `
  name               = "coding-assistant"
  title              = "Coding assistant"
  description        = "Helps with code reviews."
  content            = "You are a coding assistant for {{language}}."
  variables          = [{ name = "language" }]
  notes              = "Add the language"
  sharing_scope      = "workspace"
  workspace_relation = "reader"
`
	retitledPrompt = `
  name          = "coding-assistant"
  title         = "Code assistant"
  description   = "Helps with code reviews."
  content       = "You are a coding assistant for {{language}}."
  variables     = [{ name = "language" }]
  notes         = "Add the language"
  sharing_scope = "workspace"
`
	renotedPrompt = `
  name          = "coding-assistant"
  title         = "Code assistant"
  description   = "Helps with code reviews."
  content       = "You are a coding assistant for {{language}}."
  variables     = [{ name = "language" }]
  notes         = "Pin the language"
  sharing_scope = "workspace"
`
	unnotedPrompt = `
  name          = "coding-assistant"
  title         = "Code assistant"
  description   = "Helps with code reviews."
  content       = "You are a coding assistant for {{language}}."
  variables     = [{ name = "language" }]
  sharing_scope = "workspace"
`
	trimmedPrompt = `
  name    = "coding-assistant"
  content = "You are a terse coding assistant."
`
)

// expectNewVersion is the plan check of an update that creates a version:
// the version is unknown until the apply.
func expectNewVersion(addr string) resource.ConfigPlanChecks {
	checks := providertest.ExpectAction(addr, plancheck.ResourceActionUpdate)
	checks.PreApply = append(checks.PreApply, plancheck.ExpectUnknownValue(addr, tfjsonpath.New("version")))
	return checks
}

// expectSameVersion is the plan check of an update that creates no version:
// the plan already holds the version of the state.
func expectSameVersion(addr string, version int64) resource.ConfigPlanChecks {
	checks := providertest.ExpectAction(addr, plancheck.ResourceActionUpdate)
	checks.PreApply = append(checks.PreApply, plancheck.ExpectKnownValue(addr, tfjsonpath.New("version"), knownvalue.Int64Exact(version)))
	return checks
}

// stateIs checks exact values of the state of addr, attribute by attribute.
func stateIs(addr string, want map[string]knownvalue.Check) []statecheck.StateCheck {
	checks := make([]statecheck.StateCheck, 0, len(want))
	for attr, check := range want {
		checks = append(checks, statecheck.ExpectKnownValue(addr, tfjsonpath.New(attr), check))
	}
	return checks
}

// TestPromptResourceLifecycle takes a prompt through each kind of update,
// checks the plan and the state of each step and the exact body of each
// call, then imports it by ID and by identity.
func TestPromptResourceLifecycle(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	var id string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config:           promptConfig(minimalPrompt),
				ConfigPlanChecks: providertest.ExpectAction(promptAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: stateIs(promptAddr, map[string]knownvalue.Check{
					"name":               knownvalue.StringExact("coding-assistant"),
					"content":            knownvalue.StringExact("You are a coding assistant."),
					"version":            knownvalue.Int64Exact(1),
					"title":              knownvalue.Null(),
					"description":        knownvalue.Null(),
					"variables":          knownvalue.Null(),
					"notes":              knownvalue.Null(),
					"sharing_scope":      knownvalue.StringExact("private"),
					"workspace_relation": knownvalue.Null(),
					"created_at":         knownvalue.StringExact(at(1)),
					"updated_at":         knownvalue.StringExact(at(1)),
				}),
				Check: func(s *terraform.State) error {
					id = s.RootModule().Resources[promptAddr].Primary.ID
					return nil
				},
			},
			{
				// A new template and new display fields: a version, then the
				// update of the prompt.
				Config:           promptConfig(fullPrompt),
				ConfigPlanChecks: expectNewVersion(promptAddr),
				ConfigStateChecks: stateIs(promptAddr, map[string]knownvalue.Check{
					"version":            knownvalue.Int64Exact(2),
					"title":              knownvalue.StringExact("Coding assistant"),
					"content":            knownvalue.StringExact("You are a coding assistant for {{language}}."),
					"variables":          knownvalue.ListExact([]knownvalue.Check{knownvalue.ObjectExact(map[string]knownvalue.Check{"name": knownvalue.StringExact("language")})}),
					"notes":              knownvalue.StringExact("Add the language"),
					"sharing_scope":      knownvalue.StringExact("workspace"),
					"workspace_relation": knownvalue.StringExact("reader"),
					"created_at":         knownvalue.StringExact(at(1)),
					"updated_at":         knownvalue.StringExact(at(3)),
				}),
			},
			{
				// A new title, and the relation left to its default: the
				// prompt alone.
				Config:           promptConfig(retitledPrompt),
				ConfigPlanChecks: expectSameVersion(promptAddr, 2),
				ConfigStateChecks: stateIs(promptAddr, map[string]knownvalue.Check{
					"version":            knownvalue.Int64Exact(2),
					"title":              knownvalue.StringExact("Code assistant"),
					"workspace_relation": knownvalue.StringExact("writer"),
					"updated_at":         knownvalue.StringExact(at(4)),
				}),
			},
			{
				Config:           promptConfig(renotedPrompt),
				ConfigPlanChecks: expectSameVersion(promptAddr, 2),
				ConfigStateChecks: stateIs(promptAddr, map[string]knownvalue.Check{
					"version": knownvalue.Int64Exact(2),
					"notes":   knownvalue.StringExact("Pin the language"),
				}),
			},
			{
				Config:            promptConfig(unnotedPrompt),
				ConfigPlanChecks:  expectSameVersion(promptAddr, 2),
				ConfigStateChecks: stateIs(promptAddr, map[string]knownvalue.Check{"notes": knownvalue.Null()}),
			},
			{
				// Emptying the optional fields clears them: a version without
				// variables, then the prompt without title, description or
				// sharing.
				Config:           promptConfig(trimmedPrompt),
				ConfigPlanChecks: expectNewVersion(promptAddr),
				ConfigStateChecks: stateIs(promptAddr, map[string]knownvalue.Check{
					"version":            knownvalue.Int64Exact(3),
					"title":              knownvalue.Null(),
					"description":        knownvalue.Null(),
					"variables":          knownvalue.Null(),
					"notes":              knownvalue.Null(),
					"sharing_scope":      knownvalue.StringExact("private"),
					"workspace_relation": knownvalue.Null(),
					"updated_at":         knownvalue.StringExact(at(8)),
				}),
			},
			{
				ResourceName:      promptAddr,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				ResourceName:    promptAddr,
				ImportState:     true,
				ImportStateKind: resource.ImportBlockWithResourceIdentity,
			},
		},
	})

	f.AssertJSONBodies(routePromptCreate, `{
		"name": "coding-assistant", "definition": {"content": "You are a coding assistant.", "variables": []},
		"title": null, "description": null, "notes": null, "sharingScope": "private", "workspaceRelation": null
	}`)
	f.AssertJSONBodies(routePromptVersion,
		`{"definition": {"content": "You are a coding assistant for {{language}}.", "variables": [{"name": "language"}]}, "notes": "Add the language"}`,
		`{"definition": {"content": "You are a terse coding assistant.", "variables": []}, "notes": null}`)
	f.AssertJSONBodies(routePromptUpdate,
		`{"title": "Coding assistant", "description": "Helps with code reviews.", "sharingScope": "workspace", "workspaceRelation": "reader"}`,
		`{"title": "Code assistant", "description": "Helps with code reviews.", "sharingScope": "workspace", "workspaceRelation": "writer"}`,
		`{"title": "", "description": "", "sharingScope": "private", "workspaceRelation": null}`)
	f.AssertJSONBodies(routePromptNotes, `{"notes": "Pin the language"}`, `{"notes": ""}`)
	for _, r := range f.Requests(routePromptNotes) {
		if want := "/v2/prompts/" + id + "/versions/2"; r.Path != want {
			t.Errorf("notes path = %s, want %s", r.Path, want)
		}
	}
	for _, route := range []string{routePromptGet, routePromptDelete} {
		for _, r := range f.Requests(route) {
			if len(r.Query) > 0 {
				t.Errorf("%s sent the query %s: the latest version is the one without a query", route, r.Query.Encode())
			}
		}
	}
	f.AssertCalled(routePromptDelete, 1)
	f.AssertCredential("", sdktest.APIKey)
}

// A prompt deleted outside Terraform plans a create.
func TestPromptResourceRecreatesAPromptDeletedOutsideTerraform(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	var id string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config: promptConfig(minimalPrompt),
				Check: func(s *terraform.State) error {
					id = s.RootModule().Resources[promptAddr].Primary.ID
					return nil
				},
			},
			{
				PreConfig:        func() { f.prompts.objects.Delete(id) },
				Config:           promptConfig(minimalPrompt),
				ConfigPlanChecks: providertest.ExpectAction(promptAddr, plancheck.ResourceActionCreate),
			},
		},
	})
	f.AssertCalled(routePromptCreate, 2)
}

// A version created in Studio shows as drift, and the apply writes the
// configuration back as one more version.
func TestPromptResourceRevertsAVersionCreatedOutsideTerraform(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	var id string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config: promptConfig(minimalPrompt),
				Check: func(s *terraform.State) error {
					id = s.RootModule().Resources[promptAddr].Primary.ID
					return nil
				},
			},
			{
				PreConfig: func() {
					f.prompts.addVersion(id, map[string]any{"content": "Changed in Studio"}, "Studio edit")
				},
				Config:           promptConfig(minimalPrompt),
				ConfigPlanChecks: expectNewVersion(promptAddr),
				ConfigStateChecks: stateIs(promptAddr, map[string]knownvalue.Check{
					"version": knownvalue.Int64Exact(3),
					"content": knownvalue.StringExact("You are a coding assistant."),
					"notes":   knownvalue.Null(),
				}),
				Check: func(*terraform.State) error {
					if got := f.prompts.latest(id)["definition"].(map[string]any)["content"]; got != "You are a coding assistant." {
						t.Errorf("latest content = %v", got)
					}
					return nil
				},
			},
		},
	})
}

// A prompt created outside Terraform imports with an import block and plans
// no change: the fields its answer leaves out read as null, and an absent
// sharing scope as private.
func TestPromptResourceImportsAPromptCreatedOutsideTerraform(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	id := f.prompts.seed(
		map[string]any{"name": "studio-prompt", "title": "From Studio"},
		map[string]any{"content": "Translate to {{language}}.", "variables": []any{map[string]any{"name": "language"}}},
	)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig() + `
import {
  to = mistral_prompt.this
  id = "` + id + `"
}

resource "mistral_prompt" "this" {
  name      = "studio-prompt"
  title     = "From Studio"
  content   = "Translate to {{language}}."
  variables = [{ name = "language" }]
}
`,
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(promptAddr, plancheck.ResourceActionNoop)},
			},
			ConfigStateChecks: stateIs(promptAddr, map[string]knownvalue.Check{
				"version":       knownvalue.Int64Exact(1),
				"sharing_scope": knownvalue.StringExact("private"),
				"description":   knownvalue.Null(),
			}),
		}},
	})
	f.AssertCalled(routePromptUpdate, 0)
	f.AssertCalled(routePromptVersion, 0)
}
