// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// TestCreateOnlyWarnsOnAChangeOfAnExistingWorkspace checks the plan warning
// of createOnly, which the resource tests cannot see: only a change of a
// known value on an existing workspace warns.
func TestCreateOnlyWarnsOnAChangeOfAnExistingWorkspace(t *testing.T) {
	t.Parallel()
	// Only the nullness of the whole state and plan matters to the modifier.
	exists := tftypes.NewValue(tftypes.String, "workspace")
	gone := tftypes.NewValue(tftypes.String, nil)
	cases := map[string]struct {
		state, plan    tftypes.Value
		prior, planned types.String
		warns          bool
	}{
		"change":           {exists, exists, types.StringValue("u-1"), types.StringValue("u-2"), true},
		"no change":        {exists, exists, types.StringValue("u-1"), types.StringValue("u-1"), false},
		"after an import":  {exists, exists, types.StringNull(), types.StringValue("u-1"), false},
		"unknown":          {exists, exists, types.StringValue("u-1"), types.StringUnknown(), false},
		"create":           {gone, exists, types.StringNull(), types.StringValue("u-1"), false},
		"destroy":          {exists, gone, types.StringValue("u-1"), types.StringNull(), false},
		"removed argument": {exists, exists, types.StringValue("u-1"), types.StringNull(), true},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			req := planmodifier.StringRequest{
				Path:  path.Root("admin_user_id"),
				State: tfsdk.State{Raw: c.state}, Plan: tfsdk.Plan{Raw: c.plan},
				StateValue: c.prior, PlanValue: c.planned,
			}
			resp := &planmodifier.StringResponse{PlanValue: c.planned}
			createOnly{}.PlanModifyString(context.Background(), req, resp)
			if got := resp.Diagnostics.WarningsCount() == 1; got != c.warns {
				t.Fatalf("warns = %t, want %t: %v", got, c.warns, resp.Diagnostics)
			}
			if c.warns && !strings.Contains(resp.Diagnostics[0].Detail(), "admin_user_id only when it creates the workspace") {
				t.Errorf("detail = %q", resp.Diagnostics[0].Detail())
			}
			if resp.Diagnostics.HasError() || !resp.PlanValue.Equal(c.planned) {
				t.Errorf("the modifier must only warn: %v, plan %s", resp.Diagnostics, resp.PlanValue)
			}
		})
	}

	boolResp := &planmodifier.BoolResponse{}
	createOnly{}.PlanModifyBool(context.Background(), planmodifier.BoolRequest{
		Path:  path.Root("add_all_org_members"),
		State: tfsdk.State{Raw: exists}, Plan: tfsdk.Plan{Raw: exists},
		StateValue: types.BoolValue(false), PlanValue: types.BoolValue(true),
	}, boolResp)
	if boolResp.Diagnostics.WarningsCount() != 1 {
		t.Errorf("a change of add_all_org_members must warn: %v", boolResp.Diagnostics)
	}
	if d := (createOnly{}).MarkdownDescription(context.Background()); !strings.Contains(d, "at creation only") {
		t.Errorf("description = %q", d)
	}
}
