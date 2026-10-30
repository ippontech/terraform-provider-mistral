// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package spendlimits_test

import (
	"context"
	"encoding/json"
	"math/big"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-mistral/internal/providertest"
	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

const limitAddr = "mistral_organization_spend_limit.this"

// limitConfig is a configuration with the spend limit and the arguments of
// body.
func limitConfig(body string) string {
	return providertest.ProviderConfig() + "resource \"mistral_organization_spend_limit\" \"this\" {\n" + body + "\n}\n"
}

// limitChecks are the exact state checks of the spend limit, given its
// amount and its flag.
func limitChecks(amount, noLimit knownvalue.Check) []statecheck.StateCheck {
	return []statecheck.StateCheck{
		statecheck.ExpectKnownValue(limitAddr, tfjsonpath.New("id"), knownvalue.StringExact("organization")),
		statecheck.ExpectKnownValue(limitAddr, tfjsonpath.New("amount"), amount),
		statecheck.ExpectKnownValue(limitAddr, tfjsonpath.New("no_monthly_limit"), noLimit),
		statecheck.ExpectKnownValue(limitAddr, tfjsonpath.New("currency"), knownvalue.StringExact("EUR")),
	}
}

// The organization starts with no limit. Terraform sets one, raises it,
// removes it, sets one again, and leaves the last one at Mistral on
// destroy.
func TestOrganizationSpendLimitResourceLifecycle(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config:            limitConfig(`  amount = 500`),
				ConfigPlanChecks:  providertest.ExpectAction(limitAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: limitChecks(knownvalue.Int64Exact(500), knownvalue.Bool(false)),
			},
			{
				Config:            limitConfig(`  amount = 1000`),
				ConfigPlanChecks:  providertest.ExpectAction(limitAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: limitChecks(knownvalue.Int64Exact(1000), knownvalue.Bool(false)),
			},
			{
				// The amount the API requires goes with the current limit.
				Config:            limitConfig(`  no_monthly_limit = true`),
				ConfigPlanChecks:  providertest.ExpectAction(limitAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: limitChecks(knownvalue.Null(), knownvalue.Bool(true)),
			},
			{
				Config:            limitConfig(`  amount = 750`),
				ConfigPlanChecks:  providertest.ExpectAction(limitAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: limitChecks(knownvalue.Int64Exact(750), knownvalue.Bool(false)),
			},
			{
				ResourceName:      limitAddr,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				ResourceName:    limitAddr,
				ImportState:     true,
				ImportStateKind: resource.ImportBlockWithResourceIdentity,
			},
		},
	})

	f.AssertJSONBodies(routeSetLimit,
		`{"amount": 500, "no_monthly_limit": false}`,
		`{"amount": 1000, "no_monthly_limit": false}`,
		`{"amount": 1000, "no_monthly_limit": true}`,
		`{"amount": 750, "no_monthly_limit": false}`)
	// The destroy called nothing: the limit stays.
	if noLimit, limit := f.limit(); noLimit || limit != json.Number("750") {
		t.Errorf("limit after destroy = %v, %v; want 750 kept", noLimit, limit)
	}
	f.AssertCredential("", sdktest.AdminKey)
}

// A limit set in the Admin Panel is not overwritten by a create: the create
// fails with the import block, and the import brings it under Terraform.
func TestOrganizationSpendLimitResourceRefusesToOverwriteALimit(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.seed(false, 300)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config: limitConfig(`  amount = 500`),
				ExpectError: regexp.MustCompile(`Organization\s+spend\s+limit\s+already\s+set(.|\n)*300\s+EUR` +
					`(.|\n)*import\s+block\s+\(id\s+=\s+"organization"\)`),
			},
			{
				// Removing the limit would overwrite it too.
				Config:      limitConfig(`  no_monthly_limit = true`),
				ExpectError: regexp.MustCompile(`Organization\s+spend\s+limit\s+already\s+set`),
			},
			{
				Config: providertest.ProviderConfig() + `
import {
  to = mistral_organization_spend_limit.this
  id = "organization"
}

resource "mistral_organization_spend_limit" "this" {
  amount = 500
}
`,
				ConfigPlanChecks:  providertest.ExpectAction(limitAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: limitChecks(knownvalue.Int64Exact(500), knownvalue.Bool(false)),
			},
		},
	})
	f.AssertJSONBodies(routeSetLimit, `{"amount": 500, "no_monthly_limit": false}`)
}

// A limit equal to the configuration, or no limit at all, is taken as is:
// writing it loses nothing.
func TestOrganizationSpendLimitResourceTakesAnEqualLimit(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	// The API types usage_limit as a number: 500.0 is the same limit.
	f.seed(false, json.Number("500.0"))

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:            limitConfig(`  amount = 500`),
			ConfigPlanChecks:  providertest.ExpectAction(limitAddr, plancheck.ResourceActionCreate),
			ConfigStateChecks: limitChecks(knownvalue.Int64Exact(500), knownvalue.Bool(false)),
		}},
	})
	f.AssertJSONBodies(routeSetLimit, `{"amount": 500, "no_monthly_limit": false}`)
}

// An organization with no limit, whose usage_limit still holds the last
// one, keeps no limit: the amount the API requires is that last limit.
func TestOrganizationSpendLimitResourceKeepsNoLimit(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.seed(true, 2000)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:            limitConfig(`  no_monthly_limit = true`),
			ConfigPlanChecks:  providertest.ExpectAction(limitAddr, plancheck.ResourceActionCreate),
			ConfigStateChecks: limitChecks(knownvalue.Null(), knownvalue.Bool(true)),
		}},
	})
	f.AssertJSONBodies(routeSetLimit, `{"amount": 2000, "no_monthly_limit": true}`)
}

// An organization with no limit and no whole amount in usage_limit, none
// or a fractional one, keeps no limit without a POST: the POST requires an
// amount, the organization holds none to send, and 0 could suspend it.
func TestOrganizationSpendLimitResourceTakesNoLimitWithoutAnAmount(t *testing.T) {
	t.Parallel()

	for name, usageLimit := range map[string]any{"no usage_limit": nil, "fractional usage_limit": 99.5} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFake(t)
			f.seed(true, usageLimit)

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
				Steps: []resource.TestStep{{
					Config:            limitConfig(`  no_monthly_limit = true`),
					ConfigPlanChecks:  providertest.ExpectAction(limitAddr, plancheck.ResourceActionCreate),
					ConfigStateChecks: limitChecks(knownvalue.Null(), knownvalue.Bool(true)),
				}},
			})
			f.AssertCalled(routeSetLimit, 0)
		})
	}
}

// An organization whose no_monthly_limit is false without a usage_limit has
// no amount to send with the flag: removing the limit fails, on create and
// on update, rather than sending 0, until a whole limit is set.
func TestOrganizationSpendLimitResourceNeverSendsAZeroAmount(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	noAmount := regexp.MustCompile(`No\s+amount\s+to\s+send\s+with\s+no_monthly_limit(.|\n)*never\s+0`)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				PreConfig:   func() { f.seed(false, nil) },
				Config:      limitConfig(`  no_monthly_limit = true`),
				ExpectError: noAmount,
			},
			{
				Config:            limitConfig(`  amount = 500`),
				ConfigStateChecks: limitChecks(knownvalue.Int64Exact(500), knownvalue.Bool(false)),
			},
			{
				// The refresh reads the limit without an amount.
				PreConfig:   func() { f.seed(false, nil) },
				Config:      limitConfig(`  no_monthly_limit = true`),
				ExpectError: noAmount,
			},
			{
				Config: limitConfig(`  amount = 500`),
			},
			{
				Config:            limitConfig(`  no_monthly_limit = true`),
				ConfigStateChecks: limitChecks(knownvalue.Null(), knownvalue.Bool(true)),
			},
		},
	})
	f.AssertJSONBodies(routeSetLimit,
		`{"amount": 500, "no_monthly_limit": false}`,
		`{"amount": 500, "no_monthly_limit": false}`,
		`{"amount": 500, "no_monthly_limit": true}`)
}

// A limit changed in the Admin Panel plans an update that writes the
// configured limit back.
func TestOrganizationSpendLimitResourceRevertsALimitChangedOutsideTerraform(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: limitConfig(`  amount = 500`)},
			{
				PreConfig:         func() { f.seed(false, 800) },
				Config:            limitConfig(`  amount = 500`),
				ConfigPlanChecks:  providertest.ExpectAction(limitAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: limitChecks(knownvalue.Int64Exact(500), knownvalue.Bool(false)),
			},
		},
	})
	f.AssertCalled(routeSetLimit, 2)
}

// A POST that meets a 5xx is sent once: the API documents no idempotency.
func TestOrganizationSpendLimitResourceCreateIsSentOnceOnA5xx(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	f.FailNext(routeSetLimit, 500, 1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config:      limitConfig(`  amount = 500`),
			ExpectError: regexp.MustCompile(`Cannot\s+set\s+the\s+organization\s+spend\s+limit(.|\n)*500`),
		}},
	})
	f.AssertCalled(routeSetLimit, 1)
}

// A read that fails before the create, or on a refresh, reaches the user.
func TestOrganizationSpendLimitResourceReportsReadErrors(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				PreConfig:   func() { f.FailNextWith(routeGetLimits, 403, "Forbidden", 1) },
				Config:      limitConfig(`  amount = 500`),
				ExpectError: regexp.MustCompile(`Cannot\s+read\s+the\s+organization\s+spend\s+limit(.|\n)*403`),
			},
			{Config: limitConfig(`  amount = 500`)},
			{
				PreConfig:   func() { f.FailNext(routeGetLimits, 500, 4) },
				Config:      limitConfig(`  amount = 500`),
				ExpectError: regexp.MustCompile(`Cannot\s+read\s+the\s+organization\s+spend\s+limit(.|\n)*500`),
			},
		},
	})
}

// A limit with a fractional part, which the POST cannot set, fails the
// read instead of being rounded; a create that meets it fails the same way.
func TestOrganizationSpendLimitResourceRefusesAFractionalLimit(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				PreConfig:   func() { f.seed(false, 499.99) },
				Config:      limitConfig(`  amount = 500`),
				ExpectError: regexp.MustCompile(`Unexpected\s+organization\s+spend\s+limit(.|\n)*499.99(.|\n)*whole`),
			},
			{
				PreConfig: func() { f.seed(true, nil) },
				Config:    limitConfig(`  amount = 500`),
			},
			{
				PreConfig:   func() { f.seed(false, 1e300) },
				Config:      limitConfig(`  amount = 500`),
				ExpectError: regexp.MustCompile(`Unexpected\s+organization\s+spend\s+limit`),
			},
			{
				// Once the limit is whole again, the read recovers.
				PreConfig: func() { f.seed(false, 500) },
				Config:    limitConfig(`  amount = 500`),
			},
		},
	})
}

// Exactly one of amount and no_monthly_limit = true, and no negative amount,
// are checked at plan time, before any call.
func TestOrganizationSpendLimitResourceValidatesTheArguments(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	cases := map[string]struct{ body, err string }{
		"neither":         {``, `Missing\s+spend\s+limit`},
		"no limit false":  {`  no_monthly_limit = false`, `Missing\s+spend\s+limit`},
		"both":            {"  amount = 500\n  no_monthly_limit = true", `Conflicting\s+spend\s+limit`},
		"negative amount": {`  amount = -1`, `value\s+must\s+be\s+at\s+least\s+0`},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
				Steps:                    []resource.TestStep{{Config: limitConfig(c.body), PlanOnly: true, ExpectError: regexp.MustCompile(c.err)}},
			})
		})
	}
	f.AssertCalled("", 0)
}

// An amount unknown at plan time is checked at apply time.
func TestOrganizationSpendLimitResourceAcceptsAnAmountUnknownAtPlanTime(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig() + `
resource "terraform_data" "budget" {
  input = 1200
}

resource "mistral_organization_spend_limit" "this" {
  amount = terraform_data.budget.output
}
`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(limitAddr, tfjsonpath.New("amount"), knownvalue.Int64Exact(1200)),
			},
		}},
	})
}

// A no_monthly_limit unknown at plan time is checked at apply time, not
// taken for false: the plan does not ask for an amount.
func TestOrganizationSpendLimitResourceAcceptsAFlagUnknownAtPlanTime(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig() + `
resource "terraform_data" "uncapped" {
  input = true
}

resource "mistral_organization_spend_limit" "this" {
  no_monthly_limit = terraform_data.uncapped.output
}
`,
			ConfigStateChecks: limitChecks(knownvalue.Null(), knownvalue.Bool(true)),
		}},
	})
}

// A no_monthly_limit that stays true, but that the plan only learns at
// apply time, plans an update and sends nothing: the organization already
// has no limit, and the state holds no amount to send with the flag.
// Terraform 1.17 plans again at apply time and skips the resource, so
// Update is not called; TestUpdateKeepsNoLimitWithoutAPost calls it.
func TestOrganizationSpendLimitResourceKeepsNoLimitLearntAtApplyTime(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config:            limitConfig(`  no_monthly_limit = true`),
				ConfigStateChecks: limitChecks(knownvalue.Null(), knownvalue.Bool(true)),
			},
			{
				Config: providertest.ProviderConfig() + `
resource "terraform_data" "uncapped" {
  input = true
}

resource "mistral_organization_spend_limit" "this" {
  no_monthly_limit = terraform_data.uncapped.output
}
`,
				ConfigPlanChecks:  providertest.ExpectAction(limitAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: limitChecks(knownvalue.Null(), knownvalue.Bool(true)),
			},
		},
	})
	f.AssertCalled(routeSetLimit, 0)
}

// The import ID is the fixed word organization, and nothing else.
func TestOrganizationSpendLimitResourceRejectsAnotherImportID(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: limitConfig(`  amount = 500`)},
			{
				ResourceName:  limitAddr,
				ImportState:   true,
				ImportStateId: "org-1",
				ExpectError:   regexp.MustCompile(`Invalid\s+import\s+ID(.|\n)*always\s+"organization",\s+got\s+"org-1"`),
			},
		},
	})
}

// When an update fails, the state Terraform saves is the prior one, not the
// plan: terraform-plugin-framework v1.19.0 fills the response of Update with
// the prior state ("Require explicit provider updates for tracking
// successful updates", fwserver.UpdateResource), and Update writes the state
// only after a POST succeeds. A test step cannot show it: a failed step stops
// before its state checks, and the next refresh reads the fake again. So the
// test sends ApplyResourceChange to the provider server itself.
func TestOrganizationSpendLimitResourceKeepsThePriorStateWhenAnUpdateFails(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		seedLimit      any
		prior, planned limitValues
		failPost       bool
		wantError      string
		wantPosts      int
	}{
		"refused without an amount": {nil, limitValues{nil, false}, limitValues{nil, true}, false, "No amount to send with no_monthly_limit", 0},
		"POST error":                {500, limitValues{big.NewFloat(500), false}, limitValues{big.NewFloat(800), false}, true, "Cannot set the organization spend limit", 1},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFake(t)
			f.seed(false, c.seedLimit)
			if c.failPost {
				f.FailNext(routeSetLimit, 500, 1)
			}

			newState, diags := applyUpdate(t, f, c.prior, c.planned)

			if len(diags) != 1 || diags[0].Severity != tfprotov6.DiagnosticSeverityError || diags[0].Summary != c.wantError {
				t.Fatalf("diagnostics = %+v, want one error %q", diags, c.wantError)
			}
			if !newState.Equal(c.prior.value()) {
				t.Errorf("state after the failed update = %s, want the prior state %s", newState, c.prior.value())
			}
			f.AssertCalled(routeSetLimit, c.wantPosts)
		})
	}
}

// limitValues are the arguments of a spend limit state: amount, nil for
// null, and no_monthly_limit. Its id is organization and its currency EUR.
type limitValues struct {
	amount  *big.Float
	noLimit bool
}

// limitType is the type of a spend limit state.
var limitType = tftypes.Object{AttributeTypes: map[string]tftypes.Type{
	"id": tftypes.String, "amount": tftypes.Number, "no_monthly_limit": tftypes.Bool, "currency": tftypes.String,
}}

// value is the state of v.
func (v limitValues) value() tftypes.Value {
	var amount any
	if v.amount != nil {
		amount = v.amount
	}
	return tftypes.NewValue(limitType, map[string]tftypes.Value{
		"id": tftypes.NewValue(tftypes.String, "organization"), "amount": tftypes.NewValue(tftypes.Number, amount),
		"no_monthly_limit": tftypes.NewValue(tftypes.Bool, v.noLimit), "currency": tftypes.NewValue(tftypes.String, "EUR"),
	})
}

// config is the configuration of v: its id and currency are computed, so null.
func (v limitValues) config() tftypes.Value {
	var amount any
	if v.amount != nil {
		amount = v.amount
	}
	return tftypes.NewValue(limitType, map[string]tftypes.Value{
		"id": tftypes.NewValue(tftypes.String, nil), "amount": tftypes.NewValue(tftypes.Number, amount),
		"no_monthly_limit": tftypes.NewValue(tftypes.Bool, v.noLimit), "currency": tftypes.NewValue(tftypes.String, nil),
	})
}

// applyUpdate configures the provider of the fake with the Admin API key and
// sends it the ApplyResourceChange of an update of the spend limit from prior
// to planned, as Terraform does. It returns the new state and the
// diagnostics of the answer.
func applyUpdate(t *testing.T, f *fake, prior, planned limitValues) (tftypes.Value, []*tfprotov6.Diagnostic) {
	t.Helper()
	ctx := context.Background()
	server, err := providertest.ProviderFactories(f.Server)["mistral"]()
	if err != nil {
		t.Fatal(err)
	}
	schemas, err := server.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if got := schemas.ResourceSchemas["mistral_organization_spend_limit"].ValueType(); !got.Equal(limitType) {
		t.Fatalf("the schema of the spend limit is %s, the test builds %s", got, limitType)
	}
	providerType := schemas.Provider.ValueType().(tftypes.Object)
	settings := map[string]tftypes.Value{}
	for name, typ := range providerType.AttributeTypes {
		settings[name] = tftypes.NewValue(typ, nil)
	}
	settings["admin_api_key"] = tftypes.NewValue(tftypes.String, sdktest.TestAdminKey)
	configured, err := server.ConfigureProvider(ctx, &tfprotov6.ConfigureProviderRequest{
		Config: dynamicValue(t, tftypes.NewValue(providerType, settings)),
	})
	if err != nil || len(configured.Diagnostics) > 0 {
		t.Fatalf("ConfigureProvider: %v %+v", err, configured)
	}
	applied, err := server.ApplyResourceChange(ctx, &tfprotov6.ApplyResourceChangeRequest{
		TypeName:     "mistral_organization_spend_limit",
		PriorState:   dynamicValue(t, prior.value()),
		PlannedState: dynamicValue(t, planned.value()),
		Config:       dynamicValue(t, planned.config()),
	})
	if err != nil {
		t.Fatal(err)
	}
	newState, err := applied.NewState.Unmarshal(limitType)
	if err != nil {
		t.Fatal(err)
	}
	return newState, applied.Diagnostics
}

// dynamicValue encodes v for the provider protocol.
func dynamicValue(t *testing.T, v tftypes.Value) *tfprotov6.DynamicValue {
	t.Helper()
	dv, err := tfprotov6.NewDynamicValue(v.Type(), v)
	if err != nil {
		t.Fatal(err)
	}
	return &dv
}
