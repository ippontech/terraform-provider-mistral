// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces_test

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

// The acceptance tests need two users of the test organization: the
// Workspace Admin of the workspaces they create, and a user they make a
// member. The Admin API key alone cannot tell which users exist.
const (
	envAdminUserID  = "MISTRAL_ACC_ADMIN_USER_ID"
	envMemberUserID = "MISTRAL_ACC_MEMBER_USER_ID"
)

// preCheckUsers skips t unless both users of the acceptance tests are set.
func preCheckUsers(t *testing.T) {
	t.Helper()
	for _, v := range []string{envAdminUserID, envMemberUserID} {
		if os.Getenv(v) == "" {
			t.Skipf("%s is not set: the workspace acceptance tests are skipped", v)
		}
	}
}

// TestAccWorkspaceResource creates a workspace with a member, renames it and
// changes the member's roles, reads the workspace with both data sources,
// imports both resources, and destroys them, against the live API. Every run
// archives a workspace, which cannot be undone.
func TestAccWorkspaceResource(t *testing.T) {
	name := acctest.RandomName("workspace")
	admin, member := os.Getenv(envAdminUserID), os.Getenv(envMemberUserID)
	config := func(workspaceName, roles string) string {
		return fmt.Sprintf(`
resource "mistral_workspace" "this" {
  name          = %[1]q
  description   = "Terraform acceptance test"
  admin_user_id = %[2]q
}

resource "mistral_workspace_member" "member" {
  workspace_id = mistral_workspace.this.id
  user_id      = %[3]q
  %[4]s
}

data "mistral_workspace" "this" {
  name = mistral_workspace.this.name
}

data "mistral_workspaces" "all" {
  depends_on = [mistral_workspace.this]
}
`, workspaceName, admin, member, roles)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckAdmin(t); preCheckUsers(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config(name, ""),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("mistral_workspace.this", tfjsonpath.New("is_default"), knownvalue.Bool(false)),
					statecheck.ExpectKnownValue("mistral_workspace.this", tfjsonpath.New("icon"), knownvalue.StringExact("")),
					statecheck.ExpectKnownValue("mistral_workspace_member.member", tfjsonpath.New("role_names"),
						knownvalue.SetExact([]knownvalue.Check{knownvalue.StringExact("workspace_contributor")})),
					statecheck.ExpectKnownValue("data.mistral_workspace.this", tfjsonpath.New("description"), knownvalue.StringExact("Terraform acceptance test")),
				},
			},
			{
				// The role names must read back as configured, through the
				// role UUIDs of the user's workspaces.
				Config: config(name+"-renamed", `role_names = ["dev", "observability_viewer"]`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("mistral_workspace.this", plancheck.ResourceActionUpdate),
						plancheck.ExpectResourceAction("mistral_workspace_member.member", plancheck.ResourceActionUpdate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("data.mistral_workspace.this", tfjsonpath.New("name"), knownvalue.StringExact(name+"-renamed")),
				},
			},
			{
				ResourceName:            "mistral_workspace.this",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"admin_user_id"},
			},
			{ResourceName: "mistral_workspace_member.member", ImportState: true, ImportStateVerify: true},
		},
	})
}
