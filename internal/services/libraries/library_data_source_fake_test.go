// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package libraries_test

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

const libraryDataAddr = "data.mistral_library.studio"

// libraryDataConfig is the provider block and the data source for id.
func libraryDataConfig(id string) string {
	return providertest.ProviderConfig() + `
data "mistral_library" "studio" {
  id = "` + id + `"
}
`
}

// The data source reads a library built outside Terraform, with the counts
// of its live documents.
func TestLibraryDataSourceReadsALibrary(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	id := f.seedLibrary()
	f.seedDocument(id, "guide.md", "# Guide\n", nil)
	f.seedDocument(id, "faq.md", "# FAQ\n", nil)
	// An expired document no longer counts.
	f.seedDocument(id, "old.md", "# Old\n", map[string]any{"expires_at": "2026-01-01T00:00:00Z"})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: libraryDataConfig(id),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(libraryDataAddr, tfjsonpath.New("name"), knownvalue.StringExact("studio")),
				statecheck.ExpectKnownValue(libraryDataAddr, tfjsonpath.New("description"), knownvalue.StringExact("Built in AI Studio")),
				statecheck.ExpectKnownValue(libraryDataAddr, tfjsonpath.New("owner_type"), knownvalue.StringExact("Workspace")),
				statecheck.ExpectKnownValue(libraryDataAddr, tfjsonpath.New("owner_id"), knownvalue.StringExact(workspaceID)),
				statecheck.ExpectKnownValue(libraryDataAddr, tfjsonpath.New("nb_documents"), knownvalue.Int64Exact(2)),
				statecheck.ExpectKnownValue(libraryDataAddr, tfjsonpath.New("total_size"), knownvalue.Int64Exact(14)),
				statecheck.ExpectKnownValue(libraryDataAddr, tfjsonpath.New("created_at"), knownvalue.StringExact(at(1))),
				statecheck.ExpectKnownValue(libraryDataAddr, tfjsonpath.New("updated_at"), knownvalue.StringExact(at(1))),
			},
		}},
	})
	f.AssertCredential(routeGetLibrary, sdktest.APIKey)
}

// A library the key cannot see is an error on id.
func TestLibraryDataSourceNotFound(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      libraryDataConfig("0197f425-5e85-7353-b8e7-e8b974b9c613"),
			ExpectError: regexp.MustCompile(`Library\s+not\s+found(.|\n)*No\s+library\s+"0197f425-5e85-7353-b8e7-e8b974b9c613"`),
		}},
	})
}

// A 5xx is retried like any read, then reported.
func TestLibraryDataSourceReportsOtherErrors(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.FailNext(routeGetLibrary, 500, 4)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      libraryDataConfig("0197f425-5e85-7353-b8e7-e8b974b9c613"),
			ExpectError: regexp.MustCompile(`Cannot\s+read\s+the\s+library(.|\n)*500`),
		}},
	})
	f.AssertCalled(routeGetLibrary, 4)
}
