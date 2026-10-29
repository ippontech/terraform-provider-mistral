// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package providertest_test

import (
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

func TestExpectAction(t *testing.T) {
	t.Parallel()
	checks := providertest.ExpectAction("mistral_agent.x", plancheck.ResourceActionCreate)
	if len(checks.PreApply) != 1 || len(checks.PostApplyPostRefresh) != 1 {
		t.Errorf("ExpectAction = %+v", checks)
	}
}
