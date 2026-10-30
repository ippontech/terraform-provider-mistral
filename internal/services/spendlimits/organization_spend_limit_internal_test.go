// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package spendlimits

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

// TestUpdateKeepsNoLimitWithoutAPost calls Update with no_monthly_limit in
// the state and in the plan. Terraform 1.17 never does: a flag unknown at
// plan time that turns out true leaves an empty plan at apply time, which
// Terraform skips, so no resource test reaches this path
// (TestOrganizationSpendLimitResourceKeepsNoLimitLearntAtApplyTime checks
// that end to end). Update must write the state as it is, without a POST,
// since the state holds no amount to send with the flag.
func TestUpdateKeepsNoLimitWithoutAPost(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	srv := sdktest.NewServer(t)
	r := &OrganizationSpendLimitResource{client: client.New(client.Config{BaseURL: srv.URL, AdminAPIKey: sdktest.TestAdminKey})}
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	noLimit := spendLimitModel{
		ID:             types.StringValue(organizationID),
		Amount:         types.Int64Null(),
		NoMonthlyLimit: types.BoolValue(true),
		Currency:       types.StringValue("EUR"),
	}
	req := resource.UpdateRequest{Plan: tfsdk.Plan{Schema: schemaResp.Schema}, State: tfsdk.State{Schema: schemaResp.Schema}}
	if diags := req.Plan.Set(ctx, &noLimit); diags.HasError() {
		t.Fatal(diags)
	}
	if diags := req.State.Set(ctx, &noLimit); diags.HasError() {
		t.Fatal(diags)
	}
	resp := &resource.UpdateResponse{State: tfsdk.State{Schema: schemaResp.Schema}}

	r.Update(ctx, req, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("Update failed: %v", resp.Diagnostics)
	}
	var got spendLimitModel
	if diags := resp.State.Get(ctx, &got); diags.HasError() {
		t.Fatal(diags)
	}
	if got != noLimit {
		t.Errorf("state = %+v, want %+v", got, noLimit)
	}
	srv.AssertCalled("", 0)
}
