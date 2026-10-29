// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"testing"

	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

// limitsBody is a LimitsOUT answer with the given limit (nil for null).
func limitsBody(noMonthlyLimit bool, usageLimit any) map[string]any {
	return map[string]any{"limits": map[string]any{
		"completion": map[string]any{
			"no_monthly_limit": noMonthlyLimit, "monthly_limit_reached": false,
			"usage": 12.5, "vibe_usage": 0, "total_usage": 12.5, "usage_limit": usageLimit,
		},
		"last_payment_failure": false, "last_payment_failure_protection": nil, "currency": "EUR",
	}}
}

// usage_limit keeps the text the API sent, so that a fractional limit is not
// rounded on the way.
func TestGetOrganizationLimitsKeepsTheLimitAsSent(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("GET /v1/admin/spend-limit", http.StatusOK, limitsBody(false, 500.75))
	c := newTestClient(srv)

	got, err := c.GetOrganizationLimits(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if l := got.Limits; l.Currency != "EUR" || l.Completion.NoMonthlyLimit || l.Completion.UsageLimit.String() != "500.75" {
		t.Errorf("limits = %+v, usage_limit %v", l, l.Completion.UsageLimit)
	}
	srv.AssertCredential("", sdktest.AdminKey)
}

// An organization with no limit may answer usage_limit: null.
func TestGetOrganizationLimitsReadsANullLimit(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("GET /v1/admin/spend-limit", http.StatusOK, limitsBody(true, nil))

	got, err := newTestClient(srv).GetOrganizationLimits(context.Background())
	if err != nil || !got.Limits.Completion.NoMonthlyLimit || got.Limits.Completion.UsageLimit != nil {
		t.Errorf("limits = %+v, %v", got, err)
	}
}

// The POST always sends both the amount and the flag.
func TestSetOrganizationSpendLimit(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("POST /v1/admin/spend-limit", http.StatusOK, limitsBody(false, 500))
	c := newTestClient(srv)

	got, err := c.SetOrganizationSpendLimit(context.Background(), OrganizationSpendLimitRequest{Amount: 500})
	if err != nil || got.Limits.Completion.UsageLimit.String() != "500" {
		t.Fatalf("SetOrganizationSpendLimit = %+v, %v", got, err)
	}
	if _, err := c.SetOrganizationSpendLimit(context.Background(), OrganizationSpendLimitRequest{Amount: 500, NoMonthlyLimit: true}); err != nil {
		t.Fatal(err)
	}
	srv.AssertJSONBodies("POST /v1/admin/spend-limit",
		`{"amount": 500, "no_monthly_limit": false}`,
		`{"amount": 500, "no_monthly_limit": true}`)
	srv.AssertCredential("", sdktest.AdminKey)
}

// The read returns the API error, and the POST is not replayed after a 5xx.
func TestOrganizationSpendLimitErrors(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("POST /v1/admin/spend-limit", http.StatusOK, limitsBody(false, 500))
	srv.Respond("GET /v1/admin/spend-limit", http.StatusForbidden, map[string]any{"detail": "Forbidden"})
	srv.FailNext("POST /v1/admin/spend-limit", http.StatusServiceUnavailable, 1)
	c := newTestClient(srv)

	if _, err := c.GetOrganizationLimits(context.Background()); StatusCode(err) != http.StatusForbidden {
		t.Errorf("GetOrganizationLimits err = %v, want the 403", err)
	}
	if _, err := c.SetOrganizationSpendLimit(context.Background(), OrganizationSpendLimitRequest{Amount: 1}); StatusCode(err) != http.StatusServiceUnavailable {
		t.Errorf("a POST after a 503 must not be replayed: %v", err)
	}
	srv.AssertCalled("POST /v1/admin/spend-limit", 1)
}
