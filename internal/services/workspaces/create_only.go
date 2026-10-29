// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
)

var (
	_ planmodifier.String = createOnly{}
	_ planmodifier.Bool   = createOnly{}
)

// createOnly is the plan modifier of a workspace argument that only POST
// /v1/admin/workspaces takes (admin_user_id, add_all_org_members). Neither
// RequiresReplace nor a silent no-op fits it: a replacement would archive
// the workspace with everything it holds, for a field that only seeds its
// first members; and Terraform requires the plan to follow the
// configuration, so the change cannot be hidden. A change therefore plans an
// in-place update that records the new value without calling the API, and
// this modifier warns about it at plan time.
type createOnly struct{}

// Description says what the modifier does, for the schema documentation.
func (createOnly) Description(context.Context) string {
	return "The API takes this argument at creation only: a later change is recorded in the state and changes nothing at Mistral."
}

// MarkdownDescription is Description.
func (m createOnly) MarkdownDescription(ctx context.Context) string { return m.Description(ctx) }

// PlanModifyString warns when a create-only string argument, admin_user_id,
// changes.
func (createOnly) PlanModifyString(_ context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	warnCreateOnlyChange(req.Path, req.State, req.Plan, req.StateValue, req.PlanValue, &resp.Diagnostics)
}

// PlanModifyBool warns when a create-only boolean argument,
// add_all_org_members, changes.
func (createOnly) PlanModifyBool(_ context.Context, req planmodifier.BoolRequest, resp *planmodifier.BoolResponse) {
	warnCreateOnlyChange(req.Path, req.State, req.Plan, req.StateValue, req.PlanValue, &resp.Diagnostics)
}

// warnCreateOnlyChange warns when the planned value of a create-only argument
// differs from its value in the state of an existing workspace. A null prior
// value, as after an import, takes the configured one without a warning: the
// API never returns these arguments, so there was nothing to change.
func warnCreateOnlyChange(p path.Path, state tfsdk.State, plan tfsdk.Plan, prior, planned attr.Value, diags *diag.Diagnostics) {
	if state.Raw.IsNull() || plan.Raw.IsNull() || prior.IsNull() || planned.IsUnknown() || planned.Equal(prior) {
		return
	}
	diags.AddAttributeWarning(p, "Argument used at creation only",
		fmt.Sprintf("Mistral takes %s only when it creates the workspace. The apply records the new value in the state and changes nothing "+
			"at Mistral, and the workspace is not replaced. Manage the members of the workspace with mistral_workspace_member.", p))
}
