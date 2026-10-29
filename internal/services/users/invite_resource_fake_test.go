// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package users_test

import (
	"context"
	"fmt"
	"reflect"
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

const inviteAddr = "mistral_invite.grace"

// inviteConfig is the provider block and mistral_invite.grace with body.
func inviteConfig(body string) string {
	return providertest.ProviderConfig() + "resource \"mistral_invite\" \"grace\" {\n" + body + "\n}\n"
}

const (
	minimalInvite = `
  email      = "grace@example.com"
  role_names = ["member"]
`
	// fullInvite lists its sets out of order: the request sends them sorted.
	fullInvite = `
  email         = "grace@example.com"
  role_names    = ["member", "billing_manager"]
  workspace_ids = ["` + workspaceSupport + `", "` + workspaceDefault + `"]
`
	workspaceDefault = "5b0f8a2e-1c3d-4e5f-8a9b-0c1d2e3f4a5b"
	workspaceSupport = "9c8d7e6f-5a4b-4c3d-9e2f-1a0b9c8d7e6f"

	// graceQuery is the filtered list of a create and of a refresh;
	// unfilteredQuery the walk of an import, which knows only the ID.
	graceQuery      = "email=grace%40example.com&page=1&page_size=100"
	unfilteredQuery = "page=1&page_size=100"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// The invite cannot be updated: every change of an argument replaces it,
// and emptying workspace_ids does too. The lifecycle ends with an import by
// ID, which reads the roles back by name through GET /v1/admin/roles, and an
// import by identity.
func TestInviteResourceLifecycle(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	var firstID string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config:           inviteConfig(minimalInvite),
				ConfigPlanChecks: providertest.ExpectAction(inviteAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(inviteAddr, tfjsonpath.New("id"), knownvalue.StringRegexp(uuidPattern)),
					statecheck.ExpectKnownValue(inviteAddr, tfjsonpath.New("email"), knownvalue.StringExact("grace@example.com")),
					statecheck.ExpectKnownValue(inviteAddr, tfjsonpath.New("role_names"), knownvalue.SetExact([]knownvalue.Check{knownvalue.StringExact("member")})),
					statecheck.ExpectKnownValue(inviteAddr, tfjsonpath.New("workspace_ids"), knownvalue.Null()),
					statecheck.ExpectKnownValue(inviteAddr, tfjsonpath.New("status"), knownvalue.StringExact("pending")),
					statecheck.ExpectKnownValue(inviteAddr, tfjsonpath.New("created_at"), knownvalue.StringExact(at(1))),
				},
				Check: func(s *terraform.State) error {
					firstID = s.RootModule().Resources[inviteAddr].Primary.ID
					return nil
				},
			},
			{
				Config:           inviteConfig(fullInvite),
				ConfigPlanChecks: providertest.ExpectAction(inviteAddr, plancheck.ResourceActionDestroyBeforeCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(inviteAddr, tfjsonpath.New("role_names"), knownvalue.SetExact([]knownvalue.Check{
						knownvalue.StringExact("billing_manager"), knownvalue.StringExact("member"),
					})),
					statecheck.ExpectKnownValue(inviteAddr, tfjsonpath.New("workspace_ids"), knownvalue.SetExact([]knownvalue.Check{
						knownvalue.StringExact(workspaceDefault), knownvalue.StringExact(workspaceSupport),
					})),
					statecheck.ExpectKnownValue(inviteAddr, tfjsonpath.New("status"), knownvalue.StringExact("pending")),
					statecheck.ExpectKnownValue(inviteAddr, tfjsonpath.New("created_at"), knownvalue.StringExact(at(2))),
				},
			},
			{
				Config:           inviteConfig(minimalInvite),
				ConfigPlanChecks: providertest.ExpectAction(inviteAddr, plancheck.ResourceActionDestroyBeforeCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(inviteAddr, tfjsonpath.New("workspace_ids"), knownvalue.Null()),
					statecheck.ExpectKnownValue(inviteAddr, tfjsonpath.New("created_at"), knownvalue.StringExact(at(3))),
				},
			},
			{
				ResourceName:      inviteAddr,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				ResourceName:    inviteAddr,
				ImportState:     true,
				ImportStateKind: resource.ImportBlockWithResourceIdentity,
			},
		},
	})

	f.AssertJSONBodies(routeInvite,
		`{"email": "grace@example.com", "role_names": ["member"], "workspace_uuids": null}`,
		`{"email": "grace@example.com", "role_names": ["billing_manager", "member"], "workspace_uuids": ["`+workspaceDefault+`", "`+workspaceSupport+`"]}`,
		`{"email": "grace@example.com", "role_names": ["member"], "workspace_uuids": null}`,
	)
	f.assertListQueries(t, graceQuery, unfilteredQuery)
	f.AssertCalled(routeDeleteInvite, 3)
	if got := f.Requests(routeDeleteInvite)[0].Path; got != "/v1/admin/users-invite/"+firstID {
		t.Errorf("first DELETE path = %s, want the first invite", got)
	}
	if f.invites.Len() != 0 {
		t.Errorf("%d invites left after the destroy", f.invites.Len())
	}
	f.AssertCredential("", sdktest.AdminKey)
}

// An invite revoked outside Terraform is gone: the next plan sends a new
// one.
func TestInviteResourceRecreatesAnInviteRevokedOutsideTerraform(t *testing.T) {
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
				PreConfig:        func() { f.invites.Delete(id) },
				Config:           inviteConfig(minimalInvite),
				ConfigPlanChecks: providertest.ExpectAction(inviteAddr, plancheck.ResourceActionCreate),
			},
		},
	})
	f.AssertCalled(routeInvite, 2)
}

// An import block brings an invite sent from the Admin Panel under
// Terraform with no change: its roles come back by name, from the legacy
// letters as from the role UUIDs, and its workspaces come back.
func TestInviteResourceImportsAnInviteSentOutsideTerraform(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	id := f.seedInvite("grace@example.com", []string{"A", fakeRoleIDs["billing_manager"]}, false)
	f.invites.Patch(id, map[string]any{"workspace_uuids": []string{workspaceDefault}})
	// Other members and invites push the invite to the second page of the
	// walk, which an import makes without a filter.
	for _, email := range emails(100, "colleague@example.com") {
		f.seedUser(email, nil)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig() + `
import {
  to = mistral_invite.grace
  id = "` + id + `"
}

resource "mistral_invite" "grace" {
  email         = "grace@example.com"
  role_names    = ["organization_admin", "billing_manager"]
  workspace_ids = ["` + workspaceDefault + `"]
}
`,
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(inviteAddr, plancheck.ResourceActionNoop)},
			},
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(inviteAddr, tfjsonpath.New("id"), knownvalue.StringExact(id)),
				statecheck.ExpectKnownValue(inviteAddr, tfjsonpath.New("status"), knownvalue.StringExact("pending")),
				statecheck.ExpectKnownValue(inviteAddr, tfjsonpath.New("created_at"), knownvalue.StringExact(at(1))),
			},
		}},
	})
	f.AssertCalled(routeInvite, 0)
	f.assertListQueries(t, unfilteredQuery, "page=2&page_size=100", graceQuery)
}

// A role the roles list does not name is kept as returned, so the plan that
// follows the import shows it instead of hiding it.
func TestInviteResourceImportKeepsARoleItCannotName(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	unknownRole := "00000000-0000-4000-8000-0000000000ff"
	id := f.seedInvite("grace@example.com", []string{unknownRole}, false)

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
			PlanOnly:           true,
			ExpectNonEmptyPlan: true,
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PostApplyPreRefresh: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(inviteAddr, plancheck.ResourceActionDestroyBeforeCreate),
					expectBefore{inviteAddr, "role_names", []any{unknownRole}},
				},
			},
		}},
	})
	// A plan-only step plans twice, and each plan imports: one roles call
	// per import, none for the invite itself.
	if roles, imports := len(f.Requests(routeListRoles)), len(f.Requests(routeListUsers)); roles != imports {
		t.Errorf("%d calls to %s for %d imports, want one per import", roles, routeListRoles, imports)
	}
}

// expectBefore checks the value an attribute holds before a plan: for an
// imported resource, what the import read.
type expectBefore struct {
	addr, attribute string
	want            any
}

// CheckPlan fails unless the attribute of the planned change of addr held
// want before the plan.
func (e expectBefore) CheckPlan(_ context.Context, req plancheck.CheckPlanRequest, resp *plancheck.CheckPlanResponse) {
	for _, rc := range req.Plan.ResourceChanges {
		if rc.Address != e.addr {
			continue
		}
		before, _ := rc.Change.Before.(map[string]any)
		if got := before[e.attribute]; !reflect.DeepEqual(got, e.want) {
			resp.Error = fmt.Errorf("%s.%s before the plan = %#v, want %#v", e.addr, e.attribute, got, e.want)
		}
		return
	}
	resp.Error = fmt.Errorf("no planned change for %s", e.addr)
}
