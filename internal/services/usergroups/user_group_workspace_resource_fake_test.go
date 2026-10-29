// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package usergroups_test

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-mistral/internal/providertest"
	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

const assignmentAddr = "mistral_user_group_workspace.production"

// assignmentConfig is a configuration that assigns groupID to workspaceID
// with roles, an HCL list.
func assignmentConfig(groupID, workspaceID, roles string) string {
	return providertest.ProviderConfig() + `
resource "mistral_user_group_workspace" "production" {
  group_id     = "` + groupID + `"
  workspace_id = "` + workspaceID + `"
  role_names   = ` + roles + `
}
`
}

// The group already has access to 100 workspaces: the find walks to the
// second page, where the assignment Terraform adds lands.
func TestUserGroupWorkspaceResourceLifecycle(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	group := f.seedGroup(map[string]any{"name": "Engineering"})
	f.seedAssignments(group, 100, "user")
	roles := func(c knownvalue.Check) statecheck.StateCheck {
		return statecheck.ExpectKnownValue(assignmentAddr, tfjsonpath.New("role_names"), c)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config:           assignmentConfig(group, "ws-prod", `["user"]`),
				ConfigPlanChecks: providertest.ExpectAction(assignmentAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(assignmentAddr, tfjsonpath.New("id"), knownvalue.StringExact(group+":ws-prod")),
					statecheck.ExpectKnownValue(assignmentAddr, tfjsonpath.New("workspace_id"), knownvalue.StringExact("ws-prod")),
					roles(stringSet("user")),
				},
			},
			{
				// The roles update in place, sent in a stable order.
				Config:           assignmentConfig(group, "ws-prod", `["workspace_admin", "dev"]`),
				ConfigPlanChecks: providertest.ExpectAction(assignmentAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					roles(stringSet("dev", "workspace_admin")),
				},
			},
			{
				Config:           assignmentConfig(group, "ws-prod", `["dev"]`),
				ConfigPlanChecks: providertest.ExpectAction(assignmentAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					roles(stringSet("dev")),
				},
			},
			{
				ResourceName:      assignmentAddr,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				ResourceName:    assignmentAddr,
				ImportState:     true,
				ImportStateKind: resource.ImportBlockWithResourceIdentity,
			},
			{
				// Another workspace replaces the assignment.
				Config:           assignmentConfig(group, "ws-staging", `["dev"]`),
				ConfigPlanChecks: providertest.ExpectAction(assignmentAddr, plancheck.ResourceActionReplace),
			},
		},
	})

	f.AssertJSONBodies(routeAssignWorkspace,
		`{"workspace_uuid": "ws-prod", "role_names": ["user"]}`,
		`{"workspace_uuid": "ws-staging", "role_names": ["dev"]}`)
	f.AssertJSONBodies(routeUpdateWorkspace, `{"role_names": ["dev", "workspace_admin"]}`, `{"role_names": ["dev"]}`)
	removed := f.Requests(routeRemoveWorkspace)
	if len(removed) != 2 || removed[0].Path != "/v1/admin/user-groups/"+group+"/workspaces/ws-prod" {
		t.Errorf("DELETE requests = %v", removed)
	}
	f.assertWalkedPage2(t, routeListWorkspaces)
	if n := f.assignments.Len(); n != 100 {
		t.Errorf("%d assignments left, want the 100 seeded", n)
	}
	f.AssertCredential("", sdktest.AdminKey)
}

// What the POST does with a workspace the group already has access to is
// not documented: the create refuses, and names the roles the group has.
func TestUserGroupWorkspaceResourceRefusesToTakeOverAnAssignment(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	group := f.seedGroup(map[string]any{"name": "Engineering"})
	f.seedAssignments(group, 1, "dev")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: assignmentConfig(group, "ws-000", `["user"]`),
			ExpectError: regexp.MustCompile(`User\s+group\s+already\s+assigned\s+to\s+the\s+workspace(.|\n)*Workspace\s+ws-000(.|\n)*\[dev\]` +
				`(.|\n)*import\s+block\s+\(id\s+=\s+"` + group + `:ws-000"\)`),
		}},
	})
	f.AssertCalled(routeAssignWorkspace, 0)
}

// An assignment removed outside Terraform is missing from the list: the plan
// assigns the group again.
func TestUserGroupWorkspaceResourceRecreatesAnAssignmentRemovedOutsideTerraform(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	group := f.seedGroup(map[string]any{"name": "Engineering"})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: assignmentConfig(group, "ws-prod", `["user"]`)},
			{
				PreConfig:        func() { f.assignments.Delete(group + ":ws-prod") },
				Config:           assignmentConfig(group, "ws-prod", `["user"]`),
				ConfigPlanChecks: providertest.ExpectAction(assignmentAddr, plancheck.ResourceActionCreate),
			},
		},
	})
	f.AssertCalled(routeAssignWorkspace, 2)
}

// Roles changed in the Admin Panel plan an update that writes the
// configured roles back.
func TestUserGroupWorkspaceResourceRevertsRolesChangedOutsideTerraform(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	group := f.seedGroup(map[string]any{"name": "Engineering"})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: assignmentConfig(group, "ws-prod", `["user"]`)},
			{
				PreConfig: func() {
					f.assignments.Patch(group+":ws-prod", map[string]any{"roles": []any{map[string]any{"name": "workspace_admin", "uuid": "role-workspace_admin"}}})
				},
				Config:           assignmentConfig(group, "ws-prod", `["user"]`),
				ConfigPlanChecks: providertest.ExpectAction(assignmentAddr, plancheck.ResourceActionUpdate),
			},
		},
	})
	f.AssertJSONBodies(routeUpdateWorkspace, `{"role_names": ["user"]}`)
}

// Neither the assignment nor the role update is replayed after a 5xx.
func TestUserGroupWorkspaceResourceWritesAreSentOnceOnA5xx(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	group := f.seedGroup(map[string]any{"name": "Engineering"})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				PreConfig:   func() { f.FailNext(routeAssignWorkspace, 500, 1) },
				Config:      assignmentConfig(group, "ws-prod", `["user"]`),
				ExpectError: regexp.MustCompile(`Cannot\s+assign\s+the\s+user\s+group\s+to\s+the\s+workspace(.|\n)*500`),
			},
			{Config: assignmentConfig(group, "ws-prod", `["user"]`)},
			{
				PreConfig:   func() { f.FailNext(routeUpdateWorkspace, 503, 1) },
				Config:      assignmentConfig(group, "ws-prod", `["dev"]`),
				ExpectError: regexp.MustCompile(`Cannot\s+update\s+the\s+roles\s+of\s+the\s+user\s+group(.|\n)*503`),
			},
		},
	})
	f.AssertCalled(routeAssignWorkspace, 2)
	f.AssertCalled(routeUpdateWorkspace, 1)
}

// List errors on create and on refresh, and remove errors on destroy, reach
// the user. A group deleted outside Terraform takes its assignment out of
// the state.
func TestUserGroupWorkspaceResourceReportsErrors(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	group := f.seedGroup(map[string]any{"name": "Engineering"})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				PreConfig:   func() { f.FailNextWith(routeListWorkspaces, 403, "Forbidden", 1) },
				Config:      assignmentConfig(group, "ws-prod", `["user"]`),
				ExpectError: regexp.MustCompile(`Cannot\s+create\s+the\s+user\s+group\s+workspace\s+assignment(.|\n)*403`),
			},
			{Config: assignmentConfig(group, "ws-prod", `["user"]`)},
			{
				PreConfig:   func() { f.FailNext(routeListWorkspaces, 500, 4) },
				Config:      assignmentConfig(group, "ws-prod", `["user"]`),
				ExpectError: regexp.MustCompile(`Cannot\s+read\s+the\s+user\s+group\s+workspace\s+assignment(.|\n)*500`),
			},
			{
				PreConfig:   func() { f.FailNext(routeRemoveWorkspace, 500, 4) },
				Config:      assignmentConfig(group, "ws-prod", `["user"]`),
				Destroy:     true,
				ExpectError: regexp.MustCompile(`Cannot\s+remove\s+the\s+user\s+group\s+from\s+the\s+workspace(.|\n)*500`),
			},
			{
				// The group is gone: the list answers 404, and the
				// assignment leaves the state.
				PreConfig:          func() { f.groups.Delete(group) },
				Config:             assignmentConfig(group, "ws-prod", `["user"]`),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// A role outside the role_names of the specification, a role UUID included,
// and an ID that would break the import ID fail at plan time.
func TestUserGroupWorkspaceResourceValidatesTheArguments(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	cases := map[string]struct{ group, workspace, roles, err string }{
		"no role":       {"grp-1", "ws-1", `[]`, `set\s+must\s+contain\s+at\s+least\s+1`},
		"unknown role":  {"grp-1", "ws-1", `["owner"]`, `value\s+must\s+be\s+one\s+of`},
		"role by UUID":  {"grp-1", "ws-1", `["d7ea77c5-9260-41d0-ab26-52b5add3ee56"]`, `value\s+must\s+be\s+one\s+of`},
		"colon in ID":   {"grp-1", "ws:1", `["user"]`, `must\s+not\s+be\s+empty\s+or\s+contain\s+a\s+colon`},
		"empty groupID": {"", "ws-1", `["user"]`, `must\s+not\s+be\s+empty\s+or\s+contain\s+a\s+colon`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
				Steps: []resource.TestStep{{
					Config: assignmentConfig(c.group, c.workspace, c.roles), PlanOnly: true, ExpectError: regexp.MustCompile(c.err),
				}},
			})
		})
	}
	f.AssertCalled("", 0)
}

// An import ID without its workspace part fails.
func TestUserGroupWorkspaceResourceRejectsAMalformedImportID(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	group := f.seedGroup(map[string]any{"name": "Engineering"})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: assignmentConfig(group, "ws-prod", `["user"]`)},
			{
				ResourceName:  assignmentAddr,
				ImportState:   true,
				ImportStateId: "ws-prod",
				ExpectError:   regexp.MustCompile(`Invalid\s+import\s+ID(.|\n)*<group_id>:<workspace_id>`),
			},
		},
	})
}

// An import of a workspace the group has no access to, or of a group that
// does not exist, fails with Terraform's own error, which needs the identity
// set before Read removes the resource.
func TestUserGroupWorkspaceResourceImportOfAMissingAssignmentFails(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	group := f.seedGroup(map[string]any{"name": "Engineering"})

	for _, id := range []string{group + ":ws-missing", "grp-missing:ws-prod"} {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
			Steps: []resource.TestStep{
				{Config: assignmentConfig(group, "ws-prod", `["user"]`)},
				{
					ResourceName:  assignmentAddr,
					ImportState:   true,
					ImportStateId: id,
					ExpectError:   regexp.MustCompile(`Cannot\s+import\s+non-existent\s+remote\s+object`),
				},
			},
		})
	}
}
