// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package libraries_test

import (
	"os"
	"path/filepath"
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

const documentAddr = "mistral_library_document.guide"

// writeFile writes content to a new file of a temporary directory of the
// test, or overwrites it, and returns its path.
func writeFile(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	rewrite(t, p, content)
	return filepath.ToSlash(p)
}

// rewrite overwrites the file at p.
func rewrite(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// documentConfig is a library and its document guide with body.
func documentConfig(body string) string {
	return libraryConfig(minimalLibrary) + `
resource "mistral_library_document" "guide" {
  library_id = mistral_library.this.id
` + body + `
}
`
}

// documentIDs reads the library and document IDs of the document of the
// state.
func documentIDs(libraryID, documentID *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		attrs := s.RootModule().Resources[documentAddr].Primary.Attributes
		*libraryID, *documentID = attrs["library_id"], attrs["document_id"]
		return nil
	}
}

// TestLibraryDocumentResourceLifecycle uploads a file and waits for its
// processing, sets then removes its metadata in place, and imports it by ID
// and by identity.
func TestLibraryDocumentResourceLifecycle(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	guide := writeFile(t, "guide.md", "# Guide\n")
	source := `  source      = "` + guide + `"
  source_hash = filesha256("` + guide + `")
`
	var libraryID, documentID string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config:           documentConfig(source),
				ConfigPlanChecks: providertest.ExpectAction(documentAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(documentAddr, tfjsonpath.New("document_id"), knownvalue.StringRegexp(uuid)),
					statecheck.ExpectKnownValue(documentAddr, tfjsonpath.New("id"), knownvalue.StringRegexp(regexp.MustCompile(`^[0-9a-f-]{36}:[0-9a-f-]{36}$`))),
					statecheck.ExpectKnownValue(documentAddr, tfjsonpath.New("name"), knownvalue.StringExact("guide.md")),
					statecheck.ExpectKnownValue(documentAddr, tfjsonpath.New("source"), knownvalue.StringExact(guide)),
					statecheck.ExpectKnownValue(documentAddr, tfjsonpath.New("source_hash"),
						knownvalue.StringExact("bc553ffe57e544498b12a9865dbf3abc2004c474e349c52c378eaa402287424b")),
					statecheck.ExpectKnownValue(documentAddr, tfjsonpath.New("mime_type"), knownvalue.StringExact("text/markdown")),
					statecheck.ExpectKnownValue(documentAddr, tfjsonpath.New("extension"), knownvalue.StringExact("md")),
					statecheck.ExpectKnownValue(documentAddr, tfjsonpath.New("size"), knownvalue.Int64Exact(8)),
					statecheck.ExpectKnownValue(documentAddr, tfjsonpath.New("number_of_pages"), knownvalue.Int64Exact(1)),
					statecheck.ExpectKnownValue(documentAddr, tfjsonpath.New("process_status"), knownvalue.StringExact("done")),
					statecheck.ExpectKnownValue(documentAddr, tfjsonpath.New("attributes"), knownvalue.Null()),
					statecheck.ExpectKnownValue(documentAddr, tfjsonpath.New("expires_at"), knownvalue.Null()),
					statecheck.ExpectKnownValue(documentAddr, tfjsonpath.New("created_at"), knownvalue.StringExact(at(2))),
					statecheck.ExpectKnownValue(documentAddr, tfjsonpath.New("last_processed_at"), knownvalue.StringExact(at(3))),
				},
				Check: documentIDs(&libraryID, &documentID),
			},
			{
				Config: documentConfig(source + `
  name       = "Support guide"
  attributes = { team = "support", tier = "1" }
  expires_at = "2099-01-01T00:00:00Z"
`),
				ConfigPlanChecks: providertest.ExpectAction(documentAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(documentAddr, tfjsonpath.New("name"), knownvalue.StringExact("Support guide")),
					statecheck.ExpectKnownValue(documentAddr, tfjsonpath.New("attributes"), knownvalue.MapExact(map[string]knownvalue.Check{
						"team": knownvalue.StringExact("support"), "tier": knownvalue.StringExact("1"),
					})),
					statecheck.ExpectKnownValue(documentAddr, tfjsonpath.New("expires_at"), knownvalue.StringExact("2099-01-01T00:00:00Z")),
					statecheck.ExpectKnownValue(documentAddr, tfjsonpath.New("size"), knownvalue.Int64Exact(8)),
				},
			},
			{
				// Removing name, a key of attributes and expires_at renames the
				// document after its file and clears the expiry.
				Config: documentConfig(source + `
  attributes = { team = "support" }
`),
				ConfigPlanChecks: providertest.ExpectAction(documentAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(documentAddr, tfjsonpath.New("name"), knownvalue.StringExact("guide.md")),
					statecheck.ExpectKnownValue(documentAddr, tfjsonpath.New("attributes"), knownvalue.MapExact(map[string]knownvalue.Check{
						"team": knownvalue.StringExact("support"),
					})),
					statecheck.ExpectKnownValue(documentAddr, tfjsonpath.New("expires_at"), knownvalue.Null()),
				},
			},
			{
				ResourceName:            documentAddr,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"source", "source_hash"},
			},
			{
				// The import cannot recover the file: the plan records source
				// and source_hash in place.
				ResourceName:       documentAddr,
				ImportState:        true,
				ImportStateKind:    resource.ImportBlockWithResourceIdentity,
				ExpectNonEmptyPlan: true,
				ImportPlanChecks: resource.ImportPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(documentAddr, plancheck.ResourceActionUpdate)},
				},
			},
		},
	})

	f.AssertCalled(routeUpload, 1)
	parts, err := f.Requests(routeUpload)[0].Parts()
	if err != nil || len(parts) != 1 || parts[0].Field != "file" || parts[0].Filename != "guide.md" || string(parts[0].Content) != "# Guide\n" {
		t.Errorf("upload parts = %+v, %v", parts, err)
	}
	if want := "/v1/libraries/" + libraryID + "/documents"; f.Requests(routeUpload)[0].Path != want {
		t.Errorf("upload path = %s, want %s", f.Requests(routeUpload)[0].Path, want)
	}
	f.AssertCalled(routeDocumentStatus, 2)
	f.AssertJSONBodies(routeUpdateDocument,
		`{"name": "Support guide", "attributes": {"team": "support", "tier": "1"}, "expires_at": "2099-01-01T00:00:00Z"}`,
		`{"name": "guide.md", "attributes": {"team": "support"}, "expires_at": null}`)
	if want := "/v1/libraries/" + libraryID + "/documents/" + documentID; f.Requests(routeDeleteDocument)[0].Path != want {
		t.Errorf("delete path = %s, want %s", f.Requests(routeDeleteDocument)[0].Path, want)
	}
	f.AssertCredential("", sdktest.APIKey)
}

// The upload carries only the file: a create with a name, attributes or an
// expiry sets them once the processing is done.
func TestLibraryDocumentResourceSetsTheMetadataAfterTheProcessing(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	guide := writeFile(t, "guide.pdf", "%PDF-1.7")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: documentConfig(`
  source     = "` + guide + `"
  attributes = { year = "2026" }
  expires_at = "2099-06-30T12:00:00+02:00"
`),
			ConfigPlanChecks: providertest.ExpectAction(documentAddr, plancheck.ResourceActionCreate),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(documentAddr, tfjsonpath.New("name"), knownvalue.StringExact("guide.pdf")),
				statecheck.ExpectKnownValue(documentAddr, tfjsonpath.New("mime_type"), knownvalue.StringExact("application/pdf")),
				// The state keeps the configured text of the instant.
				statecheck.ExpectKnownValue(documentAddr, tfjsonpath.New("expires_at"), knownvalue.StringExact("2099-06-30T12:00:00+02:00")),
				statecheck.ExpectKnownValue(documentAddr, tfjsonpath.New("source_hash"), knownvalue.Null()),
			},
		}},
	})
	f.AssertJSONBodies(routeUpdateDocument, `{"name": "guide.pdf", "attributes": {"year": "2026"}, "expires_at": "2099-06-30T12:00:00+02:00"}`)
}

// The API cannot change the content of a document: a new hash of the file,
// or another file, replaces the document.
func TestLibraryDocumentResourceReplacesTheDocumentForNewContent(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	guide := writeFile(t, "guide.md", "# Guide\n")
	faq := writeFile(t, "faq.md", "# FAQ\n")
	tracked := func(p string) string {
		return documentConfig(`  source      = "` + p + `"
  source_hash = filesha256("` + p + `")`)
	}
	var libraryID, first, second string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: tracked(guide), Check: documentIDs(&libraryID, &first)},
			{
				PreConfig:        func() { rewrite(t, guide, "# Guide, second edition\n") },
				Config:           tracked(guide),
				ConfigPlanChecks: providertest.ExpectAction(documentAddr, plancheck.ResourceActionReplace),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(documentAddr, tfjsonpath.New("size"), knownvalue.Int64Exact(24)),
				},
				Check: documentIDs(&libraryID, &second),
			},
			{
				Config:           tracked(faq),
				ConfigPlanChecks: providertest.ExpectAction(documentAddr, plancheck.ResourceActionReplace),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(documentAddr, tfjsonpath.New("name"), knownvalue.StringExact("faq.md")),
				},
			},
		},
	})
	if first == second {
		t.Errorf("the new content kept the document %s", first)
	}
	f.AssertCalled(routeUpload, 3)
	f.AssertCalled(routeDeleteDocument, 3)
	f.AssertCalled(routeUpdateDocument, 0)
}

// A failed processing leaves the document in the state, tainted: the next
// apply replaces it.
func TestLibraryDocumentResourceTaintsADocumentThatFailedProcessing(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	scan := writeFile(t, "scan.pdf", "%PDF-1.4 unreadable")
	f.process("scan.pdf", "in_progress", "error")
	config := documentConfig(`  source = "` + scan + `"`)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config:      config,
				ExpectError: regexp.MustCompile(`Library\s+document\s+processing\s+failed(.|\n)*"scan.pdf"(.|\n)*status\s+is\s+"error"(.|\n)*tainted`),
			},
			{
				// A document with no text is a failure too.
				PreConfig: func() { f.process("scan.pdf", "missing_content") },
				Config:    config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(documentAddr, plancheck.ResourceActionReplace)},
				},
				ExpectError: regexp.MustCompile(`status\s+is\s+"missing_content"`),
			},
			{
				PreConfig:        func() { f.process("scan.pdf", "in_progress", "done") },
				Config:           config,
				ConfigPlanChecks: providertest.ExpectAction(documentAddr, plancheck.ResourceActionReplace),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(documentAddr, tfjsonpath.New("process_status"), knownvalue.StringExact("done")),
				},
			},
		},
	})
	f.AssertCalled(routeUpload, 3)
	f.AssertCalled(routeDeleteDocument, 3)
}

// A document still processing when timeouts.create runs out is tainted too.
// The timeout leaves the upload a second, even on a loaded machine; the fake
// answers the status reads of that second, one per millisecond.
func TestLibraryDocumentResourceTimesOut(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.SetMaxRequests(5000)
	long := writeFile(t, "long.pdf", "%PDF-1.7 long")
	f.process("long.pdf", "waiting_for_capacity")
	config := func(timeout string) string {
		return documentConfig(`  source = "` + long + `"
  timeouts {
    create = "` + timeout + `"
  }`)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config:      config("1s"),
				ExpectError: regexp.MustCompile(`Library\s+document\s+still\s+processing(.|\n)*status\s+"waiting_for_capacity"(.|\n)*timeouts.create\s+\(1s\)`),
			},
			{
				PreConfig:        func() { f.process("long.pdf", "done") },
				Config:           config("10m"),
				ConfigPlanChecks: providertest.ExpectAction(documentAddr, plancheck.ResourceActionReplace),
			},
		},
	})
}

// No silent adoption: the API answers an upload of content the library
// already holds with the existing document, which the create refuses to
// take over.
func TestLibraryDocumentResourceRefusesToTakeOverADocument(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	libraryID := f.seedLibrary()
	documentID := f.seedDocument(libraryID, "Guide", "# Guide\n", nil)
	guide := writeFile(t, "guide.md", "# Guide\n")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig() + `
resource "mistral_library_document" "guide" {
  library_id = "` + libraryID + `"
  source     = "` + guide + `"
}
`,
			ExpectError: regexp.MustCompile(`Library\s+document\s+already\s+exists(.|\n)*"Guide"(.|\n)*import\s+block\s+\(id\s+=\s+"` + libraryID + `:` + documentID + `"\)`),
		}},
	})
	f.AssertCalled(routeDocumentStatus, 0)
	f.AssertCalled(routeDeleteDocument, 0)
}

// A document uploaded outside Terraform imports with an import block: the
// first apply records the file in place, without an upload, and a numeric
// attribute reads as its JSON text.
func TestLibraryDocumentResourceImportsADocumentCreatedOutsideTerraform(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	libraryID := f.seedLibrary()
	documentID := f.seedDocument(libraryID, "guide.md", "# Guide\n", map[string]any{"attributes": map[string]any{"year": 2024}})
	guide := writeFile(t, "guide.md", "# Guide\n")
	config := providertest.ProviderConfig() + `
resource "mistral_library_document" "guide" {
  library_id  = "` + libraryID + `"
  source      = "` + guide + `"
  source_hash = filesha256("` + guide + `")
  attributes  = { year = "2024" }
}
`

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config: config + `
import {
  to = mistral_library_document.guide
  id = "` + libraryID + `:` + documentID + `"
}
`,
				ConfigPlanChecks: providertest.ExpectAction(documentAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(documentAddr, tfjsonpath.New("source"), knownvalue.StringExact(guide)),
					statecheck.ExpectKnownValue(documentAddr, tfjsonpath.New("attributes").AtMapKey("year"), knownvalue.StringExact("2024")),
				},
			},
			{
				// From then on, new content replaces the document. The new
				// document gets the attributes as strings.
				PreConfig:        func() { rewrite(t, guide, "# Guide, second edition\n") },
				Config:           config,
				ConfigPlanChecks: providertest.ExpectAction(documentAddr, plancheck.ResourceActionReplace),
			},
		},
	})
	f.AssertCalled(routeUpload, 1)
	f.AssertJSONBodies(routeUpdateDocument, `{"name": "guide.md", "attributes": {"year": "2024"}, "expires_at": null}`)
}

// A document deleted outside Terraform, or past its expiry, reads as gone
// and plans a new upload.
func TestLibraryDocumentResourceRecreatesADocumentGoneOutsideTerraform(t *testing.T) {
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
				// Deleted in AI Studio.
				PreConfig:        func() { f.documents.Delete(documentID) },
				Config:           config,
				ConfigPlanChecks: providertest.ExpectAction(documentAddr, plancheck.ResourceActionCreate),
				Check:            documentIDs(&libraryID, &documentID),
			},
			{
				// Expired: the expiry was set outside Terraform.
				PreConfig:        func() { f.documents.Patch(documentID, map[string]any{"expires_at": "2026-01-01T00:00:00Z"}) },
				Config:           config,
				ConfigPlanChecks: providertest.ExpectAction(documentAddr, plancheck.ResourceActionCreate),
			},
		},
	})
	f.AssertCalled(routeUpload, 3)
}

// An expiry in the past would delete the document right after its upload.
func TestLibraryDocumentResourceRefusesAnExpiryInThePast(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	guide := writeFile(t, "guide.md", "# Guide\n")
	config := func(expiry string) string {
		return documentConfig(`  source     = "` + guide + `"
  expires_at = "` + expiry + `"`)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config:      config("2020-01-01T00:00:00Z"),
				ExpectError: regexp.MustCompile(`Expiry\s+in\s+the\s+past(.|\n)*2020-01-01T00:00:00Z`),
			},
			{Config: config("2099-01-01T00:00:00Z")},
			{
				Config:      config("2021-01-01T00:00:00Z"),
				ExpectError: regexp.MustCompile(`Expiry\s+in\s+the\s+past`),
			},
		},
	})
	f.AssertCalled(routeUpload, 1)
}

// Invalid arguments fail the plan, before any request.
func TestLibraryDocumentResourceValidatesItsArguments(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	guide := writeFile(t, "guide.md", "# Guide\n")
	minimal := `library_id = "lib"
source = "` + guide + `"
`

	cases := map[string]struct{ body, err string }{
		"malformed expiry":     {minimal + `expires_at = "tomorrow"`, `Invalid\s+date-time`},
		"empty attributes":     {minimal + `attributes = {}`, `map\s+must\s+contain\s+at\s+least\s+1`},
		"colon in library ID":  {`library_id = "a:b"` + "\n" + `source = "` + guide + `"`, `must\s+not\s+be\s+empty\s+or\s+contain\s+a\s+colon`},
		"empty name":           {minimal + `name = ""`, `string\s+length\s+must\s+be\s+at\s+least\s+1`},
		"malformed timeout":    {minimal + `timeouts { create = "soon" }`, `(?i)duration`},
		"unknown timeout kind": {minimal + `timeouts { delete = "1m" }`, `Unsupported\s+argument`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
				Steps: []resource.TestStep{{
					Config:      providertest.ProviderConfig() + "resource \"mistral_library_document\" \"guide\" {\n" + c.body + "\n}\n",
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(c.err),
				}},
			})
		})
	}
	f.AssertCalled("", 0)
}
