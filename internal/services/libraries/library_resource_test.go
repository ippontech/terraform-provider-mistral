// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package libraries_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-mistral/internal/acctest"
)

// TestAccLibraryResource creates a library, uploads a document to it and
// waits for its processing, updates the metadata of both, clears what the
// second step removes, reads the library with the data source, imports both,
// and destroys them, against the live API.
func TestAccLibraryResource(t *testing.T) {
	name := acctest.RandomName("library")
	dir := t.TempDir()
	source := filepath.ToSlash(filepath.Join(dir, "guide.md"))
	if err := os.WriteFile(source, []byte("# Returns\n\nA customer can return an order within 30 days.\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := func(library, document string) string {
		return fmt.Sprintf(`
resource "mistral_library" "this" {
  name = %q
%s
}

resource "mistral_library_document" "guide" {
  library_id  = mistral_library.this.id
  source      = %q
  source_hash = filesha256(%q)
%s
}

data "mistral_library" "this" {
  id = mistral_library.this.id
}
`, name, library, source, source, document)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config(`  description = "Acceptance test library"`, `  attributes = { purpose = "terraform-acceptance-test", tier = "1" }`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("mistral_library.this", tfjsonpath.New("owner_type"), knownvalue.StringExact("Workspace")),
					statecheck.ExpectKnownValue("mistral_library_document.guide", tfjsonpath.New("name"), knownvalue.StringExact("guide.md")),
					statecheck.ExpectKnownValue("mistral_library_document.guide", tfjsonpath.New("process_status"), knownvalue.StringExact("done")),
					statecheck.ExpectKnownValue("data.mistral_library.this", tfjsonpath.New("name"), knownvalue.StringExact(name)),
				},
			},
			{
				// Removing the description, a key of attributes, and setting a
				// name and an expiry must update both in place.
				Config: config("", `  name       = "Returns policy"
  attributes = { purpose = "terraform-acceptance-test" }
  expires_at = "2099-01-01T00:00:00Z"`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("mistral_library.this", plancheck.ResourceActionUpdate),
						plancheck.ExpectResourceAction("mistral_library_document.guide", plancheck.ResourceActionUpdate),
					},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("mistral_library.this", tfjsonpath.New("description"), knownvalue.Null()),
					statecheck.ExpectKnownValue("mistral_library.this", tfjsonpath.New("nb_documents"), knownvalue.Int64Exact(1)),
					statecheck.ExpectKnownValue("mistral_library_document.guide", tfjsonpath.New("name"), knownvalue.StringExact("Returns policy")),
					statecheck.ExpectKnownValue("mistral_library_document.guide", tfjsonpath.New("attributes"), knownvalue.MapExact(map[string]knownvalue.Check{
						"purpose": knownvalue.StringExact("terraform-acceptance-test"),
					})),
				},
			},
			{
				// Removing the expiry must clear it.
				Config: config("", `  name       = "Returns policy"
  attributes = { purpose = "terraform-acceptance-test" }`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("mistral_library_document.guide", tfjsonpath.New("expires_at"), knownvalue.Null()),
				},
			},
			{ResourceName: "mistral_library.this", ImportState: true, ImportStateVerify: true},
			{
				ResourceName:            "mistral_library_document.guide",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"source", "source_hash"},
			},
		},
	})
}

// TestAccLibraryShareResource shares a library with a user of the
// organization as a viewer, then as an editor, imports the grant and
// revokes it, against the live API. MISTRAL_TEST_SHARE_USER_ID names the
// user; without it the test is skipped.
func TestAccLibraryShareResource(t *testing.T) {
	acctest.PreCheck(t)
	userID := os.Getenv("MISTRAL_TEST_SHARE_USER_ID")
	if userID == "" {
		t.Skip("MISTRAL_TEST_SHARE_USER_ID is not set: the library share acceptance test needs a user of the organization")
	}
	name := acctest.RandomName("share")
	config := func(level string) string {
		return fmt.Sprintf(`
resource "mistral_library" "this" {
  name = %q
}

resource "mistral_library_share" "user" {
  library_id      = mistral_library.this.id
  share_with_type = "User"
  share_with_id   = %q
  level           = %q
}
`, name, userID, level)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: config("Viewer")},
			{
				Config: config("Editor"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("mistral_library_share.user", plancheck.ResourceActionUpdate)},
				},
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("mistral_library_share.user", tfjsonpath.New("level"), knownvalue.StringExact("Editor")),
				},
			},
			{ResourceName: "mistral_library_share.user", ImportState: true, ImportStateVerify: true},
		},
	})
}
