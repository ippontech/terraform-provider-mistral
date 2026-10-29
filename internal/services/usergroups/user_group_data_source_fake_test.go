// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package usergroups_test

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

const groupDataAddr = "data.mistral_user_group.engineering"

// groupDataConfig is a configuration that reads the group named name.
func groupDataConfig(name string) string {
	return providertest.ProviderConfig() + `
data "mistral_user_group" "engineering" {
  name = "` + name + `"
}
`
}

// A group an identity provider syncs, found by its exact name among 150
// groups the search also matches, on the second page, and assigned to a
// workspace by Terraform: the consumer the data source exists for.
func TestUserGroupDataSourceReadsAGroupByItsExactName(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	for i := range 150 {
		f.seedGroup(map[string]any{"name": fmt.Sprintf("Engineering %03d", i)})
	}
	f.seedGroup(map[string]any{"name": "engineering"})
	parent := f.seedGroup(map[string]any{"name": "Staff"})
	id := f.seedGroup(map[string]any{
		"name": "Engineering", "description": "Synced from Okta", "externally_managed": true,
		"organization_role": "member", "parent_group_ids": []any{parent},
	})
	check := func(attr string, c knownvalue.Check) statecheck.StateCheck {
		return statecheck.ExpectKnownValue(groupDataAddr, tfjsonpath.New(attr), c)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: groupDataConfig("Engineering") + `
resource "mistral_user_group_workspace" "production" {
  group_id     = data.mistral_user_group.engineering.id
  workspace_id = "ws-prod"
  role_names   = ["user"]
}
`,
			ConfigStateChecks: []statecheck.StateCheck{
				check("id", knownvalue.StringExact(id)),
				check("name", knownvalue.StringExact("Engineering")),
				check("description", knownvalue.StringExact("Synced from Okta")),
				check("externally_managed", knownvalue.Bool(true)),
				check("organization_role", knownvalue.StringExact("member")),
				check("parent_group_ids", stringSet(parent)),
				statecheck.ExpectKnownValue(assignmentAddr, tfjsonpath.New("group_id"), knownvalue.StringExact(id)),
			},
		}},
	})

	f.assertWalkedPage2(t, routeListGroups)
	for _, r := range f.Requests(routeListGroups) {
		if q := r.Query.Get("search"); q != "Engineering" {
			t.Errorf("search = %q, want the name", q)
		}
	}
	f.AssertCredential("", sdktest.AdminKey)
}

// A group with no description, no parent and no role reads them as null.
func TestUserGroupDataSourceReadsAMinimalGroup(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.seedGroup(map[string]any{"name": "Interns", "externally_managed": nil})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: groupDataConfig("Interns"),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(groupDataAddr, tfjsonpath.New("description"), knownvalue.Null()),
				statecheck.ExpectKnownValue(groupDataAddr, tfjsonpath.New("parent_group_ids"), knownvalue.Null()),
				statecheck.ExpectKnownValue(groupDataAddr, tfjsonpath.New("organization_role"), knownvalue.Null()),
				statecheck.ExpectKnownValue(groupDataAddr, tfjsonpath.New("externally_managed"), knownvalue.Bool(false)),
			},
		}},
	})
}

// The search matches "Engineering 001", but no group has the exact name;
// an organization with no group at all gives the same error.
func TestUserGroupDataSourceNotFound(t *testing.T) {
	t.Parallel()
	for _, seeded := range []bool{true, false} {
		f := newFake(t)
		if seeded {
			f.seedGroup(map[string]any{"name": "Engineering 001"})
		}
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
			Steps: []resource.TestStep{{
				Config:      groupDataConfig("Engineering"),
				ExpectError: regexp.MustCompile(`User\s+group\s+not\s+found(.|\n)*No\s+user\s+group(.|\n)*"Engineering"`),
			}},
		})
	}
}

// Group names are not documented as unique: two groups with the name fail
// the read, and name both IDs.
func TestUserGroupDataSourceRefusesAnAmbiguousName(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	first := f.seedGroup(map[string]any{"name": "Engineering"})
	second := f.seedGroup(map[string]any{"name": "Engineering"})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      groupDataConfig("Engineering"),
			ExpectError: regexp.MustCompile(`Several\s+user\s+groups\s+have\s+this\s+name(.|\n)*` + first + `,\s+` + second),
		}},
	})
}

// A list that keeps failing is retried like any read, then reported.
func TestUserGroupDataSourceReportsOtherErrors(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.FailNext(routeListGroups, 500, 4)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      groupDataConfig("Engineering"),
			ExpectError: regexp.MustCompile(`Cannot\s+list\s+the\s+user\s+groups(.|\n)*500`),
		}},
	})
	f.AssertCalled(routeListGroups, 4)
}
