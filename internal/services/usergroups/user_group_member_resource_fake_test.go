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
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-mistral/internal/providertest"
	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

const memberAddr = "mistral_user_group_member.ada"

// memberConfig is a configuration with the membership of userID in groupID.
func memberConfig(groupID, userID string) string {
	return providertest.ProviderConfig() + `
resource "mistral_user_group_member" "ada" {
  group_id = "` + groupID + `"
  user_id  = "` + userID + `"
}
`
}

// The group already holds 120 members: the find walks to the second page,
// where the member Terraform adds lands.
func TestUserGroupMemberResourceLifecycle(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	group := f.seedGroup(map[string]any{"name": "Engineering"})
	f.seedMembers(group, 120)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config:           memberConfig(group, "usr-ada"),
				ConfigPlanChecks: providertest.ExpectAction(memberAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(memberAddr, tfjsonpath.New("id"), knownvalue.StringExact(group+":usr-ada")),
					statecheck.ExpectKnownValue(memberAddr, tfjsonpath.New("group_id"), knownvalue.StringExact(group)),
					statecheck.ExpectKnownValue(memberAddr, tfjsonpath.New("user_id"), knownvalue.StringExact("usr-ada")),
				},
			},
			{
				// Another user replaces the member: there is no update.
				Config:           memberConfig(group, "usr-alan"),
				ConfigPlanChecks: providertest.ExpectAction(memberAddr, plancheck.ResourceActionReplace),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(memberAddr, tfjsonpath.New("id"), knownvalue.StringExact(group+":usr-alan")),
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

	f.AssertJSONBodies(routeAddMembers, `{"user_uuids": ["usr-ada"]}`, `{"user_uuids": ["usr-alan"]}`)
	f.AssertJSONBodies(routeRemoveMembers, `{"user_uuids": ["usr-ada"]}`, `{"user_uuids": ["usr-alan"]}`)
	if got := f.Requests(routeAddMembers)[0].Path; got != "/v1/admin/user-groups/"+group+"/members" {
		t.Errorf("POST path = %s", got)
	}
	f.assertWalkedPage2(t, routeListMembers)
	for _, r := range f.Requests(routeListMembers) {
		if r.Query.Get("page_size") != "100" {
			t.Errorf("list query = %s, want page_size=100", r.Query.Encode())
		}
	}
	// The members seeded outside Terraform are still there.
	if n := f.members.Len(); n != 120 {
		t.Errorf("%d members left, want the 120 seeded", n)
	}
	f.AssertCredential("", sdktest.AdminKey)
}

// Adding a user who is already in the group would succeed silently: the
// create refuses instead, so a destroy never removes a member someone else
// added.
func TestUserGroupMemberResourceRefusesToTakeOverAMember(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	group := f.seedGroup(map[string]any{"name": "Engineering"})
	f.seedMembers(group, 1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      memberConfig(group, "usr-000"),
			ExpectError: regexp.MustCompile(`User\s+already\s+in\s+the\s+group(.|\n)*import\s+block\s+\(id\s+=\s+"` + group + `:usr-000"\)`),
		}},
	})
	f.AssertCalled(routeAddMembers, 0)
}

// A user removed from the group outside Terraform is missing from the list:
// the plan adds the user again.
func TestUserGroupMemberResourceRecreatesAMemberRemovedOutsideTerraform(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	group := f.seedGroup(map[string]any{"name": "Engineering"})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: memberConfig(group, "usr-ada")},
			{
				PreConfig:        func() { f.members.Delete(group + ":usr-ada") },
				Config:           memberConfig(group, "usr-ada"),
				ConfigPlanChecks: providertest.ExpectAction(memberAddr, plancheck.ResourceActionCreate),
			},
		},
	})
	f.AssertCalled(routeAddMembers, 2)
}

// When the group is gone, so is the membership: the list answers 404 and
// Read removes the member from the state.
func TestUserGroupMemberResourceFollowsItsGroupDeletedOutsideTerraform(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	config := providertest.ProviderConfig() + `
resource "mistral_user_group" "engineering" {
  name = "Engineering"
}

resource "mistral_user_group_member" "ada" {
  group_id = mistral_user_group.engineering.id
  user_id  = "usr-ada"
}
`
	var group string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: func(s *terraform.State) error {
					group = s.RootModule().Resources["mistral_user_group.engineering"].Primary.ID
					return nil
				},
			},
			{
				PreConfig:        func() { f.groups.Delete(group) },
				Config:           config,
				ConfigPlanChecks: providertest.ExpectAction(memberAddr, plancheck.ResourceActionCreate),
			},
		},
	})
}

// A create that meets a 5xx is sent once, like every write the API does not
// document as idempotent.
func TestUserGroupMemberResourceCreateIsSentOnceOnA5xx(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	group := f.seedGroup(map[string]any{"name": "Engineering"})
	f.FailNext(routeAddMembers, 502, 1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      memberConfig(group, "usr-ada"),
			ExpectError: regexp.MustCompile(`Cannot\s+add\s+the\s+user\s+to\s+the\s+group(.|\n)*502`),
		}},
	})
	f.AssertCalled(routeAddMembers, 1)
}

// List errors on create and on refresh, and remove errors on destroy, reach
// the user. The remove is retried like any delete.
func TestUserGroupMemberResourceReportsErrors(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	group := f.seedGroup(map[string]any{"name": "Engineering"})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				PreConfig:   func() { f.FailNextWith(routeListMembers, 403, "Forbidden", 1) },
				Config:      memberConfig(group, "usr-ada"),
				ExpectError: regexp.MustCompile(`Cannot\s+create\s+the\s+user\s+group\s+member(.|\n)*403`),
			},
			{Config: memberConfig(group, "usr-ada")},
			{
				PreConfig:   func() { f.FailNext(routeListMembers, 500, 4) },
				Config:      memberConfig(group, "usr-ada"),
				ExpectError: regexp.MustCompile(`Cannot\s+read\s+the\s+user\s+group\s+member(.|\n)*500`),
			},
			{
				PreConfig:   func() { f.FailNext(routeRemoveMembers, 500, 4) },
				Config:      memberConfig(group, "usr-ada"),
				Destroy:     true,
				ExpectError: regexp.MustCompile(`Cannot\s+remove\s+the\s+user\s+from\s+the\s+group(.|\n)*500`),
			},
		},
	})
}

// An import ID needs both parts, joined by one colon.
func TestUserGroupMemberResourceRejectsAMalformedImportID(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	group := f.seedGroup(map[string]any{"name": "Engineering"})

	for _, id := range []string{"usr-ada", group + ":", ":usr-ada", group + ":usr:ada"} {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
			Steps: []resource.TestStep{
				{Config: memberConfig(group, "usr-ada")},
				{
					ResourceName:  memberAddr,
					ImportState:   true,
					ImportStateId: id,
					ExpectError:   regexp.MustCompile(`Invalid\s+import\s+ID(.|\n)*<group_id>:<user_id>`),
				},
			},
		})
	}
}

// An ID that is empty or holds a colon would break the import ID: it fails
// at plan time, before any call.
func TestUserGroupMemberResourceValidatesTheIDs(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	for _, c := range [][2]string{{"", "usr-ada"}, {"grp:1", "usr-ada"}, {"grp-1", ""}} {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
			Steps: []resource.TestStep{{
				Config:      memberConfig(c[0], c[1]),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`must\s+not\s+be\s+empty\s+or\s+contain\s+a\s+colon`),
			}},
		})
	}
	f.AssertCalled("", 0)
}

// An import of a user who is not in the group, or of a group that does not
// exist, fails with Terraform's own error, which needs the identity set
// before Read removes the resource.
func TestUserGroupMemberResourceImportOfAMissingMemberFails(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	group := f.seedGroup(map[string]any{"name": "Engineering"})

	for _, id := range []string{group + ":usr-missing", "grp-missing:usr-ada"} {
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
			Steps: []resource.TestStep{
				{Config: memberConfig(group, "usr-ada")},
				{
					ResourceName:  memberAddr,
					ImportState:   true,
					ImportStateId: id,
					ExpectError:   regexp.MustCompile(`Cannot\s+import\s+non-existent\s+remote\s+object`),
				},
			},
		})
	}
}
