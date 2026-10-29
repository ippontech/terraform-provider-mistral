// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package users_test

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/ippontech/terraform-provider-mistral/internal/providertest"
)

// A create that meets a 5xx is sent once: the invite may exist, and a
// replay would send a second email.
func TestInviteResourceCreateIsSentOnceOnA5xx(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.FailNext(routeInvite, 500, 1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      inviteConfig(minimalInvite),
			ExpectError: regexp.MustCompile(`Cannot\s+create\s+the\s+invite(.|\n)*500`),
		}},
	})
	f.AssertCalled(routeInvite, 1)
}

// No silent adoption: a pending invite sent outside Terraform is refused
// with the import block that brings it under Terraform.
func TestInviteResourceRefusesToTakeOverAPendingInvite(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	id := f.seedInvite("Grace@Example.com", []string{"M"}, false)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      inviteConfig(minimalInvite),
			ExpectError: regexp.MustCompile(`Invite\s+already\s+exists(.|\n)*import\s+block\s+\(id\s+=\s+"` + id + `"\)`),
		}},
	})
	f.AssertCalled(routeInvite, 0)
}

// An address that belongs to a member has no one to invite: the create
// fails before the POST, and names the member.
func TestInviteResourceRefusesToInviteAMember(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	id := f.seedUser("grace@example.com", nil)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      inviteConfig(minimalInvite),
			ExpectError: regexp.MustCompile(`Already\s+a\s+member(.|\n)*user\s+ID\s+"` + id + `"`),
		}},
	})
	f.AssertCalled(routeInvite, 0)
}

// The API answers 200 when it refuses an address.
func TestInviteResourceReportsAnAddressMistralRefuses(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.invalid["grace@example.com"] = true

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      inviteConfig(minimalInvite),
			ExpectError: regexp.MustCompile(`Invalid\s+email(.|\n)*Mistral\s+refused\s+grace@example.com`),
		}},
	})
}

// A create Mistral accepts without listing the invite leaves the provider
// with no ID to track: the apply fails and says where to look.
func TestInviteResourceReportsAnInviteMistralDoesNotList(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.unlisted = true

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      inviteConfig(minimalInvite),
			ExpectError: regexp.MustCompile(`Cannot\s+find\s+the\s+invite(.|\n)*does\s+not\s+list\s+it`),
		}},
	})
}

// Both listings of a create report their failure: the check before the
// POST, and the lookup of the invite after it.
func TestInviteResourceReportsListErrors(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				// The check before the create.
				PreConfig:   func() { f.FailNext(routeListUsers, 500, 4) },
				Config:      inviteConfig(minimalInvite),
				ExpectError: regexp.MustCompile(`Cannot\s+create\s+the\s+invite(.|\n)*500`),
			},
			{
				// The lookup of the invite the create made.
				PreConfig:   func() { f.afterInvite = func() { f.FailNext(routeListUsers, 503, 4) } },
				Config:      inviteConfig(minimalInvite),
				ExpectError: regexp.MustCompile(`Cannot\s+find\s+the\s+invite(.|\n)*listing\s+it\s+failed(.|\n)*503`),
			},
		},
	})
	f.AssertCalled(routeInvite, 1)
}

// A refresh and a destroy report the error of the API; a 401 is not
// retried, a 500 is, up to four calls.
func TestInviteResourceReportsReadAndDeleteErrors(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: inviteConfig(minimalInvite)},
			{
				PreConfig:   func() { f.FailNextWith(routeListUsers, 401, "Invalid API Key", 1) },
				Config:      inviteConfig(minimalInvite),
				ExpectError: regexp.MustCompile(`Cannot\s+read\s+the\s+invite(.|\n)*401(.|\n)*Invalid\s+API\s+Key`),
			},
			{
				PreConfig:   func() { f.FailNext(routeDeleteInvite, 500, 4) },
				Config:      providertest.ProviderConfig(),
				ExpectError: regexp.MustCompile(`Cannot\s+delete\s+the\s+invite(.|\n)*500`),
			},
		},
	})
}

// An import that cannot name the roles of the invite fails rather than
// writing roles the configuration would never match.
func TestInviteResourceReportsARolesErrorOnImport(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	id := f.seedInvite("grace@example.com", []string{fakeRoleIDs["member"]}, false)
	f.FailNext(routeListRoles, 500, 4)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig() + `
import {
  to = mistral_invite.grace
  id = "` + id + `"
}

resource "mistral_invite" "grace" {
  email      = "grace@example.com"
  role_names = ["member"]
}
`,
			ExpectError: regexp.MustCompile(`Cannot\s+read\s+the\s+invite(.|\n)*/v1/admin/roles(.|\n)*500`),
		}},
	})
}

// An invite Mistral does not list cannot be imported. An accepted invite
// is among them: Mistral no longer lists it, and an import knows no address
// to look for a member with.
func TestInviteResourceImportOfAMissingInviteFails(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig() + `
import {
  to = mistral_invite.grace
  id = "00000000-0000-4000-8000-000000000000"
}

resource "mistral_invite" "grace" {
  email      = "grace@example.com"
  role_names = ["member"]
}
`,
			ExpectError: regexp.MustCompile(`Cannot\s+import\s+non-existent\s+remote\s+object`),
		}},
	})
}

// The validators refuse, at plan time and without a call, a list of
// addresses, a role name the API does not take, and empty sets.
func TestInviteResourceValidatesItsArguments(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	cases := map[string]struct{ body, err string }{
		"two addresses separated by a comma": {
			`email = "grace@example.com,ada@example.com"
  role_names = ["member"]`, `must\s+be\s+one\s+email\s+address`,
		},
		"two addresses separated by a new line": {
			`email = "grace@example.com\nada@example.com"
  role_names = ["member"]`, `must\s+be\s+one\s+email\s+address`,
		},
		"no at sign": {
			`email = "grace"
  role_names = ["member"]`, `must\s+be\s+one\s+email\s+address`,
		},
		"an unknown role": {
			`email = "grace@example.com"
  role_names = ["owner"]`, `value\s+must\s+be\s+one\s+of`,
		},
		"no role": {
			`email = "grace@example.com"
  role_names = []`, `set\s+must\s+contain\s+at\s+least\s+1`,
		},
		"no workspace": {
			`email = "grace@example.com"
  role_names = ["member"]
  workspace_ids = []`, `set\s+must\s+contain\s+at\s+least\s+1`,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
				Steps: []resource.TestStep{{
					Config:      inviteConfig(c.body),
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(c.err),
				}},
			})
		})
	}
	f.AssertCalled("", 0)
}
