// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package promptsandskills_test

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"

	"github.com/ippontech/terraform-provider-mistral/internal/providertest"
)

// The template of the second version of the error tests.
const secondPrompt = `
  name    = "coding-assistant"
  content = "You are a careful coding assistant."
  title   = "Coding assistant"
`

// A create that meets a 5xx is sent once: the prompt may exist, and a replay
// would create a second one.
func TestPromptResourceCreateIsSentOnceOnA5xx(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.FailNext(routePromptCreate, 500, 1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      promptConfig(minimalPrompt),
			ExpectError: regexp.MustCompile(`Cannot\s+create\s+the\s+prompt(.|\n)*500`),
		}},
	})
	f.AssertCalled(routePromptCreate, 1)
}

// A new version that meets a 5xx is sent once, and nothing else is called:
// the version comes first.
func TestPromptResourceNewVersionIsSentOnceOnA5xx(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: promptConfig(minimalPrompt)},
			{
				PreConfig:   func() { f.FailNext(routePromptVersion, 503, 1) },
				Config:      promptConfig(secondPrompt),
				ExpectError: regexp.MustCompile(`Cannot\s+create\s+a\s+version\s+of\s+the\s+prompt(.|\n)*503`),
			},
		},
	})
	f.AssertCalled(routePromptVersion, 1)
	f.AssertCalled(routePromptUpdate, 0)
}

// When the update of the prompt fails after its new version, the next plan
// reads the version and holds the update of the prompt only.
func TestPromptResourceRepairsAnUpdateThatFailedAfterItsVersion(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: promptConfig(minimalPrompt)},
			{
				PreConfig:   func() { f.FailNext(routePromptUpdate, 500, 1) },
				Config:      promptConfig(secondPrompt),
				ExpectError: regexp.MustCompile(`Cannot\s+update\s+the\s+prompt(.|\n)*500`),
			},
			{
				Config:            promptConfig(secondPrompt),
				ConfigPlanChecks:  expectSameVersion(promptAddr, 2),
				ConfigStateChecks: stateIs(promptAddr, map[string]knownvalue.Check{"title": knownvalue.StringExact("Coding assistant")}),
			},
		},
	})
	f.AssertCalled(routePromptVersion, 1)
	f.AssertCalled(routePromptUpdate, 2)
}

// A name already taken points to an import block rather than adopting the
// prompt.
func TestPromptResourcePointsToAnImportWhenTheNameIsTaken(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.FailNextWith(routePromptCreate, 409, "Prompt coding-assistant already exists", 1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      promptConfig(minimalPrompt),
			ExpectError: regexp.MustCompile(`prompt\s+named\s+"coding-assistant"\s+already\s+exists(.|\n)*import\s+block(.|\n)*409`),
		}},
	})
}

// When the API deduplicates a definition into an earlier version, the latest
// version keeps another definition: the apply fails with the reason instead
// of writing a state that contradicts the plan.
func TestPromptResourceReportsADefinitionDeduplicatedIntoAnEarlierVersion(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.dedupAnyVersion = true

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: promptConfig(minimalPrompt)},
			{Config: promptConfig(secondPrompt)},
			{
				Config:      promptConfig(minimalPrompt),
				ExpectError: regexp.MustCompile(`did\s+not\s+get\s+a\s+new\s+latest\s+version(.|\n)*version\s+1,(.|\n)*stays\s+2`),
			},
		},
	})
	f.AssertCalled(routePromptVersion, 2)
}

// A failed read, update of the notes or delete reaches the user with the
// status of the API.
func TestPromptResourceReportsReadNotesAndDeleteErrors(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: promptConfig(minimalPrompt)},
			{
				PreConfig:   func() { f.FailNextWith(routePromptGet, 401, "Unauthorized", 1) },
				Config:      promptConfig(minimalPrompt),
				ExpectError: regexp.MustCompile(`Cannot\s+read\s+the\s+prompt(.|\n)*401`),
			},
			{
				PreConfig:   func() { f.FailNextWith(routePromptNotes, 403, "Forbidden", 1) },
				Config:      promptConfig(minimalPrompt + `  notes = "n"`),
				ExpectError: regexp.MustCompile(`Cannot\s+update\s+the\s+notes\s+of\s+version\s+1\s+of\s+the\s+prompt(.|\n)*403`),
			},
			{
				PreConfig:   func() { f.FailNext(routePromptDelete, 500, 4) },
				Config:      promptConfig(secondPrompt),
				Destroy:     true,
				ExpectError: regexp.MustCompile(`Cannot\s+delete\s+the\s+prompt(.|\n)*500`),
			},
		},
	})
}

// An invalid configuration fails at plan time, before any call.
func TestPromptResourceValidatesTheConfiguration(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	cases := map[string]struct{ body, err string }{
		"relation with the private scope": {
			`sharing_scope = "private"` + "\n" + `workspace_relation = "reader"`, `Unexpected\s+workspace_relation`,
		},
		"relation without a scope": {
			`workspace_relation = "writer"`, `Unexpected\s+workspace_relation`,
		},
		"unknown scope": {
			`sharing_scope = "organization"`, `value\s+must\s+be\s+one\s+of`,
		},
		"empty variables": {
			`variables = []`, `list\s+must\s+contain\s+at\s+least\s+1`,
		},
		"empty title": {
			`title = ""`, `string\s+length\s+must\s+be\s+at\s+least\s+1`,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
				Steps: []resource.TestStep{{
					Config:      promptConfig(minimalPrompt + c.body),
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(c.err),
				}},
			})
		})
	}
	f.AssertCalled("", 0)
}

// A scope unknown at plan time leaves the relation unknown until the apply,
// which plans it again with the scope known.
func TestPromptResourceAcceptsAScopeUnknownAtPlanTime(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: promptConfig(minimalPrompt) + `
resource "mistral_prompt" "shared" {
  name          = "shared"
  content       = "Shared."
  sharing_scope = mistral_prompt.this.id == "" ? "private" : "workspace"
}
`,
			ConfigStateChecks: stateIs("mistral_prompt.shared", map[string]knownvalue.Check{
				"sharing_scope":      knownvalue.StringExact("workspace"),
				"workspace_relation": knownvalue.StringExact("writer"),
			}),
		}},
	})
}

// Importing a prompt or a skill the API does not have reports it as missing,
// not as a provider error about the resource identity.
func TestImportOfAMissingPromptOrSkill(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: promptConfig(minimalPrompt) + "resource \"mistral_skill\" \"this\" {\n" + minimalSkill + "\n}\n"},
			{
				ResourceName:  promptAddr,
				ImportState:   true,
				ImportStateId: "00000000-0000-4000-8000-999999999999",
				ExpectError:   regexp.MustCompile(`Cannot\s+import\s+non-existent\s+remote\s+object`),
			},
			{
				ResourceName:  skillAddr,
				ImportState:   true,
				ImportStateId: "00000000-0000-4000-8000-999999999999",
				ExpectError:   regexp.MustCompile(`Cannot\s+import\s+non-existent\s+remote\s+object`),
			},
		},
	})
}
