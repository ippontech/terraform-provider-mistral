// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package apikeys_test

import (
	"fmt"
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
	keyAddr     = "mistral_api_key.ci"
	workspaceID = "00000000-0000-4000-8000-0000000000aa"
	userID      = "00000000-0000-4000-8000-0000000000bb"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// keyConfig is a configuration with one mistral_api_key for workspaceID and
// userID, whose other arguments are body.
func keyConfig(body string) string {
	return providertest.ProviderConfig() + fmt.Sprintf(`
resource "mistral_api_key" "ci" {
  workspace_id = %q
  user_id      = %q
%s
}
`, workspaceID, userID, body)
}

// createBody is the body of a create with name and expiration, JSON values.
func createBody(name, expiration string) string {
	return fmt.Sprintf(`{"name": %s, "expiration": %s, "workspace_uuid": %q, "user_id": %q}`, name, expiration, workspaceID, userID)
}

// readKeyID reads the ID of mistral_api_key.ci into id.
func readKeyID(id *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		*id = s.RootModule().Resources[keyAddr].Primary.ID
		return nil
	}
}

// TestAPIKeyResourceLifecycle creates a key, replaces it by emptying its name,
// then its expiration, and imports it by ID and by identity.
func TestAPIKeyResourceLifecycle(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	var firstID string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config:           keyConfig("  name       = \"ci\"\n  expiration = \"2027-06-30\""),
				ConfigPlanChecks: providertest.ExpectAction(keyAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New("id"), knownvalue.StringRegexp(uuidPattern)),
					statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New("key"), knownvalue.StringExact("fake-secret-0001")),
					statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New("hidden_key"), knownvalue.StringExact("************0001")),
					statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New("name"), knownvalue.StringExact("ci")),
					statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New("expiration"), knownvalue.StringExact("2027-06-30")),
					statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New("expiration_date"), knownvalue.StringExact("2027-06-30")),
					statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New("created_at"), knownvalue.StringExact("2026-10-29T10:01:00Z")),
					statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New("workspace_id"), knownvalue.StringExact(workspaceID)),
					statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New("user_id"), knownvalue.StringExact(userID)),
				},
				Check: readKeyID(&firstID),
			},
			{
				// The API cannot update a key: removing the name replaces it.
				Config:           keyConfig("  expiration = \"2027-06-30\""),
				ConfigPlanChecks: providertest.ExpectAction(keyAddr, plancheck.ResourceActionDestroyBeforeCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New("key"), knownvalue.StringExact("fake-secret-0002")),
					statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New("name"), knownvalue.Null()),
				},
			},
			{
				Config:           keyConfig(""),
				ConfigPlanChecks: providertest.ExpectAction(keyAddr, plancheck.ResourceActionDestroyBeforeCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New("key"), knownvalue.StringExact("fake-secret-0003")),
					statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New("expiration"), knownvalue.Null()),
					statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New("expiration_date"), knownvalue.Null()),
				},
			},
			{
				// The list returns neither the secret nor the user.
				ResourceName:            keyAddr,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"key", "user_id"},
			},
			{
				// The import block records user_id in place: no replacement.
				ResourceName:       keyAddr,
				ImportState:        true,
				ImportStateKind:    resource.ImportBlockWithResourceIdentity,
				ExpectNonEmptyPlan: true,
				ImportPlanChecks: resource.ImportPlanChecks{PreApply: []plancheck.PlanCheck{
					plancheck.ExpectResourceAction(keyAddr, plancheck.ResourceActionUpdate),
				}},
			},
		},
	})

	f.AssertJSONBodies(routeCreate, createBody(`"ci"`, `"2027-06-30"`), createBody("null", `"2027-06-30"`), createBody("null", "null"))
	f.AssertCalled(routeDelete, 3)
	if got := f.Requests(routeDelete)[0].Path; got != "/v1/admin/api-keys/"+firstID {
		t.Errorf("DELETE path = %s", got)
	}
	// A key with a name is looked up by its name; one without is looked up
	// in the whole list.
	if got := f.Requests(routeList)[0].Query.Encode(); got != "limit=100&name=ci&offset=0" {
		t.Errorf("first list query = %q", got)
	}
	if got := f.Requests(routeList)[len(f.Requests(routeList))-1].Query.Encode(); got != "limit=100&offset=0" {
		t.Errorf("last list query = %q", got)
	}
	f.AssertCredential("", sdktest.AdminKey)
}

// With an expiration policy, the API shortens the expiry or sets one. The
// configured expiration stays in the state, and the plan stays empty.
func TestAPIKeyResourceKeepsTheConfiguredExpirationUnderAPolicy(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.setPolicy(30)
	config := keyConfig("  expiration = \"2027-06-30\"") + fmt.Sprintf(`
resource "mistral_api_key" "unlimited" {
  workspace_id = %q
  user_id      = %q
}
`, workspaceID, userID)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config:           config,
				ConfigPlanChecks: providertest.ExpectAction(keyAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New("expiration"), knownvalue.StringExact("2027-06-30")),
					statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New("expiration_date"), knownvalue.StringExact("2026-11-28")),
					statecheck.ExpectKnownValue("mistral_api_key.unlimited", tfjsonpath.New("expiration"), knownvalue.Null()),
					statecheck.ExpectKnownValue("mistral_api_key.unlimited", tfjsonpath.New("expiration_date"), knownvalue.StringExact("2026-11-28")),
				},
			},
			{
				Config:   config,
				PlanOnly: true,
			},
		},
	})
	f.AssertCalled(routeCreate, 2)
}

// An import block brings a key created in the Admin Panel under Terraform.
// user_id and expiration are not read back: the first apply records them in
// place and does not replace the key, which already expires on the
// configured date.
func TestAPIKeyResourceImportsAKeyCreatedOutsideTerraform(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	id := f.seed("ci")
	f.keys.Patch(id, map[string]any{"expiration_date": "2027-06-30"})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config:           keyConfig("  name       = \"ci\"\n  expiration = \"2027-06-30\"") + "import {\n  to = mistral_api_key.ci\n  id = \"" + id + "\"\n}\n",
				ConfigPlanChecks: providertest.ExpectAction(keyAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New("id"), knownvalue.StringExact(id)),
					statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New("key"), knownvalue.Null()),
					statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New("user_id"), knownvalue.StringExact(userID)),
					statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New("expiration"), knownvalue.StringExact("2027-06-30")),
					statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New("expiration_date"), knownvalue.StringExact("2027-06-30")),
					statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New("hidden_key"), knownvalue.StringExact("************ci")),
				},
			},
			{
				// From then on, a change of user replaces the key.
				Config: providertest.ProviderConfig() + fmt.Sprintf(`
resource "mistral_api_key" "ci" {
  workspace_id = %q
  user_id      = "00000000-0000-4000-8000-0000000000cc"
  name         = "ci"
  expiration   = "2027-06-30"
}
`, workspaceID),
				ConfigPlanChecks: providertest.ExpectAction(keyAddr, plancheck.ResourceActionDestroyBeforeCreate),
			},
		},
	})
	f.AssertCalled(routeCreate, 1)
	f.AssertCalled(routeDelete, 2)
}

// Adding an expiration to a key created without one replaces the key: the
// API cannot set an expiry on an existing key, so recording the date in place
// would leave a key that never expires.
func TestAPIKeyResourceReplacesAKeyWhenAnExpirationIsAdded(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config:           keyConfig(""),
				ConfigPlanChecks: providertest.ExpectAction(keyAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New("expiration_date"), knownvalue.Null()),
				},
			},
			{
				Config:           keyConfig(`  expiration = "2027-06-30"`),
				ConfigPlanChecks: providertest.ExpectAction(keyAddr, plancheck.ResourceActionDestroyBeforeCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New("key"), knownvalue.StringExact("fake-secret-0002")),
					statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New("expiration"), knownvalue.StringExact("2027-06-30")),
					statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New("expiration_date"), knownvalue.StringExact("2027-06-30")),
				},
			},
		},
	})
	f.AssertJSONBodies(routeCreate, createBody("null", "null"), createBody("null", `"2027-06-30"`))
	// The replacement, then the destroy at the end of the test.
	f.AssertCalled(routeDelete, 2)
}

// An expiration added to a key still valid that already expires on or
// before that date, as a policy sets it, is recorded in place: the key meets
// the date, and a new key would expire no earlier. A key that expires later,
// that has expired or expires today, or an expiration_date the provider
// cannot compare, replaces the key, and so does a date unknown at plan time.
func TestAPIKeyResourceRecordsAnExpirationTheKeyAlreadyMeets(t *testing.T) {
	t.Parallel()
	const policyDate = "2026-11-28" // today in the fake, plus the 30 days of the policy
	cases := map[string]struct {
		policy           bool
		expirationDate   string // set on the key outside Terraform when not empty
		expiration       string // the expression added to the configuration
		action           plancheck.ResourceActionType
		wantExpiration   string
		wantExpiryDate   string
		wantCreatedCalls int
	}{
		"the date the key has": {true, "", `"2026-11-28"`, plancheck.ResourceActionUpdate, "2026-11-28", policyDate, 1},
		"a later date":         {true, "", `"2027-06-30"`, plancheck.ResourceActionUpdate, "2027-06-30", policyDate, 1},
		"an earlier date":      {true, "", `"2026-11-01"`, plancheck.ResourceActionDestroyBeforeCreate, "2026-11-01", "2026-11-01", 2},
		"a date and time":      {false, "2026-11-28T00:00:00Z", `"2027-06-30"`, plancheck.ResourceActionDestroyBeforeCreate, "2027-06-30", "2027-06-30", 2},
		"an unknown date":      {true, "", `terraform_data.expiry.output`, plancheck.ResourceActionDestroyBeforeCreate, "2026-11-28", policyDate, 2},
		// The clock of the provider is the fake's today, 2026-10-29 (TestMain).
		"an expired key":              {false, "2026-10-01", `"2027-06-30"`, plancheck.ResourceActionDestroyBeforeCreate, "2027-06-30", "2027-06-30", 2},
		"a key that expires today":    {false, "2026-10-29", `"2027-06-30"`, plancheck.ResourceActionDestroyBeforeCreate, "2027-06-30", "2027-06-30", 2},
		"a key that expires tomorrow": {false, "2026-10-30", `"2027-06-30"`, plancheck.ResourceActionUpdate, "2027-06-30", "2026-10-30", 1},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFake(t)
			if c.policy {
				f.setPolicy(30)
			}
			var id string
			expiry := "resource \"terraform_data\" \"expiry\" {\n  input = \"2026-11-28\"\n}\n"

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
				Steps: []resource.TestStep{
					{Config: keyConfig(""), Check: readKeyID(&id)},
					{
						PreConfig: func() {
							if c.expirationDate != "" {
								f.keys.Patch(id, map[string]any{"expiration_date": c.expirationDate})
							}
						},
						Config:           keyConfig("  expiration = "+c.expiration) + expiry,
						ConfigPlanChecks: providertest.ExpectAction(keyAddr, c.action),
						ConfigStateChecks: []statecheck.StateCheck{
							statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New("expiration"), knownvalue.StringExact(c.wantExpiration)),
							statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New("expiration_date"), knownvalue.StringExact(c.wantExpiryDate)),
						},
					},
				},
			})
			f.AssertCalled(routeCreate, c.wantCreatedCalls)
		})
	}
}

// The first apply after an import records an expiration in place only when
// the key meets it, as for a key Terraform created: an imported key that
// never expires is replaced, rather than kept with a date in the state that
// Mistral does not enforce.
func TestAPIKeyResourceReplacesAnImportedKeyThatOutlivesTheExpiration(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	id := f.seed("ci")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:           keyConfig("  name       = \"ci\"\n  expiration = \"2027-06-30\"") + "import {\n  to = mistral_api_key.ci\n  id = \"" + id + "\"\n}\n",
			ConfigPlanChecks: providertest.ExpectAction(keyAddr, plancheck.ResourceActionDestroyBeforeCreate),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New("key"), knownvalue.StringExact("fake-secret-0001")),
				statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New("expiration_date"), knownvalue.StringExact("2027-06-30")),
			},
		}},
	})
	f.AssertJSONBodies(routeCreate, createBody(`"ci"`, `"2027-06-30"`))
	if got := f.Requests(routeDelete)[0].Path; got != "/v1/admin/api-keys/"+id {
		t.Errorf("the replacement deleted %s, want the imported key %s", got, id)
	}
}

// With ignore_changes on user_id, an imported key keeps a null user_id in
// the state for good. Once its expiration is recorded, moving it later still
// renews the key.
func TestAPIKeyResourceRenewsAnImportedKeyWhoseUserIsIgnored(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	id := f.seed("ci")
	f.keys.Patch(id, map[string]any{"expiration_date": "2027-06-30"})
	config := func(expiration string) string {
		return keyConfig(fmt.Sprintf("  name       = \"ci\"\n  expiration = %q\n  lifecycle {\n    ignore_changes = [user_id]\n  }", expiration))
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config:           config("2027-06-30") + "import {\n  to = mistral_api_key.ci\n  id = \"" + id + "\"\n}\n",
				ConfigPlanChecks: providertest.ExpectAction(keyAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New("user_id"), knownvalue.Null()),
					statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New("expiration"), knownvalue.StringExact("2027-06-30")),
				},
			},
			{
				Config:           config("2028-06-30"),
				ConfigPlanChecks: providertest.ExpectAction(keyAddr, plancheck.ResourceActionDestroyBeforeCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New("expiration_date"), knownvalue.StringExact("2028-06-30")),
				},
			},
		},
	})
	f.AssertJSONBodies(routeCreate, createBody(`"ci"`, `"2028-06-30"`))
}

// Moving a configured expiration later renews the key: a replacement, even
// though the key already expires before the new date.
func TestAPIKeyResourceRenewsAKeyWhenItsExpirationMovesLater(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: keyConfig(`  expiration = "2027-06-30"`)},
			{
				Config:           keyConfig(`  expiration = "2028-06-30"`),
				ConfigPlanChecks: providertest.ExpectAction(keyAddr, plancheck.ResourceActionDestroyBeforeCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New("expiration_date"), knownvalue.StringExact("2028-06-30")),
				},
			},
		},
	})
	f.AssertJSONBodies(routeCreate, createBody("null", `"2027-06-30"`), createBody("null", `"2028-06-30"`))
}

// Only the first apply after an import records an expiration in place: once
// it has recorded user_id, adding an expiration replaces the key, as for a
// key Terraform created.
func TestAPIKeyResourceReplacesOnAnExpirationAddedAfterTheImport(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	id := f.seed("ci")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config:           keyConfig(`  name = "ci"`) + "import {\n  to = mistral_api_key.ci\n  id = \"" + id + "\"\n}\n",
				ConfigPlanChecks: providertest.ExpectAction(keyAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New("user_id"), knownvalue.StringExact(userID)),
					statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New("expiration"), knownvalue.Null()),
				},
			},
			{
				Config:           keyConfig("  name       = \"ci\"\n  expiration = \"2027-06-30\""),
				ConfigPlanChecks: providertest.ExpectAction(keyAddr, plancheck.ResourceActionDestroyBeforeCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New("key"), knownvalue.StringExact("fake-secret-0001")),
					statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New("expiration_date"), knownvalue.StringExact("2027-06-30")),
				},
			},
		},
	})
	f.AssertJSONBodies(routeCreate, createBody(`"ci"`, `"2027-06-30"`))
	if got := f.Requests(routeDelete)[0].Path; got != "/v1/admin/api-keys/"+id {
		t.Errorf("the replacement deleted %s, want the imported key %s", got, id)
	}
}

// A key deleted outside Terraform plans a create.
func TestAPIKeyResourceRecreatesAKeyDeletedOutsideTerraform(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	var id string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: keyConfig(`  name = "ci"`), Check: readKeyID(&id)},
			{
				PreConfig:        func() { f.keys.Delete(id) },
				Config:           keyConfig(`  name = "ci"`),
				ConfigPlanChecks: providertest.ExpectAction(keyAddr, plancheck.ResourceActionCreate),
			},
		},
	})
	f.AssertCalled(routeCreate, 2)
	f.AssertCalled(routeDelete, 1)
}

// A key renamed outside Terraform escapes the name filter: the refresh finds
// it in the whole list, and the plan replaces it, since no call renames a
// key.
func TestAPIKeyResourceFindsAKeyRenamedOutsideTerraform(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	var id string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: keyConfig(`  name = "ci"`), Check: readKeyID(&id)},
			{
				PreConfig:        func() { f.keys.Patch(id, map[string]any{"name": "renamed"}) },
				Config:           keyConfig(`  name = "ci"`),
				ConfigPlanChecks: providertest.ExpectAction(keyAddr, plancheck.ResourceActionDestroyBeforeCreate),
			},
		},
	})
	if got := f.Requests(routeDelete)[0].Path; got != "/v1/admin/api-keys/"+id {
		t.Errorf("the replacement deleted %s, want the renamed key %s", got, id)
	}
}

// The name filter matches part of a name: with 120 other keys whose name
// contains it, the key is on the second page.
func TestAPIKeyResourceWalksThePagesOfTheList(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	for i := range 120 {
		f.seed(fmt.Sprintf("ci-%03d", i))
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:           keyConfig(`  name = "ci"`),
			ConfigPlanChecks: providertest.ExpectAction(keyAddr, plancheck.ResourceActionCreate),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New("hidden_key"), knownvalue.StringExact("************0001")),
			},
		}},
	})
	var secondPage int
	for _, r := range f.Requests(routeList) {
		if r.Query.Encode() == "limit=100&name=ci&offset=100" {
			secondPage++
		}
	}
	if secondPage == 0 {
		t.Error("no read reached the second page")
	}
}

// A create that meets a 5xx is sent once: the key may exist, and a replay
// would create a second one.
func TestAPIKeyResourceCreateIsSentOnceOnA5xx(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.FailNext(routeCreate, 502, 1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      keyConfig(""),
			ExpectError: regexp.MustCompile(`Cannot\s+create\s+the\s+API\s+key(.|\n)*502`),
		}},
	})
	f.AssertCalled(routeCreate, 1)
}

// Read and delete errors of the API reach the user.
func TestAPIKeyResourceReportsErrors(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: keyConfig(`  name = "ci"`)},
			{
				PreConfig:   func() { f.FailNext(routeList, 500, 4) },
				Config:      keyConfig(`  name = "ci"`),
				ExpectError: regexp.MustCompile(`Cannot\s+read\s+the\s+API\s+key(.|\n)*500`),
			},
			{
				PreConfig:   func() { f.FailNextWith(routeDelete, 403, "Forbidden", 1) },
				Config:      keyConfig(`  name = "ci"`),
				Destroy:     true,
				ExpectError: regexp.MustCompile(`Cannot\s+delete\s+the\s+API\s+key(.|\n)*403`),
			},
		},
	})
}

// Invalid arguments fail at plan time, before any call.
func TestAPIKeyResourceValidatesItsArguments(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	cases := map[string]struct{ body, err string }{
		"not a date":    {`  expiration = "30/06/2027"`, `Invalid\s+date(.|\n)*YYYY-MM-DD`},
		"not a day":     {`  expiration = "2027-02-30"`, `Invalid\s+date`},
		"date and time": {`  expiration = "2027-06-30T00:00:00Z"`, `Invalid\s+date`},
		"empty name":    {`  name = ""`, `character\s+count\s+must\s+be\s+between\s+1\s+and\s+255`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
				Steps: []resource.TestStep{{
					Config:      keyConfig(c.body),
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(c.err),
				}},
			})
		})
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      providertest.ProviderConfig() + "resource \"mistral_api_key\" \"ci\" {\n  user_id = \"\"\n}\n",
			PlanOnly:    true,
			ExpectError: regexp.MustCompile(`"workspace_id"\s+is\s+required`),
		}},
	})
	f.AssertCalled("", 0)
}

// Importing an ID no key has fails as Terraform words it, not with a
// framework error about the missing identity.
func TestAPIKeyResourceImportOfAMissingKey(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.seed("other")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:        keyConfig(""),
			ResourceName:  keyAddr,
			ImportState:   true,
			ImportStateId: sdktest.NewUUID(),
			ExpectError:   regexp.MustCompile(`Cannot\s+import\s+non-existent\s+remote\s+object`),
		}},
	})
	f.AssertCalled(routeCreate, 0)
}
