// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package providertest_test

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"

	"github.com/ippontech/terraform-provider-mistral/internal/providertest"
	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

// TestProviderRunsAgainstTheFake configures the provider with the dummy keys
// and applies a configuration that calls nothing.
func TestProviderRunsAgainstTheFake(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig() + `
resource "terraform_data" "x" {
  input = "x"
}
`,
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
			},
		}},
	})
	srv.AssertCalled("", 0)
}

// TestProviderFactoriesIgnoreTheEnvironment runs a configuration without
// keys with both keys in the environment: the data sources still miss their
// credential, and nothing reaches the fake. It cannot call t.Parallel, since
// t.Setenv refuses a parallel test; Go runs it before the parallel tests of
// the package, and restores the environment when it ends.
func TestProviderFactoriesIgnoreTheEnvironment(t *testing.T) {
	t.Setenv("MISTRAL_API_KEY", "env-key-must-not-be-read")
	t.Setenv("MISTRAL_ADMIN_API_KEY", "env-admin-key-must-not-be-read")
	srv := sdktest.NewServer(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv),
		Steps: []resource.TestStep{
			{
				Config:      "provider \"mistral\" {}\n\ndata \"mistral_models\" \"all\" {}\n",
				ExpectError: regexp.MustCompile(`Missing\s+Mistral\s+credential(.|\n)*"api_key"`),
			},
			{
				Config:      "provider \"mistral\" {}\n\ndata \"mistral_workspaces\" \"all\" {}\n",
				ExpectError: regexp.MustCompile(`Missing\s+Mistral\s+credential(.|\n)*"admin_api_key"`),
			},
		},
	})
	srv.AssertCalled("", 0)
}

func TestExpectAction(t *testing.T) {
	t.Parallel()
	checks := providertest.ExpectAction("mistral_agent.x", plancheck.ResourceActionCreate)
	if len(checks.PreApply) != 1 || len(checks.PostApplyPostRefresh) != 1 {
		t.Errorf("ExpectAction = %+v", checks)
	}
}
