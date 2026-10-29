// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package users_test

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
)

// An accepted invite stays in the state with nothing to change, as PR #330
// of ippontech/terraform-provider-anthropic learnt: removing it would make
// the next apply invite a member. Destroying it leaves the member in the
// organization and calls nothing.
func TestInviteResourceKeepsAnAcceptedInvite(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	var id, memberID string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config: inviteConfig(minimalInvite),
				Check: func(s *terraform.State) error {
					id = s.RootModule().Resources[inviteAddr].Primary.ID
					return nil
				},
			},
			{
				PreConfig:        func() { memberID = f.accept("grace@example.com") },
				Config:           inviteConfig(minimalInvite),
				ConfigPlanChecks: providertest.ExpectAction(inviteAddr, plancheck.ResourceActionNoop),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(inviteAddr, tfjsonpath.New("status"), knownvalue.StringExact("accepted")),
					statecheck.ExpectKnownValue(inviteAddr, tfjsonpath.New("created_at"), knownvalue.StringExact(at(1))),
					statecheck.ExpectKnownValue(inviteAddr, tfjsonpath.New("role_names"), knownvalue.SetExact([]knownvalue.Check{knownvalue.StringExact("member")})),
				},
				Check: func(s *terraform.State) error {
					if got := s.RootModule().Resources[inviteAddr].Primary.ID; got != id {
						t.Errorf("id = %s after the acceptance, want %s", got, id)
					}
					return nil
				},
			},
			{
				Config:           providertest.ProviderConfig(),
				ConfigPlanChecks: providertest.ExpectAction(inviteAddr, plancheck.ResourceActionDestroy),
			},
		},
	})
	f.AssertCalled(routeDeleteInvite, 0)
	f.AssertCalled(routeInvite, 1)
	if _, ok := f.users.Get(memberID); !ok {
		t.Error("the destroy of an accepted invite removed the member")
	}
}

// An accepted invite has done its job: changing it plans a replacement,
// whose create refuses the address of a member, as the page says.
func TestInviteResourceCannotReplaceAnAcceptedInvite(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: inviteConfig(minimalInvite)},
			{
				PreConfig:   func() { f.accept("grace@example.com") },
				Config:      inviteConfig(fullInvite),
				ExpectError: regexp.MustCompile(`Already\s+a\s+member`),
			},
		},
	})
	f.AssertCalled(routeInvite, 1)
	f.AssertCalled(routeDeleteInvite, 0)
}

// A member who leaves the organization takes the accepted invite with them:
// the next plan invites the address again, in plain sight.
func TestInviteResourceInvitesAgainAMemberWhoLeft(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	var memberID string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: inviteConfig(minimalInvite)},
			{
				PreConfig: func() { memberID = f.accept("grace@example.com") },
				Config:    inviteConfig(minimalInvite),
			},
			{
				PreConfig:        func() { f.users.Delete(memberID) },
				Config:           inviteConfig(minimalInvite),
				ConfigPlanChecks: providertest.ExpectAction(inviteAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(inviteAddr, tfjsonpath.New("status"), knownvalue.StringExact("pending")),
				},
			},
		},
	})
	f.AssertCalled(routeInvite, 2)
	f.AssertCalled(routeDeleteInvite, 1)
}

// An expired invite stays in the state, marked expired, with nothing to
// change; destroying it revokes it, which clears it from the Invited Users
// of the Admin Panel.
func TestInviteResourceKeepsAnExpiredInvite(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	var id string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config: inviteConfig(minimalInvite),
				Check: func(s *terraform.State) error {
					id = s.RootModule().Resources[inviteAddr].Primary.ID
					return nil
				},
			},
			{
				PreConfig:        func() { f.expire(id) },
				Config:           inviteConfig(minimalInvite),
				ConfigPlanChecks: providertest.ExpectAction(inviteAddr, plancheck.ResourceActionNoop),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(inviteAddr, tfjsonpath.New("status"), knownvalue.StringExact("expired")),
				},
			},
		},
	})
	f.AssertCalled(routeDeleteInvite, 1)
	if f.invites.Len() != 0 {
		t.Error("the destroy of an expired invite did not revoke it")
	}
}

// An expired invite for the address does not block a create: the new
// invite is a different one, and the expired one is left alone.
func TestInviteResourceInvitesAgainAfterAnExpiredInvite(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	expired := f.seedInvite("grace@example.com", []string{fakeRoleIDs["member"]}, true)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:           inviteConfig(minimalInvite),
			ConfigPlanChecks: providertest.ExpectAction(inviteAddr, plancheck.ResourceActionCreate),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(inviteAddr, tfjsonpath.New("status"), knownvalue.StringExact("pending")),
				statecheck.ExpectKnownValue(inviteAddr, tfjsonpath.New("created_at"), knownvalue.StringExact(at(2))),
			},
			Check: func(s *terraform.State) error {
				if s.RootModule().Resources[inviteAddr].Primary.ID == expired {
					t.Error("the create took over the expired invite")
				}
				return nil
			},
		}},
	})
	if _, ok := f.invites.Get(expired); !ok {
		t.Error("the expired invite was revoked")
	}
}
