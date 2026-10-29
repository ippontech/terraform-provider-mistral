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

const userAddr = "data.mistral_user.ada"

// userConfig is the provider block and data.mistral_user.ada for email.
func userConfig(email string) string {
	return providertest.ProviderConfig() + `
data "mistral_user" "ada" {
  email = "` + email + `"
}
`
}

// ada is a member with an organization role UUID and two workspaces, as
// the read by ID returns it.
var ada = map[string]any{
	"name": "Ada Lovelace", "first_name": "Ada", "last_name": "Lovelace",
	"raw_roles": []string{fakeRoleIDs["organization_admin"]},
	"workspaces": []any{
		map[string]any{"uuid": workspaceDefault, "name": "Default", "is_default": true, "raw_roles": []string{"A"}, "raw_role": "A"},
		map[string]any{"uuid": workspaceSupport, "name": "Support", "is_default": false, "raw_roles": []string{"M"}, "raw_role": "M"},
	},
}

// The lookup ignores case and keeps the configured address; the filter of
// the API matches more members than the address, over two pages, and the
// data source picks the member with that exact address, on the second page.
func TestUserDataSourceReadsAMemberByEmail(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	for _, email := range emails(100, "ada@example.com") {
		f.seedUser(email, nil)
	}
	id := f.seedUser("ada@example.com", ada)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: userConfig("Ada@Example.com"),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(userAddr, tfjsonpath.New("id"), knownvalue.StringExact(id)),
				statecheck.ExpectKnownValue(userAddr, tfjsonpath.New("email"), knownvalue.StringExact("Ada@Example.com")),
				statecheck.ExpectKnownValue(userAddr, tfjsonpath.New("name"), knownvalue.StringExact("Ada Lovelace")),
				statecheck.ExpectKnownValue(userAddr, tfjsonpath.New("first_name"), knownvalue.StringExact("Ada")),
				statecheck.ExpectKnownValue(userAddr, tfjsonpath.New("last_name"), knownvalue.StringExact("Lovelace")),
				statecheck.ExpectKnownValue(userAddr, tfjsonpath.New("organization_roles"),
					knownvalue.ListExact([]knownvalue.Check{knownvalue.StringExact(fakeRoleIDs["organization_admin"])})),
				statecheck.ExpectKnownValue(userAddr, tfjsonpath.New("workspaces"), knownvalue.ListExact([]knownvalue.Check{
					knownvalue.ObjectExact(map[string]knownvalue.Check{
						"workspace_id": knownvalue.StringExact(workspaceDefault), "name": knownvalue.StringExact("Default"),
						"is_default": knownvalue.Bool(true), "roles": knownvalue.ListExact([]knownvalue.Check{knownvalue.StringExact("A")}),
					}),
					knownvalue.ObjectExact(map[string]knownvalue.Check{
						"workspace_id": knownvalue.StringExact(workspaceSupport), "name": knownvalue.StringExact("Support"),
						"is_default": knownvalue.Bool(false), "roles": knownvalue.ListExact([]knownvalue.Check{knownvalue.StringExact("M")}),
					}),
				})),
			},
		}},
	})
	f.assertListQueries(t, "email=Ada%40Example.com&page=1&page_size=100", "email=Ada%40Example.com&page=2&page_size=100")
	if got := f.Requests(routeGetUser)[0].Path; got != "/v1/admin/users/"+id {
		t.Errorf("read path = %s", got)
	}
	f.AssertCredential("", sdktest.AdminKey)
}

// A member the API returns without roles or workspaces reads null for
// them, not an empty list: the API did not say.
func TestUserDataSourceReadsMissingListsAsNull(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.seedUser("ada@example.com", map[string]any{"raw_roles": nil, "workspaces": nil, "first_name": nil})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: userConfig("ada@example.com"),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(userAddr, tfjsonpath.New("organization_roles"), knownvalue.Null()),
				statecheck.ExpectKnownValue(userAddr, tfjsonpath.New("workspaces"), knownvalue.Null()),
				statecheck.ExpectKnownValue(userAddr, tfjsonpath.New("first_name"), knownvalue.Null()),
			},
		}},
	})
}

// A filter that matches other addresses only gives the not-found error,
// with no hint and no read by ID.
func TestUserDataSourceNotFound(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.seedUser("x.ada@example.com", nil)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      userConfig("ada@example.com"),
			ExpectError: regexp.MustCompile(`User\s+not\s+found(.|\n)*No\s+member\s+of\s+the\s+organization\s+has\s+the\s+email\s+"ada@example.com"\.\n*$`),
		}},
	})
	f.AssertCalled(routeGetUser, 0)
}

// An address with a pending invite is not a member yet, and the error says
// so.
func TestUserDataSourceNotFoundNamesAPendingInvite(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.seedInvite("ada@example.com", []string{"M"}, false)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      userConfig("ada@example.com"),
			ExpectError: regexp.MustCompile(`User\s+not\s+found(.|\n)*invite\s+for\s+that\s+address\s+is\s+pending`),
		}},
	})
}

// The member can leave between the list and the read by ID.
func TestUserDataSourceNotFoundOnTheReadByID(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.seedUser("ada@example.com", nil)
	f.FailNext(routeGetUser, 404, 1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      userConfig("ada@example.com"),
			ExpectError: regexp.MustCompile(`User\s+not\s+found`),
		}},
	})
}

// A failed list and a failed read by ID are reported, each after the
// retries of a read.
func TestUserDataSourceReportsOtherErrors(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.seedUser("ada@example.com", nil)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				PreConfig:   func() { f.FailNext(routeListUsers, 500, 4) },
				Config:      userConfig("ada@example.com"),
				ExpectError: regexp.MustCompile(`Cannot\s+read\s+the\s+user(.|\n)*500`),
			},
			{
				PreConfig:   func() { f.FailNext(routeGetUser, 500, 4) },
				Config:      userConfig("ada@example.com"),
				ExpectError: regexp.MustCompile(`Cannot\s+read\s+the\s+user(.|\n)*500`),
			},
		},
	})
	f.AssertCalled(routeGetUser, 4)
}
