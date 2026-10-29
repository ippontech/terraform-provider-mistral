// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package serviceaccounts_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-mistral/internal/providertest"
)

// A create that meets a 5xx is sent once: the account may exist, and a replay
// would make a second one or fail on the name.
func TestServiceAccountResourceCreateIsSentOnceOnA5xx(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.FailNext(routeCreate, 500, 1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      accountConfig(minimalAccount),
			ExpectError: regexp.MustCompile(`Cannot\s+create\s+the\s+service\s+account(.|\n)*500`),
		}},
	})
	f.AssertCalled(routeCreate, 1)
}

// A name taken in the workspace fails the create with the way to import the
// account, rather than adopting it.
func TestServiceAccountResourceRefusesATakenName(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.seed(workspaceID, "prod-worker", nil, roleExecutor)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      accountConfig(minimalAccount),
			ExpectError: regexp.MustCompile(`Service\s+account\s+already\s+exists(.|\n)*named\s+"prod-worker"(.|\n)*import\s+block(.|\n)*409`),
		}},
	})
	f.AssertCalled(routeCreate, 1)
}

// When the PATCH of the description succeeds and the PUT of the roles fails,
// the state keeps the new description: the next apply sends the roles only.
func TestServiceAccountResourceKeepsAPartialUpdate(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	both := accountConfig(`
  name        = "prod-worker"
  description = "Runs the workflow workers"
  role_ids    = ["` + roleExecutor + `", "` + roleDev + `"]
`)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: accountConfig(minimalAccount)},
			{
				PreConfig:   func() { f.FailNextWith(routeSetRoles, 403, "Forbidden", 1) },
				Config:      both,
				ExpectError: regexp.MustCompile(`Cannot\s+set\s+the\s+roles\s+of\s+the\s+service\s+account(.|\n)*403`),
			},
			{
				Config:           both,
				ConfigPlanChecks: providertest.ExpectAction(accountAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(accountAddr, tfjsonpath.New("description"), knownvalue.StringExact("Runs the workflow workers")),
					statecheck.ExpectKnownValue(accountAddr, tfjsonpath.New("role_ids"), roleSet(roleExecutor, roleDev)),
				},
			},
		},
	})
	// The second apply did not send the description again.
	f.AssertCalled(routeUpdate, 1)
	f.AssertCalled(routeSetRoles, 2)
}

// An update that meets a 5xx is not replayed; a PUT of the roles, a full
// replace, is.
func TestServiceAccountResourceRetriesOnlyTheRoles(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: accountConfig(minimalAccount)},
			{
				PreConfig:   func() { f.FailNext(routeUpdate, 503, 1) },
				Config:      accountConfig(minimalAccount + `  description = "changed"`),
				ExpectError: regexp.MustCompile(`Cannot\s+update\s+the\s+service\s+account(.|\n)*503`),
			},
			{
				PreConfig: func() { f.FailNext(routeSetRoles, 503, 1) },
				Config: accountConfig(`
  name     = "prod-worker"
  role_ids = ["` + roleDev + `"]
`),
				ConfigPlanChecks: providertest.ExpectAction(accountAddr, plancheck.ResourceActionUpdate),
			},
		},
	})
	f.AssertCalled(routeUpdate, 1)
	f.AssertCalled(routeSetRoles, 2)
}

// TestServiceAccountResourceReportsReadErrors checks that an error of either
// read call is reported, with what the API said.
func TestServiceAccountResourceReportsReadErrors(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: accountConfig(minimalAccount)},
			{
				PreConfig:   func() { f.FailNextWith(routeGet, 401, "Invalid API Key", 1) },
				Config:      accountConfig(minimalAccount),
				ExpectError: regexp.MustCompile(`Cannot\s+read\s+the\s+service\s+account(.|\n)*401(.|\n)*Invalid\s+API\s+Key`),
			},
			{
				PreConfig:   func() { f.FailNext(routeListRoles, 500, 4) },
				Config:      accountConfig(minimalAccount),
				ExpectError: regexp.MustCompile(`Cannot\s+read\s+the\s+roles\s+of\s+the\s+service\s+account(.|\n)*500`),
			},
		},
	})
}

// TestServiceAccountResourceReportsDeleteErrors checks that a delete still
// failing after its retries is reported.
func TestServiceAccountResourceReportsDeleteErrors(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: accountConfig(minimalAccount)},
			{
				PreConfig:   func() { f.FailNext(routeDelete, 500, 4) },
				Config:      accountConfig(minimalAccount),
				Destroy:     true,
				ExpectError: regexp.MustCompile(`Cannot\s+delete\s+the\s+service\s+account(.|\n)*500`),
			},
		},
	})
}

// The limits of the guide and of the specification are refused at plan time,
// before any call.
func TestServiceAccountResourceValidatesItsArguments(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	fifteen := make([]string, 15)
	for i := range fifteen {
		fifteen[i] = `"role-` + string(rune('a'+i)) + `"`
	}

	cases := map[string]struct{ body, err string }{
		"mixed-case name":        {`name = "Prod-Worker"` + "\n" + `role_ids = ["r"]`, `lowercase\s+letter`},
		"one-character name":     {`name = "p"` + "\n" + `role_ids = ["r"]`, `2\s+to\s+63\s+characters`},
		"name starting a digit":  {`name = "1-worker"` + "\n" + `role_ids = ["r"]`, `lowercase\s+letter`},
		"64-character name":      {`name = "` + strings.Repeat("a", 64) + `"` + "\n" + `role_ids = ["r"]`, `2\s+to\s+63\s+characters`},
		"no role":                {`name = "prod-worker"` + "\n" + `role_ids = []`, `set\s+must\s+contain\s+at\s+least\s+1`},
		"fifteen roles":          {`name = "prod-worker"` + "\n" + `role_ids = [` + strings.Join(fifteen, ", ") + `]`, `at\s+most\s+14`},
		"empty role ID":          {`name = "prod-worker"` + "\n" + `role_ids = [""]`, `at\s+least\s+1`},
		"empty description":      {minimalAccount + `description = ""`, `length\s+must\s+be\s+between\s+1\s+and\s+4096`},
		"too long a description": {minimalAccount + `description = "` + strings.Repeat("d", 4097) + `"`, `length\s+must\s+be\s+between\s+1\s+and\s+4096`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
				Steps: []resource.TestStep{{
					Config:      accountConfig(c.body),
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(c.err),
				}},
			})
		})
	}
	t.Run("empty workspace", func(t *testing.T) {
		t.Parallel()
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
			Steps: []resource.TestStep{{
				Config: providertest.ProviderConfig() + `
resource "mistral_service_account" "worker" {
  workspace_id = ""
` + minimalAccount + `
}
`,
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`workspace_id(.|\n)*at\s+least\s+1`),
			}},
		})
	})
	t.Cleanup(func() { f.AssertCalled("", 0) })
}
