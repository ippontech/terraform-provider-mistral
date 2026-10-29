// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"encoding/json"
	"net/http"
)

// spendLimitPath is the organization spend limit of the Admin API, one per
// organization (https://docs.mistral.ai/api/endpoint/beta/admin/billing).
const spendLimitPath = "/v1/admin/spend-limit"

// OrganizationLimits is the answer of GET and POST /v1/admin/spend-limit
// (LimitsOUT): the limits of the organization of the Admin API key.
type OrganizationLimits struct {
	Limits OrganizationLimitsContext `json:"limits"`
}

// OrganizationLimitsContext holds the usage limits and the billing currency
// (LimitsContext). The payment failure fields are left out: nothing the
// provider manages depends on them.
type OrganizationLimitsContext struct {
	Completion OrganizationUsageLimits `json:"completion"`
	// Currency is the billing currency of the organization, the unit of the
	// limit and of the usage.
	Currency string `json:"currency"`
}

// OrganizationUsageLimits is the monthly limit and the usage counted against
// it (UsageLimits).
type OrganizationUsageLimits struct {
	// NoMonthlyLimit reports that the organization has no monthly limit.
	NoMonthlyLimit bool `json:"no_monthly_limit"`
	// UsageLimit is the monthly limit, in the billing currency. The
	// specification types it as a number, while the POST takes an integer:
	// it is kept as the text the API sent, so that the caller sees a
	// fractional value instead of a rounded one.
	UsageLimit          *json.Number `json:"usage_limit"`
	MonthlyLimitReached bool         `json:"monthly_limit_reached"`
}

// OrganizationSpendLimitRequest is the body of POST /v1/admin/spend-limit
// (NewUsageLimitIN). Amount is required by the specification even when
// NoMonthlyLimit removes the limit. It is "a whole number in your
// Organization's billing currency (500 means 500, not 500 cents)"
// (https://docs.mistral.ai/admin/admin-api/usage-metrics).
type OrganizationSpendLimitRequest struct {
	Amount         int64 `json:"amount"`
	NoMonthlyLimit bool  `json:"no_monthly_limit"`
}

// GetOrganizationLimits returns the limits of the organization: GET
// /v1/admin/spend-limit.
func (cl *Client) GetOrganizationLimits(ctx context.Context) (*OrganizationLimits, error) {
	var out OrganizationLimits
	if err := cl.do(ctx, call{method: http.MethodGet, path: spendLimitPath, audience: adminKey}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SetOrganizationSpendLimit sets the monthly spend limit of the organization,
// or removes it: POST /v1/admin/spend-limit. The API documents no
// idempotency, so a 5xx is not replayed, although the call sets a value
// rather than adding one.
func (cl *Client) SetOrganizationSpendLimit(ctx context.Context, req OrganizationSpendLimitRequest) (*OrganizationLimits, error) {
	var out OrganizationLimits
	if err := cl.do(ctx, call{method: http.MethodPost, path: spendLimitPath, body: req, audience: adminKey}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
