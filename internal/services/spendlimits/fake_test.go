// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package spendlimits_test

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

const (
	routeGetLimits = "GET /v1/admin/spend-limit"
	routeSetLimit  = "POST /v1/admin/spend-limit"
)

// fake serves the organization spend limit of the Admin API
// (https://docs.mistral.ai/api/endpoint/beta/admin/billing): one limit per
// organization, read and set, never deleted.
//
// Besides the transport (LimitsOUT on both routes), it holds what the
// provider depends on, from the usage metrics guide
// (https://docs.mistral.ai/admin/admin-api/usage-metrics): amount is a whole
// number in the billing currency, and no_monthly_limit = true removes the
// limit. With the flag, the fake still stores the amount as usage_limit,
// since what the API answers there is not documented: the provider must
// ignore it.
type fake struct {
	*sdktest.Server

	mu             sync.Mutex
	noMonthlyLimit bool
	// usageLimit is the limit as the API writes it, a JSON number or nil.
	usageLimit any
}

// newFake returns a fake whose organization starts with no monthly limit,
// billed in euros.
func newFake(t *testing.T) *fake {
	t.Helper()
	f := &fake{Server: sdktest.NewServer(t), noMonthlyLimit: true}
	f.Handle(routeGetLimits, f.get)
	f.Handle(routeSetLimit, f.set)
	return f
}

// seed sets the limit of the organization outside Terraform: a JSON number,
// or nil with noMonthlyLimit.
func (f *fake) seed(noMonthlyLimit bool, usageLimit any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.noMonthlyLimit, f.usageLimit = noMonthlyLimit, usageLimit
}

// limit returns the limit the organization has now.
func (f *fake) limit() (bool, any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.noMonthlyLimit, f.usageLimit
}

// get answers the limits of the organization.
func (f *fake) get(w http.ResponseWriter, _ *http.Request) {
	sdktest.WriteJSON(w, http.StatusOK, f.body())
}

// set stores the limit of the body. An amount that is not an integer gets
// the 422 of the API's validation.
func (f *fake) set(w http.ResponseWriter, r *http.Request) {
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	amount, isNumber := body["amount"].(json.Number)
	if _, err := amount.Int64(); !isNumber || err != nil {
		sdktest.WriteValidationError(w, []string{"body", "amount"}, "Input should be a valid integer")
		return
	}
	noLimit, _ := body["no_monthly_limit"].(bool)
	f.seed(noLimit, amount)
	sdktest.WriteJSON(w, http.StatusOK, f.body())
}

// body is the LimitsOUT of the organization, with a usage that changes
// nothing the provider manages.
func (f *fake) body() map[string]any {
	noLimit, usageLimit := f.limit()
	return map[string]any{"limits": map[string]any{
		"completion": map[string]any{
			"no_monthly_limit": noLimit, "monthly_limit_reached": false,
			"usage": 42.5, "vibe_usage": 0, "total_usage": 42.5, "usage_limit": usageLimit,
		},
		"last_payment_failure": false, "last_payment_failure_protection": nil, "currency": "EUR",
	}}
}
