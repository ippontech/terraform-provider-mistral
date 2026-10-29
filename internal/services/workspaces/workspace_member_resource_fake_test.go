// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces_test

import (
	"fmt"
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

const memberAddr = "mistral_workspace_member.ada"

// memberConfig is a workspace administered by admin, with user as a member
// whose other arguments are body.
func memberConfig(admin, user, body string) string {
	return workspaceConfig(minimalWorkspace(admin)) + fmt.Sprintf(`
resource "mistral_workspace_member" "ada" {
  workspace_id = mistral_workspace.this.id
  user_id      = %q
%s
}
`, user, body)
}

// memberKey reads the ID of mistral_workspace_member.ada, the key of the
// fake's members, into key.
func memberKey(key *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		*key = s.RootModule().Resources[memberAddr].Primary.ID
		return nil
	}
}

// roleSet checks a set of role names exactly.
func roleSet(names ...string) knownvalue.Check {
	checks := make([]knownvalue.Check, 0, len(names))
	for _, n := range names {
		checks = append(checks, knownvalue.StringExact(n))
	}
	return knownvalue.SetExact(checks)
}

// TestWorkspaceMemberResourceLifecycle adds a member with the default role,
// changes its roles, goes back to the default by removing role_names, and
// imports the membership by ID and by identity.
func TestWorkspaceMemberResourceLifecycle(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	admin, ada := f.addUser("admin@example.com"), f.addUser("ada@example.com")
	var workspaceID string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config:           memberConfig(admin, ada, ""),
				ConfigPlanChecks: providertest.ExpectAction(memberAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(memberAddr, tfjsonpath.New("role_names"), roleSet("workspace_contributor")),
					statecheck.ExpectKnownValue(memberAddr, tfjsonpath.New("user_id"), knownvalue.StringExact(ada)),
					statecheck.ExpectKnownValue(memberAddr, tfjsonpath.New("id"), knownvalue.StringRegexp(regexp.MustCompile(`^[0-9a-f-]{36}:`+ada+`$`))),
				},
				Check: readWorkspaceID(&workspaceID),
			},
			{
				Config:           memberConfig(admin, ada, `  role_names = ["observability_viewer", "dev"]`),
				ConfigPlanChecks: providertest.ExpectAction(memberAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(memberAddr, tfjsonpath.New("role_names"), roleSet("dev", "observability_viewer")),
				},
			},
			{
				// Removing role_names sets the default role again.
				Config:           memberConfig(admin, ada, ""),
				ConfigPlanChecks: providertest.ExpectAction(memberAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(memberAddr, tfjsonpath.New("role_names"), roleSet("workspace_contributor")),
				},
			},
			{
				ResourceName:      memberAddr,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				ResourceName:    memberAddr,
				ImportState:     true,
				ImportStateKind: resource.ImportBlockWithResourceIdentity,
			},
		},
	})

	member := func(roles string) string {
		return fmt.Sprintf(`{"members": [{"user_uuid": %q, "role_names": %s}]}`, ada, roles)
	}
	f.AssertJSONBodies(routePutMembers, member(`["workspace_contributor"]`), member(`["dev", "observability_viewer"]`), member(`["workspace_contributor"]`))
	f.AssertJSONBodies(routeRemoveMembers, fmt.Sprintf(`{"members": [{"user_uuid": %q}]}`, ada))
	if got := f.Requests(routePutMembers)[0].Path; got != "/v1/admin/workspaces/"+workspaceID+"/users" {
		t.Errorf("PATCH path = %s", got)
	}
	if got := f.Requests(routeGetUser)[0].Path; got != "/v1/admin/users/"+ada {
		t.Errorf("GET path = %s", got)
	}
	f.AssertCredential("", sdktest.AdminKey)
}

// The PATCH is an upsert: a create must not take over the membership of
// the admin_user_id, which the workspace create made.
func TestWorkspaceMemberResourceRefusesToTakeOverAMembership(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	admin := f.addUser("admin@example.com")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      memberConfig(admin, admin, ""),
			ExpectError: regexp.MustCompile(`Workspace\s+member\s+already\s+exists(.|\n)*workspace_admin(.|\n)*import\s+block\s+\(id\s+=\s+"[0-9a-f-]{36}:` + admin),
		}},
	})
	f.AssertCalled(routePutMembers, 0)
}

// The membership of the admin_user_id comes under Terraform with an import
// block, and plans no change when the configuration names its role.
func TestWorkspaceMemberResourceImportsTheAdminMembership(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	admin := f.addUser("admin@example.com")
	workspaceID := f.seed("ML", false)
	f.setRoles(workspaceID, admin, "workspace_admin")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig() + fmt.Sprintf(`
import {
  to = mistral_workspace_member.ada
  id = "%[1]s:%[2]s"
}

resource "mistral_workspace_member" "ada" {
  workspace_id = %[1]q
  user_id      = %[2]q
  role_names   = ["workspace_admin"]
}
`, workspaceID, admin),
			ConfigPlanChecks: providertest.ExpectAction(memberAddr, plancheck.ResourceActionNoop),
		}},
	})
	f.AssertCalled(routePutMembers, 0)
}

// A membership removed outside Terraform plans a create.
func TestWorkspaceMemberResourceRecreatesAMembershipRemovedOutsideTerraform(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	admin, ada := f.addUser("admin@example.com"), f.addUser("ada@example.com")
	var key string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: memberConfig(admin, ada, ""), Check: memberKey(&key)},
			{
				PreConfig:        func() { f.members.Delete(key) },
				Config:           memberConfig(admin, ada, ""),
				ConfigPlanChecks: providertest.ExpectAction(memberAddr, plancheck.ResourceActionCreate),
			},
		},
	})
	f.AssertCalled(routePutMembers, 2)
}

// A user removed from the organization is no longer a member of anything:
// GET /v1/admin/users/{user_id} answers 404, and the membership leaves the
// state.
func TestWorkspaceMemberResourceFollowsAUserRemovedFromTheOrganization(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	admin, ada := f.addUser("admin@example.com"), f.addUser("ada@example.com")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: memberConfig(admin, ada, "")},
			{
				PreConfig:          func() { f.users.Delete(ada) },
				Config:             memberConfig(admin, ada, ""),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// Roles changed in the Admin Panel show as drift, by name, or as the raw
// value the roles list does not name (the legacy code A here), and the apply
// sets the configured roles back.
func TestWorkspaceMemberResourceSetsBackRolesChangedOutsideTerraform(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	admin, ada := f.addUser("admin@example.com"), f.addUser("ada@example.com")
	var key, workspaceID string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config: memberConfig(admin, ada, `  role_names = ["dev"]`),
				Check:  resource.ComposeTestCheckFunc(memberKey(&key), readWorkspaceID(&workspaceID)),
			},
			{
				PreConfig: func() { f.setRoles(workspaceID, ada, "workspace_admin", "dev") },
				Config:    memberConfig(admin, ada, `  role_names = ["dev"]`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(memberAddr, plancheck.ResourceActionUpdate),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
			{
				RefreshState: true,
				PreConfig:    func() { f.setRawRoles(workspaceID, ada, "A") },
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr(memberAddr, "role_names.#", "1"),
					resource.TestCheckTypeSetElemAttr(memberAddr, "role_names.*", "A"),
				),
				ExpectNonEmptyPlan: true,
			},
			{
				Config:           memberConfig(admin, ada, `  role_names = ["dev"]`),
				ConfigPlanChecks: providertest.ExpectAction(memberAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(memberAddr, tfjsonpath.New("role_names"), roleSet("dev")),
				},
			},
		},
	})
	f.AssertCalled(routePutMembers, 3)
}

// The PATCH is an upsert the documentation calls idempotent: a 5xx is
// replayed, and the create succeeds.
func TestWorkspaceMemberResourceRetriesTheUpsert(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	admin, ada := f.addUser("admin@example.com"), f.addUser("ada@example.com")
	f.FailNext(routePutMembers, 503, 1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:           memberConfig(admin, ada, ""),
			ConfigPlanChecks: providertest.ExpectAction(memberAddr, plancheck.ResourceActionCreate),
		}},
	})
	f.AssertCalled(routePutMembers, 2)
}

// Errors of the check, the upsert, the reads and the removal reach the user,
// and so does a removal that leaves the user in place.
func TestWorkspaceMemberResourceReportsErrors(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	admin, ada := f.addUser("admin@example.com"), f.addUser("ada@example.com")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				// The workspace is created; the check before the member create fails.
				PreConfig:   func() { f.FailNextWith(routeGetUser, 403, "Forbidden", 1) },
				Config:      memberConfig(admin, ada, ""),
				ExpectError: regexp.MustCompile(`Cannot\s+create\s+the\s+workspace\s+member(.|\n)*403`),
			},
			{
				Config:      memberConfig(admin, ada, `  role_names = ["owner"]`),
				ExpectError: regexp.MustCompile(`Cannot\s+set\s+the\s+workspace\s+member(.|\n)*422(.|\n)*Invalid\s+role\s+name`),
			},
			{Config: memberConfig(admin, ada, "")},
			{
				PreConfig:   func() { f.FailNext(routeGetUser, 500, 4) },
				Config:      memberConfig(admin, ada, ""),
				ExpectError: regexp.MustCompile(`Cannot\s+read\s+the\s+workspace\s+member(.|\n)*500`),
			},
			{
				PreConfig:   func() { f.FailNextWith(routeListRoles, 403, "RBAC is not enabled", 1) },
				Config:      memberConfig(admin, ada, ""),
				ExpectError: regexp.MustCompile(`Cannot\s+read\s+the\s+workspace\s+member(.|\n)*RBAC\s+is\s+not\s+enabled`),
			},
			{
				PreConfig:   func() { f.FailNext(routeRemoveMembers, 500, 4) },
				Config:      workspaceConfig(minimalWorkspace(admin)),
				ExpectError: regexp.MustCompile(`Cannot\s+remove\s+the\s+workspace\s+member(.|\n)*500`),
			},
			{
				// The API answers 200 but leaves the user in the workspace.
				PreConfig:   func() { f.keep(ada) },
				Config:      workspaceConfig(minimalWorkspace(admin)),
				ExpectError: regexp.MustCompile(`Cannot\s+remove\s+the\s+workspace\s+member(.|\n)*did\s+not\s+remove\s+the\s+user`),
			},
		},
	})
}

// A removal that answers 404, as for a workspace archived meanwhile, is a
// removal done.
func TestWorkspaceMemberResourceDeleteTakesA404AsDone(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	admin, ada := f.addUser("admin@example.com"), f.addUser("ada@example.com")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: memberConfig(admin, ada, "")},
			{
				PreConfig:        func() { f.FailNextWith(routeRemoveMembers, 404, "Workspace not found", 1) },
				Config:           workspaceConfig(minimalWorkspace(admin)),
				ConfigPlanChecks: providertest.ExpectAction(memberAddr, plancheck.ResourceActionDestroy),
			},
		},
	})
	f.AssertCalled(routeRemoveMembers, 1)
}

// An import ID must be two non-empty parts joined by one colon.
func TestWorkspaceMemberResourceRejectsAMalformedImportID(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	admin, ada := f.addUser("admin@example.com"), f.addUser("ada@example.com")

	for _, id := range []string{"ws-only", ":" + ada, "ws:", "ws:" + ada + ":extra"} {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
			Steps: []resource.TestStep{
				{Config: memberConfig(admin, ada, "")},
				{
					ResourceName:  memberAddr,
					ImportState:   true,
					ImportStateId: id,
					ExpectError:   regexp.MustCompile(`Invalid\s+import\s+ID(.|\n)*<workspace_id>:<user_id>`),
				},
			},
		})
	}
}

// Invalid arguments fail at plan time, before any call.
func TestWorkspaceMemberResourceValidatesItsArguments(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	cases := map[string]struct{ body, err string }{
		"empty roles":    {`  user_id = "u"` + "\n" + `  role_names = []`, `set\s+must\s+contain\s+at\s+least\s+1`},
		"empty role":     {`  user_id = "u"` + "\n" + `  role_names = [""]`, `length\s+must\s+be\s+at\s+least\s+1`},
		"colon in user":  {`  user_id = "a:b"`, `must\s+not\s+be\s+empty\s+or\s+contain\s+a\s+colon`},
		"empty user":     {`  user_id = ""`, `must\s+not\s+be\s+empty\s+or\s+contain\s+a\s+colon`},
		"missing user":   {``, `"user_id"\s+is\s+required`},
		"colon in space": {`  user_id = "u"` + "\n" + `  workspace_id = "a:b"`, `must\s+not\s+be\s+empty\s+or\s+contain\s+a\s+colon`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			workspace := `  workspace_id = "ws"` + "\n"
			if regexp.MustCompile(`workspace_id`).MatchString(c.body) {
				workspace = ""
			}
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
				Steps: []resource.TestStep{{
					Config:      providertest.ProviderConfig() + "resource \"mistral_workspace_member\" \"ada\" {\n" + workspace + c.body + "\n}\n",
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(c.err),
				}},
			})
		})
	}
	f.AssertCalled("", 0)
}

// Importing a membership that does not exist fails as Terraform words it,
// whether the user is unknown or not a member of the workspace.
func TestWorkspaceMemberResourceImportOfAMissingMembership(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	ada := f.addUser("ada@example.com")
	workspaceID := f.seed("ML", false)

	for _, user := range []string{ada, sdktest.NewUUID()} {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
			Steps: []resource.TestStep{{
				Config: providertest.ProviderConfig() + fmt.Sprintf(
					"resource \"mistral_workspace_member\" \"ada\" {\n  workspace_id = %q\n  user_id      = %q\n}\n", workspaceID, user),
				ResourceName:  memberAddr,
				ImportState:   true,
				ImportStateId: workspaceID + ":" + user,
				ExpectError:   regexp.MustCompile(`Cannot\s+import\s+non-existent\s+remote\s+object`),
			}},
		})
	}
	f.AssertCalled(routePutMembers, 0)
}
