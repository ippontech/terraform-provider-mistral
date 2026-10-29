// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package usergroups_test

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

const groupAddr = "mistral_user_group.interns"

// uuidPattern matches the IDs sdktest.NewUUID gives.
var uuidPattern = regexp.MustCompile(`^00000000-0000-4000-8000-\d{12}$`)

// groupConfig is a configuration with the group Interns and the arguments of
// body.
func groupConfig(body string) string {
	return providertest.ProviderConfig() + "resource \"mistral_user_group\" \"interns\" {\n  name = \"Interns\"\n" + body + "\n}\n"
}

// stringSet is the exact check of a set of strings.
func stringSet(values ...string) knownvalue.Check {
	checks := make([]knownvalue.Check, len(values))
	for i, v := range values {
		checks[i] = knownvalue.StringExact(v)
	}
	return knownvalue.SetExact(checks)
}

// The lifecycle: create with the name only, set every argument, clear the
// description and drop a parent, change the role alone, drop the parents,
// then import by ID and by identity.
func TestUserGroupResourceLifecycle(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	parentA := f.seedGroup(map[string]any{"name": "Staff"})
	parentB := f.seedGroup(map[string]any{"name": "Paris"})
	check := func(attr string, c knownvalue.Check) statecheck.StateCheck {
		return statecheck.ExpectKnownValue(groupAddr, tfjsonpath.New(attr), c)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config:           groupConfig(""),
				ConfigPlanChecks: providertest.ExpectAction(groupAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					check("id", knownvalue.StringRegexp(uuidPattern)),
					check("name", knownvalue.StringExact("Interns")),
					check("description", knownvalue.Null()),
					check("parent_group_ids", knownvalue.Null()),
					check("organization_role", knownvalue.Null()),
					check("externally_managed", knownvalue.Bool(false)),
				},
			},
			{
				Config: groupConfig(`
  description       = "Summer interns"
  parent_group_ids  = ["` + parentB + `", "` + parentA + `"]
  organization_role = "billing_manager"
`),
				ConfigPlanChecks: providertest.ExpectAction(groupAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					check("description", knownvalue.StringExact("Summer interns")),
					check("parent_group_ids", stringSet(parentA, parentB)),
					check("organization_role", knownvalue.StringExact("billing_manager")),
				},
			},
			{
				// Removing the description clears it; removing the role
				// leaves it as it is, since the API cannot clear it.
				Config:           groupConfig(`  parent_group_ids = ["` + parentA + `"]`),
				ConfigPlanChecks: providertest.ExpectAction(groupAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					check("description", knownvalue.Null()),
					check("parent_group_ids", stringSet(parentA)),
					check("organization_role", knownvalue.StringExact("billing_manager")),
				},
			},
			{
				// A role change alone calls the role route only.
				Config: groupConfig(`
  parent_group_ids  = ["` + parentA + `"]
  organization_role = "member"
`),
				ConfigPlanChecks: providertest.ExpectAction(groupAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					check("organization_role", knownvalue.StringExact("member")),
				},
			},
			{
				Config:           groupConfig(`  organization_role = "member"`),
				ConfigPlanChecks: providertest.ExpectAction(groupAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					check("parent_group_ids", knownvalue.Null()),
				},
			},
			{
				ResourceName:      groupAddr,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				ResourceName:    groupAddr,
				ImportState:     true,
				ImportStateKind: resource.ImportBlockWithResourceIdentity,
			},
		},
	})

	f.AssertJSONBodies(routeCreateGroup, `{"name": "Interns", "description": null, "parent_group_ids": []}`)
	f.AssertJSONBodies(routeUpdateGroup,
		`{"name": "Interns", "description": "Summer interns", "parent_group_ids": ["`+parentA+`", "`+parentB+`"]}`,
		`{"name": "Interns", "description": "", "parent_group_ids": ["`+parentA+`"]}`,
		`{"name": "Interns", "description": "", "parent_group_ids": []}`)
	f.AssertJSONBodies(routeSetRole, `{"organization_role": "billing_manager"}`, `{"organization_role": "member"}`)
	f.AssertCalled(routeDeleteGroup, 1)
	f.AssertCredential("", sdktest.AdminKey)
}

// A create with every argument sends the group in one call and the role in
// a second one, then reads the group back.
func TestUserGroupResourceCreatesWithEveryArgument(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	parent := f.seedGroup(map[string]any{"name": "Staff"})
	config := groupConfig(`
  description       = "Summer interns"
  parent_group_ids  = ["` + parent + `"]
  organization_role = "organization_admin"
`)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config:           config,
				ConfigPlanChecks: providertest.ExpectAction(groupAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(groupAddr, tfjsonpath.New("description"), knownvalue.StringExact("Summer interns")),
					statecheck.ExpectKnownValue(groupAddr, tfjsonpath.New("parent_group_ids"), stringSet(parent)),
					statecheck.ExpectKnownValue(groupAddr, tfjsonpath.New("organization_role"), knownvalue.StringExact("organization_admin")),
				},
			},
			{ResourceName: groupAddr, ImportState: true, ImportStateVerify: true},
		},
	})
	f.AssertJSONBodies(routeCreateGroup, `{"name": "Interns", "description": "Summer interns", "parent_group_ids": ["`+parent+`"]}`)
	f.AssertJSONBodies(routeSetRole, `{"organization_role": "organization_admin"}`)
}

// A group deleted in the Admin Panel answers 404: Read removes it, and the
// plan creates it again.
func TestUserGroupResourceRecreatesAGroupDeletedOutsideTerraform(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	var id string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config: groupConfig(""),
				Check: func(s *terraform.State) error {
					id = s.RootModule().Resources[groupAddr].Primary.ID
					return nil
				},
			},
			{
				PreConfig:        func() { f.groups.Delete(id) },
				Config:           groupConfig(""),
				ConfigPlanChecks: providertest.ExpectAction(groupAddr, plancheck.ResourceActionCreate),
			},
		},
	})
	f.AssertCalled(routeCreateGroup, 2)
}

// A group renamed in the Admin Panel plans an update that writes the name
// back.
func TestUserGroupResourceRevertsAChangeMadeOutsideTerraform(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	var id string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config: groupConfig(""),
				Check: func(s *terraform.State) error {
					id = s.RootModule().Resources[groupAddr].Primary.ID
					return nil
				},
			},
			{
				PreConfig:        func() { f.groups.Patch(id, map[string]any{"name": "Renamed", "organization_role": "A"}) },
				Config:           groupConfig(""),
				ConfigPlanChecks: providertest.ExpectAction(groupAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(groupAddr, tfjsonpath.New("name"), knownvalue.StringExact("Interns")),
					// The legacy letter reads as its role name.
					statecheck.ExpectKnownValue(groupAddr, tfjsonpath.New("organization_role"), knownvalue.StringExact("organization_admin")),
				},
			},
		},
	})
	f.AssertJSONBodies(routeUpdateGroup, `{"name": "Interns", "description": "", "parent_group_ids": []}`)
}

// A create that meets a 5xx is sent once: the group may exist, and a replay
// would create a second one.
func TestUserGroupResourceCreateIsSentOnceOnA5xx(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.FailNext(routeCreateGroup, 500, 1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      groupConfig(""),
			ExpectError: regexp.MustCompile(`Cannot\s+create\s+the\s+user\s+group(.|\n)*500`),
		}},
	})
	f.AssertCalled(routeCreateGroup, 1)
}

// When the role call fails after the create, the group is saved as tainted:
// the next apply replaces it instead of creating a second one beside it.
func TestUserGroupResourceTaintsAGroupWhoseRoleFailed(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	config := groupConfig(`  organization_role = "member"`)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				PreConfig:   func() { f.FailNextWith(routeSetRole, 422, "Unknown role", 1) },
				Config:      config,
				ExpectError: regexp.MustCompile(`Cannot\s+set\s+the\s+organization\s+role(.|\n)*Unknown role`),
			},
			{
				Config:           config,
				ConfigPlanChecks: providertest.ExpectAction(groupAddr, plancheck.ResourceActionReplace),
				Check: func(*terraform.State) error {
					if n := f.groups.Len(); n != 1 {
						return fmt.Errorf("%d groups at Mistral after the replacement, want 1", n)
					}
					return nil
				},
			},
		},
	})
	f.AssertCalled(routeCreateGroup, 2)
}

// The read that follows the role call can fail on its own: a create has no
// refresh before it, so the injected failures reach that read.
func TestUserGroupResourceReportsAReadErrorAfterTheRole(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.FailNext(routeGetGroup, 500, 4)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      groupConfig(`  organization_role = "member"`),
			ExpectError: regexp.MustCompile(`Cannot\s+read\s+the\s+user\s+group(.|\n)*500`),
		}},
	})
	f.AssertCalled(routeSetRole, 1)
}

// Update, read and delete errors reach the user with the message of the API.
func TestUserGroupResourceReportsErrors(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: groupConfig("")},
			{
				PreConfig:   func() { f.FailNextWith(routeUpdateGroup, 403, "The group is externally managed", 1) },
				Config:      groupConfig(`  description = "changed"`),
				ExpectError: regexp.MustCompile(`Cannot\s+update\s+the\s+user\s+group(.|\n)*externally managed`),
			},
			{
				PreConfig:   func() { f.FailNextWith(routeGetGroup, 401, "Invalid API Key", 1) },
				Config:      groupConfig(""),
				ExpectError: regexp.MustCompile(`Cannot\s+read\s+the\s+user\s+group(.|\n)*401`),
			},
			{
				PreConfig:   func() { f.FailNext(routeDeleteGroup, 500, 4) },
				Config:      groupConfig(""),
				Destroy:     true,
				ExpectError: regexp.MustCompile(`Cannot\s+delete\s+the\s+user\s+group(.|\n)*500`),
			},
		},
	})
}

// Invalid arguments fail at plan time, before any call.
func TestUserGroupResourceValidatesTheArguments(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	cases := map[string]struct{ config, err string }{
		"empty name": {
			providertest.ProviderConfig() + `resource "mistral_user_group" "interns" { name = "" }`, `string\s+length\s+must\s+be\s+between\s+1\s+and\s+255`,
		},
		"name too long": {
			providertest.ProviderConfig() + `resource "mistral_user_group" "interns" { name = "` + strings.Repeat("a", 256) + `" }`, `between\s+1\s+and\s+255`,
		},
		"empty description":  {groupConfig(`  description = ""`), `between\s+1\s+and\s+255`},
		"empty parent list":  {groupConfig(`  parent_group_ids = []`), `set\s+must\s+contain\s+at\s+least\s+1`},
		"empty parent ID":    {groupConfig(`  parent_group_ids = [""]`), `length\s+must\s+be\s+at\s+least\s+1`},
		"unknown role":       {groupConfig(`  organization_role = "owner"`), `value\s+must\s+be\s+one\s+of`},
		"role by its letter": {groupConfig(`  organization_role = "A"`), `value\s+must\s+be\s+one\s+of`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
				Steps:                    []resource.TestStep{{Config: c.config, PlanOnly: true, ExpectError: regexp.MustCompile(c.err)}},
			})
		})
	}
	f.AssertCalled("", 0)
}

// A group an identity provider syncs, imported with an import block, plans
// no change when the configuration matches it.
func TestUserGroupResourceImportsAnExternallyManagedGroup(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	id := f.seedGroup(map[string]any{
		"name": "Engineering", "description": "", "externally_managed": true, "organization_role": "M",
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig() + `
import {
  to = mistral_user_group.engineering
  id = "` + id + `"
}

resource "mistral_user_group" "engineering" {
  name              = "Engineering"
  organization_role = "member"
}
`,
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("mistral_user_group.engineering", plancheck.ResourceActionNoop)},
			},
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("mistral_user_group.engineering", tfjsonpath.New("externally_managed"), knownvalue.Bool(true)),
				statecheck.ExpectKnownValue("mistral_user_group.engineering", tfjsonpath.New("description"), knownvalue.Null()),
				statecheck.ExpectKnownValue("mistral_user_group.engineering", tfjsonpath.New("organization_role"), knownvalue.StringExact("member")),
			},
		}},
	})
	f.AssertCalled(routeUpdateGroup, 0)
	f.AssertCalled(routeSetRole, 0)
}

// An import of a group the API does not have fails with Terraform's own
// error, which needs the identity set before Read removes the resource.
func TestUserGroupResourceImportOfAMissingGroupFails(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: groupConfig("")},
			{
				ResourceName:  groupAddr,
				ImportState:   true,
				ImportStateId: "00000000-0000-4000-8000-999999999999",
				ExpectError:   regexp.MustCompile(`Cannot\s+import\s+non-existent\s+remote\s+object`),
			},
		},
	})
}
