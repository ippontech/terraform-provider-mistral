// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package providertest runs the provider against the fake API of
// internal/sdktest in resource.UnitTest tests: provider factories pointed at
// a sdktest.Server, the provider configuration with the dummy keys, and the
// plan checks every step uses.
//
// It imports internal/provider, which imports every service, so only
// external test packages (package <area>_test) can use it.
package providertest

import (
	"fmt"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
	"github.com/ippontech/terraform-provider-mistral/internal/provider"
	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

// RetryPolicy is the retry policy of the provider under test: the production
// rules, with millisecond delays.
var RetryPolicy = client.RetryPolicy{
	MaxRetries:    client.DefaultRetryPolicy.MaxRetries,
	BaseDelay:     time.Millisecond,
	MaxDelay:      5 * time.Millisecond,
	MaxRetryAfter: 10 * time.Millisecond,
}

// ProviderFactories returns the factories of a provider whose client calls
// srv. No environment variable is read for the server URL, so tests can run
// in parallel.
func ProviderFactories(srv *sdktest.Server) map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"mistral": providerserver.NewProtocol6WithError(
			provider.New("test", provider.WithServerURL(srv.URL), provider.WithRetryPolicy(RetryPolicy))()),
	}
}

// ProviderConfig is the provider block of a test configuration. It sets both
// keys to the dummy values of sdktest, so the environment of the developer
// never fills them.
func ProviderConfig() string {
	return fmt.Sprintf("provider \"mistral\" {\n  api_key       = %q\n  admin_api_key = %q\n}\n",
		sdktest.TestAPIKey, sdktest.TestAdminKey)
}

// ExpectAction is the plan check of an apply step: the planned action of
// addr before the apply, and an empty plan after it, once refreshed.
func ExpectAction(addr string, action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{
		PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, action)},
		PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
	}
}
