// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package spendlimits_test

import (
	"context"
	"os"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-mistral/internal/acctest"
	"github.com/ippontech/terraform-provider-mistral/internal/client"
)

// TestAccOrganizationSpendLimitResource takes the spend limit of the test
// organization as it is, raises it, imports it, and puts it back, against
// the live API. The destroy leaves the limit at Mistral, so the organization
// ends with the limit it had: a test that lowered it could suspend the
// organization's API access.
func TestAccOrganizationSpendLimitResource(t *testing.T) {
	acctest.PreCheckAdmin(t)
	current, err := client.New(client.Config{AdminAPIKey: os.Getenv("MISTRAL_ADMIN_API_KEY")}).GetOrganizationLimits(context.Background())
	if err != nil {
		t.Fatalf("read the spend limit of the test organization: %v", err)
	}
	completion := current.Limits.Completion
	original, raised := "  no_monthly_limit = true", "  amount = 10000000"
	if !completion.NoMonthlyLimit && completion.UsageLimit != nil {
		amount, err := completion.UsageLimit.Int64()
		if err != nil {
			t.Fatalf("the test organization has a limit of %s, which is not whole", completion.UsageLimit)
		}
		original, raised = "  amount = "+strconv.FormatInt(amount, 10), "  amount = "+strconv.FormatInt(amount+1, 10)
	}
	config := func(body string) string {
		return "resource \"mistral_organization_spend_limit\" \"this\" {\n" + body + "\n}\n"
	}
	addr := "mistral_organization_spend_limit.this"

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config(original),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(addr, tfjsonpath.New("id"), knownvalue.StringExact("organization")),
					statecheck.ExpectKnownValue(addr, tfjsonpath.New("currency"), knownvalue.NotNull()),
				},
			},
			{Config: config(raised)},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true},
			{Config: config(original)},
		},
	})
}
