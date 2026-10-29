// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package libraries_test

import (
	"net/http"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"

	"github.com/ippontech/terraform-provider-mistral/internal/providertest"
	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

// An upload that meets a 5xx is sent once: the document may exist, and the
// next apply meets it as content the library already holds.
func TestLibraryDocumentResourceUploadIsSentOnceOnA5xx(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	guide := writeFile(t, "guide.md", "# Guide\n")
	f.FailNext(routeUpload, 500, 1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      documentConfig(`  source = "` + guide + `"`),
			ExpectError: regexp.MustCompile(`Cannot\s+upload\s+the\s+library\s+document(.|\n)*500`),
		}},
	})
	f.AssertCalled(routeUpload, 1)
}

// A source that cannot be read fails before any request.
func TestLibraryDocumentResourceNeedsARegularSourceFile(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	dir := t.TempDir()

	for name, c := range map[string]struct{ source, err string }{
		"missing file": {filepath.ToSlash(filepath.Join(dir, "gone.pdf")), `Cannot\s+read\s+the\s+source\s+file(.|\n)*no\s+such\s+file`},
		"directory":    {filepath.ToSlash(dir), `Cannot\s+read\s+the\s+source\s+file(.|\n)*it\s+is\s+a\s+directory`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
				Steps: []resource.TestStep{{
					Config:      documentConfig(`  source = "` + c.source + `"`),
					ExpectError: regexp.MustCompile(c.err),
				}},
			})
		})
	}
	f.AssertCalled(routeUpload, 0)
}

// A read of the status that fails after the client's retries ends the
// create, with the document tainted.
func TestLibraryDocumentResourceReportsAStatusError(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	guide := writeFile(t, "guide.md", "# Guide\n")
	f.FailNext(routeDocumentStatus, 503, 4)
	config := documentConfig(`  source = "` + guide + `"`)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config:      config,
				ExpectError: regexp.MustCompile(`Cannot\s+read\s+the\s+processing\s+status(.|\n)*503(.|\n)*tainted`),
			},
			{
				Config:           config,
				ConfigPlanChecks: providertest.ExpectAction(documentAddr, plancheck.ResourceActionReplace),
			},
		},
	})
}

// A PATCH that fails after the processing leaves the document tainted; a
// PATCH that fails on an update is sent once.
func TestLibraryDocumentResourceReportsMetadataErrors(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	guide := writeFile(t, "guide.md", "# Guide\n")
	named := func(name string) string {
		return documentConfig(`  source = "` + guide + `"
  name   = "` + name + `"`)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				PreConfig:   func() { f.FailNextWith(routeUpdateDocument, 422, "Invalid name", 1) },
				Config:      named("Guide"),
				ExpectError: regexp.MustCompile(`Cannot\s+complete\s+the\s+library\s+document(.|\n)*422(.|\n)*tainted`),
			},
			{
				// The tainted document is replaced.
				Config: named("Guide"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(documentAddr, plancheck.ResourceActionReplace)},
				},
			},
			{
				PreConfig:   func() { f.FailNext(routeUpdateDocument, 500, 1) },
				Config:      named("Support guide"),
				ExpectError: regexp.MustCompile(`Cannot\s+update\s+the\s+library\s+document(.|\n)*500`),
			},
		},
	})
	f.AssertCalled(routeUpdateDocument, 3)
}

// Errors other than 404 of a read and of a delete reach the user.
func TestLibraryDocumentResourceReportsReadAndDeleteErrors(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	guide := writeFile(t, "guide.md", "# Guide\n")
	config := documentConfig(`  source = "` + guide + `"`)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: config},
			{
				PreConfig:   func() { f.FailNextWith(routeGetDocument, 401, "Unauthorized", 1) },
				Config:      config,
				ExpectError: regexp.MustCompile(`Cannot\s+read\s+the\s+library\s+document(.|\n)*401`),
			},
			{
				PreConfig:   func() { f.FailNext(routeDeleteDocument, 500, 4) },
				Config:      libraryConfig(minimalLibrary),
				ExpectError: regexp.MustCompile(`Cannot\s+delete\s+the\s+library\s+document(.|\n)*500`),
			},
		},
	})
}

// Deleting the library deletes its documents: the destroy of a document
// whose library is gone succeeds.
func TestLibraryDocumentResourceFollowsItsLibraryDeletedOutsideTerraform(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	guide := writeFile(t, "guide.md", "# Guide\n")
	config := documentConfig(`  source = "` + guide + `"`)
	var libraryID, documentID string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: config, Check: documentIDs(&libraryID, &documentID)},
			{
				PreConfig:        func() { f.libraries.Delete(libraryID) },
				Config:           config,
				ConfigPlanChecks: providertest.ExpectAction(documentAddr, plancheck.ResourceActionCreate),
			},
		},
	})
}

// An import ID other than <library_id>:<document_id> is refused before any
// request.
func TestLibraryDocumentResourceRejectsAMalformedImportID(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	guide := writeFile(t, "guide.md", "# Guide\n")

	for _, id := range []string{"lib", "lib:", ":doc", "lib:doc:extra"} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
				Steps: []resource.TestStep{{
					Config: providertest.ProviderConfig() + `
resource "mistral_library_document" "guide" {
  library_id = "lib"
  source     = "` + guide + `"
}
`,
					ResourceName:  documentAddr,
					ImportState:   true,
					ImportStateId: id,
					ExpectError:   regexp.MustCompile(`Invalid\s+import\s+ID(.|\n)*<library_id>:<document_id>`),
				}},
			})
		})
	}
}

// The API may still return a document past its expiry until its deletion
// runs: Read treats it as gone, so an import finds nothing. The fake deletes
// at the expiry, so a canned answer stands for the API here.
func TestLibraryDocumentResourceTreatsAnExpiredDocumentAsGone(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond(routeGetDocument, http.StatusOK, map[string]any{
		"id": "00000000-0000-4000-8000-000000000002", "library_id": "00000000-0000-4000-8000-000000000001",
		"name": "guide.md", "created_at": "2026-01-01T00:00:00Z", "process_status": "done", "expires_at": "2026-02-01T00:00:00Z",
	})
	guide := writeFile(t, "guide.md", "# Guide\n")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig() + `
import {
  to = mistral_library_document.guide
  id = "00000000-0000-4000-8000-000000000001:00000000-0000-4000-8000-000000000002"
}

resource "mistral_library_document" "guide" {
  library_id = "00000000-0000-4000-8000-000000000001"
  source     = "` + guide + `"
}
`,
			ExpectError: regexp.MustCompile(`Cannot\s+import\s+non-existent\s+remote\s+object`),
		}},
	})
}
