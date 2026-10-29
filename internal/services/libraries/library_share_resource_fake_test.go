// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package libraries_test

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

const (
	shareAddr = "mistral_library_share.support"

	// The entities of the tests: a workspace and a user other than the
	// owners of the fake.
	supportWorkspaceID = "3f2e1d0c-9b8a-4f7e-8d6c-5b4a3f2e1d0c"
	analystID          = "9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d"
)

// shareConfig is the provider block and shareBlock.
func shareConfig(libraryID, entity, id, level string) string {
	return providertest.ProviderConfig() + shareBlock(libraryID, entity, id, level)
}

// shareBlock is a grant on the library libraryID (a quoted ID, or a
// reference), with the entity and the level.
func shareBlock(libraryID, entity, id, level string) string {
	return `
resource "mistral_library_share" "support" {
  library_id      = ` + libraryID + `
  share_with_type = "` + entity + `"
  share_with_id   = "` + id + `"
  level           = "` + level + `"
}
`
}

// seedShare stores a grant made outside Terraform.
func (f *fake) seedShare(libraryID, entity, id, role string) {
	f.shares.Put(map[string]any{
		"key": shareKey(libraryID, entity, id), "library_id": libraryID, "user_id": nil, "org_id": orgID,
		"role": role, "share_with_type": entity, "share_with_uuid": id,
	})
}

// TestLibraryShareResourceLifecycle grants a workspace access to a library
// built outside Terraform, changes the level in place, imports the grant,
// and replaces it for another entity.
func TestLibraryShareResourceLifecycle(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	libraryID := f.seedLibrary()
	// Another grant of the library, which Read must skip.
	f.seedShare(libraryID, "User", analystID, "Editor")
	library := `"` + libraryID + `"`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config:           shareConfig(library, "Workspace", supportWorkspaceID, "Viewer"),
				ConfigPlanChecks: providertest.ExpectAction(shareAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(shareAddr, tfjsonpath.New("id"), knownvalue.StringExact(libraryID+":Workspace:"+supportWorkspaceID)),
					statecheck.ExpectKnownValue(shareAddr, tfjsonpath.New("level"), knownvalue.StringExact("Viewer")),
				},
			},
			{
				Config:           shareConfig(library, "Workspace", supportWorkspaceID, "Editor"),
				ConfigPlanChecks: providertest.ExpectAction(shareAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(shareAddr, tfjsonpath.New("level"), knownvalue.StringExact("Editor")),
				},
			},
			{
				ResourceName:      shareAddr,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				ResourceName:    shareAddr,
				ImportState:     true,
				ImportStateKind: resource.ImportBlockWithResourceIdentity,
			},
			{
				// Another entity is another grant.
				Config:           shareConfig(library, "User", "5d4c3b2a-1f0e-4d9c-8b7a-6f5e4d3c2b1a", "Editor"),
				ConfigPlanChecks: providertest.ExpectAction(shareAddr, plancheck.ResourceActionReplace),
			},
		},
	})

	f.AssertJSONBodies(routePutShare,
		`{"level": "Viewer", "share_with_uuid": "`+supportWorkspaceID+`", "share_with_type": "Workspace"}`,
		`{"level": "Editor", "share_with_uuid": "`+supportWorkspaceID+`", "share_with_type": "Workspace"}`,
		`{"level": "Editor", "share_with_uuid": "5d4c3b2a-1f0e-4d9c-8b7a-6f5e4d3c2b1a", "share_with_type": "User"}`)
	f.AssertJSONBodies(routeDeleteShare,
		`{"share_with_uuid": "`+supportWorkspaceID+`", "share_with_type": "Workspace"}`,
		`{"share_with_uuid": "5d4c3b2a-1f0e-4d9c-8b7a-6f5e4d3c2b1a", "share_with_type": "User"}`)
	if _, ok := f.shares.Get(shareKey(libraryID, "User", analystID)); !ok {
		t.Error("the grant made outside Terraform was deleted")
	}
	f.AssertCredential("", sdktest.APIKey)
}

// The PUT is an upsert: a create must not take over a grant made outside
// Terraform, whatever the case the API answers the entity in.
func TestLibraryShareResourceRefusesToTakeOverAGrant(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	libraryID := f.seedLibrary()
	f.shares.Put(map[string]any{
		"key": "outside", "library_id": libraryID, "org_id": orgID,
		"role": "Viewer", "share_with_type": "workspace", "share_with_uuid": "3F2E1D0C-9B8A-4F7E-8D6C-5B4A3F2E1D0C",
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: shareConfig(`"`+libraryID+`"`, "Workspace", supportWorkspaceID, "Editor"),
			ExpectError: regexp.MustCompile(`Library\s+share\s+already\s+exists(.|\n)*as\s+Viewer(.|\n)*import\s+block\s+\(id\s+=\s+"` +
				libraryID + `:Workspace:` + supportWorkspaceID + `"\)`),
		}},
	})
	f.AssertCalled(routePutShare, 0)
}

// A grant revoked outside Terraform, or whose library is gone, plans a
// create.
func TestLibraryShareResourceRecreatesAGrantGoneOutsideTerraform(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	config := libraryConfig(minimalLibrary) + shareBlock("mistral_library.this.id", "Org", orgID, "Viewer")
	var libraryID string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: func(s *terraform.State) error {
					libraryID = s.RootModule().Resources[libraryAddr].Primary.ID
					return nil
				},
			},
			{
				// Revoked in AI Studio.
				PreConfig:        func() { f.shares.Delete(shareKey(libraryID, "Org", orgID)) },
				Config:           config,
				ConfigPlanChecks: providertest.ExpectAction(shareAddr, plancheck.ResourceActionCreate),
			},
			{
				// The library is gone, and its grants with it.
				PreConfig:        func() { f.libraries.Delete(libraryID) },
				Config:           config,
				ConfigPlanChecks: providertest.ExpectAction(shareAddr, plancheck.ResourceActionCreate),
			},
		},
	})
	f.AssertCalled(routePutShare, 3)
}

// An import ID other than <library_id>:<share_with_type>:<share_with_id>,
// with a known type, is refused.
func TestLibraryShareResourceRejectsAMalformedImportID(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	libraryID := f.seedLibrary()

	for _, id := range []string{libraryID + ":" + supportWorkspaceID, libraryID + ":Group:" + supportWorkspaceID, libraryID + "::" + supportWorkspaceID} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
				Steps: []resource.TestStep{{
					Config:        shareConfig(`"`+libraryID+`"`, "Workspace", supportWorkspaceID, "Viewer"),
					ResourceName:  shareAddr,
					ImportState:   true,
					ImportStateId: id,
					ExpectError:   regexp.MustCompile(`Invalid\s+import\s+ID(.|\n)*<library_id>:<share_with_type>:<share_with_id>`),
				}},
			})
		})
	}
}

// Errors of the list, the PUT and the DELETE reach the user.
func TestLibraryShareResourceReportsErrors(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	libraryID := f.seedLibrary()
	library := `"` + libraryID + `"`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config:      shareConfig(`"00000000-0000-4000-8000-999999999999"`, "Workspace", supportWorkspaceID, "Viewer"),
				ExpectError: regexp.MustCompile(`Cannot\s+create\s+the\s+library\s+share(.|\n)*404`),
			},
			{
				PreConfig: func() {
					f.FailNextWith(routePutShare, 403, "You have to be owner of the library to share a library", 1)
				},
				Config:      shareConfig(library, "Workspace", supportWorkspaceID, "Viewer"),
				ExpectError: regexp.MustCompile(`Cannot\s+share\s+the\s+library(.|\n)*403(.|\n)*owner`),
			},
			{Config: shareConfig(library, "Workspace", supportWorkspaceID, "Viewer")},
			{
				PreConfig:   func() { f.FailNext(routeListShares, 500, 4) },
				Config:      shareConfig(library, "Workspace", supportWorkspaceID, "Viewer"),
				ExpectError: regexp.MustCompile(`Cannot\s+read\s+the\s+library\s+share(.|\n)*500`),
			},
			{
				PreConfig:   func() { f.FailNext(routeDeleteShare, 500, 4) },
				Config:      shareConfig(library, "Workspace", supportWorkspaceID, "Viewer"),
				Destroy:     true,
				ExpectError: regexp.MustCompile(`Cannot\s+delete\s+the\s+library\s+share(.|\n)*500`),
			},
		},
	})
}

// Invalid arguments fail the plan, before any request.
func TestLibraryShareResourceValidatesItsArguments(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	cases := map[string]struct {
		config, err string
	}{
		"unknown entity": {shareConfig(`"lib"`, "Group", supportWorkspaceID, "Viewer"), `value\s+must\s+be\s+one\s+of`},
		"unknown level":  {shareConfig(`"lib"`, "User", analystID, "Owner"), `value\s+must\s+be\s+one\s+of`},
		"colon in an ID": {shareConfig(`"lib"`, "User", "a:b", "Viewer"), `must\s+not\s+be\s+empty\s+or\s+contain\s+a\s+colon`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
				Steps:                    []resource.TestStep{{Config: c.config, PlanOnly: true, ExpectError: regexp.MustCompile(c.err)}},
			})
		})
	}
	f.AssertCalled("", 0)
}
