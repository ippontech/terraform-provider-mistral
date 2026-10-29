// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package users_test

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-mistral/internal/providertest"
	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

const usersAddr = "data.mistral_users.all"

var usersConfig = providertest.ProviderConfig() + `
data "mistral_users" "all" {}
`

// 150 members and a pending invite take two pages; the list holds the
// members only, in the order of the API, without the first and last name
// the list does not return.
func TestUsersDataSourceListsEveryMember(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	adaID := f.seedUser("ada@example.com", ada)
	for _, email := range emails(149, "colleague@example.com") {
		f.seedUser(email, nil)
	}
	f.seedInvite("grace@example.com", []string{"M"}, false)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: usersConfig,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(usersAddr, tfjsonpath.New("users"), knownvalue.ListSizeExact(150)),
				statecheck.ExpectKnownValue(usersAddr, tfjsonpath.New("users").AtSliceIndex(0), knownvalue.ObjectExact(map[string]knownvalue.Check{
					"id":                 knownvalue.StringExact(adaID),
					"email":              knownvalue.StringExact("ada@example.com"),
					"name":               knownvalue.StringExact("Ada Lovelace"),
					"organization_roles": knownvalue.ListExact([]knownvalue.Check{knownvalue.StringExact(fakeRoleIDs["organization_admin"])}),
					"workspaces": knownvalue.ListExact([]knownvalue.Check{
						knownvalue.ObjectExact(map[string]knownvalue.Check{
							"workspace_id": knownvalue.StringExact(workspaceDefault), "name": knownvalue.StringExact("Default"),
							"is_default": knownvalue.Bool(true), "roles": knownvalue.ListExact([]knownvalue.Check{knownvalue.StringExact("A")}),
						}),
						knownvalue.ObjectExact(map[string]knownvalue.Check{
							"workspace_id": knownvalue.StringExact(workspaceSupport), "name": knownvalue.StringExact("Support"),
							"is_default": knownvalue.Bool(false), "roles": knownvalue.ListExact([]knownvalue.Check{knownvalue.StringExact("M")}),
						}),
					}),
				})),
				statecheck.ExpectKnownValue(usersAddr, tfjsonpath.New("users").AtSliceIndex(149).AtMapKey("email"),
					knownvalue.StringExact("148.colleague@example.com")),
				statecheck.ExpectKnownValue(usersAddr, tfjsonpath.New("users").AtSliceIndex(149).AtMapKey("workspaces"),
					knownvalue.ListExact([]knownvalue.Check{})),
			},
		}},
	})
	f.assertListQueries(t, "page=1&page_size=100", "page=2&page_size=100")
	f.AssertCredential("", sdktest.AdminKey)
}

// An organization with invites and no member reads an empty list.
func TestUsersDataSourceListsNoMember(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.seedInvite("grace@example.com", []string{"M"}, false)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: usersConfig,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(usersAddr, tfjsonpath.New("users"), knownvalue.ListExact([]knownvalue.Check{})),
			},
		}},
	})
}

// A failed list is reported after the retries of a read.
func TestUsersDataSourceReportsErrors(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.FailNext(routeListUsers, 500, 4)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      usersConfig,
			ExpectError: regexp.MustCompile(`Cannot\s+list\s+the\s+users(.|\n)*500`),
		}},
	})
	f.AssertCalled(routeListUsers, 4)
}
