// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package users_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/compare"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-mistral/internal/acctest"
)

// TestAccInviteResource invites an address of example.com, which receives
// no mail, imports the invite, and revokes it, against the live API. It
// checks the readings of the specification the fake encodes: the invite is
// listed by GET /v1/admin/users?email= with its roles, and its roles name
// back through GET /v1/admin/roles.
func TestAccInviteResource(t *testing.T) {
	email := acctest.RandomName("invite") + "@example.com"
	config := fmt.Sprintf(`
resource "mistral_invite" "this" {
  email      = %q
  role_names = ["member"]
}
`, email)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckAdmin(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("mistral_invite.this", tfjsonpath.New("status"), knownvalue.StringExact("pending")),
					statecheck.ExpectKnownValue("mistral_invite.this", tfjsonpath.New("email"), knownvalue.StringExact(email)),
				},
			},
			{
				ResourceName:      "mistral_invite.this",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

// TestAccUserDataSources lists the members of the organization, then reads
// the first one by email, against the live API.
func TestAccUserDataSources(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckAdmin(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: `
data "mistral_users" "all" {}

data "mistral_user" "first" {
  email = data.mistral_users.all.users[0].email
}
`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.CompareValuePairs(
					"data.mistral_user.first", tfjsonpath.New("id"),
					"data.mistral_users.all", tfjsonpath.New("users").AtSliceIndex(0).AtMapKey("id"),
					compare.ValuesSame()),
			},
		}},
	})
}
