// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package usergroups_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-mistral/internal/acctest"
)

// accFixture returns the ID of an object of the test organization that this
// area cannot create, from the environment variable name, and skips t
// without it. The variables are those of the workspaces and API keys tests:
// MISTRAL_ACC_MEMBER_USER_ID, a member of the organization that is not the
// owner of the keys, and MISTRAL_ACC_WORKSPACE_ID, an existing workspace.
func accFixture(t *testing.T, name string) string {
	t.Helper()
	v := os.Getenv(name)
	if v == "" {
		t.Skip(name + " is not set: this acceptance test needs an existing object of the test organization")
	}
	return v
}

// TestAccUserGroupResource creates a parent group and a child group, gives
// the child a description, a parent and a role, then clears them, reads the
// parent by name, imports the child, and destroys both, against the live
// API.
func TestAccUserGroupResource(t *testing.T) {
	name := acctest.RandomName("group")
	config := func(body string) string {
		return fmt.Sprintf(`
resource "mistral_user_group" "parent" {
  name = "%[1]s-parent"
}

resource "mistral_user_group" "child" {
  name = "%[1]s-child"
%[2]s
}

data "mistral_user_group" "parent" {
  name = mistral_user_group.parent.name
}
`, name, body)
	}
	child := "mistral_user_group.child"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckAdmin(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config(""),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(child, tfjsonpath.New("description"), knownvalue.Null()),
					statecheck.ExpectKnownValue(child, tfjsonpath.New("externally_managed"), knownvalue.Bool(false)),
					statecheck.ExpectKnownValue("data.mistral_user_group.parent", tfjsonpath.New("name"), knownvalue.StringExact(name+"-parent")),
				},
			},
			{
				Config: config(`
  description       = "Acceptance test group"
  parent_group_ids  = [mistral_user_group.parent.id]
  organization_role = "member"
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(child, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(child, tfjsonpath.New("organization_role"), knownvalue.StringExact("member")),
					statecheck.ExpectKnownValue(child, tfjsonpath.New("parent_group_ids"), knownvalue.SetSizeExact(1)),
				},
			},
			{
				// Clearing the description with "" and the parents with []
				// must clear them, not keep them.
				Config: config(`  organization_role = "member"`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(child, tfjsonpath.New("description"), knownvalue.Null()),
					statecheck.ExpectKnownValue(child, tfjsonpath.New("parent_group_ids"), knownvalue.Null()),
				},
			},
			{ResourceName: child, ImportState: true, ImportStateVerify: true},
		},
	})
}

// TestAccUserGroupMemberResource adds the user of MISTRAL_ACC_MEMBER_USER_ID to a
// new group, imports the membership, and removes it, against the live API.
func TestAccUserGroupMemberResource(t *testing.T) {
	acctest.SkipUnlessAcc(t)
	userID := accFixture(t, "MISTRAL_ACC_MEMBER_USER_ID")
	config := fmt.Sprintf(`
resource "mistral_user_group" "this" {
  name = %q
}

resource "mistral_user_group_member" "this" {
  group_id = mistral_user_group.this.id
  user_id  = %q
}
`, acctest.RandomName("members"), userID)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckAdmin(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("mistral_user_group_member.this", tfjsonpath.New("user_id"), knownvalue.StringExact(userID)),
				},
			},
			{ResourceName: "mistral_user_group_member.this", ImportState: true, ImportStateVerify: true},
		},
	})
}

// TestAccUserGroupWorkspaceResource gives a new group access to the
// workspace of MISTRAL_ACC_WORKSPACE_ID, changes its roles in place,
// imports the assignment, and removes it, against the live API.
func TestAccUserGroupWorkspaceResource(t *testing.T) {
	acctest.SkipUnlessAcc(t)
	workspaceID := accFixture(t, "MISTRAL_ACC_WORKSPACE_ID")
	name := acctest.RandomName("access")
	config := func(roles string) string {
		return fmt.Sprintf(`
resource "mistral_user_group" "this" {
  name = %q
}

resource "mistral_user_group_workspace" "this" {
  group_id     = mistral_user_group.this.id
  workspace_id = %q
  role_names   = %s
}
`, name, workspaceID, roles)
	}
	addr := "mistral_user_group_workspace.this"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckAdmin(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config(`["user"]`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(addr, tfjsonpath.New("role_names"), stringSet("user")),
				},
			},
			{
				// The roles read back by name, as role_names sends them.
				Config: config(`["user", "dev"]`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(addr, tfjsonpath.New("role_names"), stringSet("dev", "user")),
				},
			},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true},
		},
	})
}
