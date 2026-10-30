// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package spendlimits holds mistral_organization_spend_limit: the monthly
// spend limit of the organization of the Admin API key
// (https://docs.mistral.ai/admin/billing-usage/usage-limits).
package spendlimits

import (
	"context"
	"fmt"
	"math"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/identityschema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
	"github.com/ippontech/terraform-provider-mistral/internal/providerdata"
)

var (
	_ resource.ResourceWithConfigure      = &OrganizationSpendLimitResource{}
	_ resource.ResourceWithImportState    = &OrganizationSpendLimitResource{}
	_ resource.ResourceWithIdentity       = &OrganizationSpendLimitResource{}
	_ resource.ResourceWithValidateConfig = &OrganizationSpendLimitResource{}
)

// organizationID is the ID, import ID and identity of the one spend limit of
// an organization: the Admin API key selects the organization, so the ID
// only has to say which singleton the resource is.
const organizationID = "organization"

// NewOrganizationSpendLimitResource returns the
// mistral_organization_spend_limit resource.
func NewOrganizationSpendLimitResource() resource.Resource {
	return &OrganizationSpendLimitResource{}
}

// OrganizationSpendLimitResource manages the monthly spend limit of the
// organization: GET and POST /v1/admin/spend-limit, a singleton that always
// exists and has no delete.
type OrganizationSpendLimitResource struct {
	client *client.Client
}

// spendLimitModel is the state of mistral_organization_spend_limit.
type spendLimitModel struct {
	ID             types.String `tfsdk:"id"`
	Amount         types.Int64  `tfsdk:"amount"`
	NoMonthlyLimit types.Bool   `tfsdk:"no_monthly_limit"`
	Currency       types.String `tfsdk:"currency"`
}

// spendLimitIdentity is the resource identity of
// mistral_organization_spend_limit.
type spendLimitIdentity struct {
	ID types.String `tfsdk:"id"`
}

// Metadata names the type mistral_organization_spend_limit.
func (r *OrganizationSpendLimitResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization_spend_limit"
}

// IdentitySchema makes the fixed word organization the identity of an
// import block.
func (r *OrganizationSpendLimitResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = identityschema.Schema{
		Attributes: map[string]identityschema.Attribute{
			"id": identityschema.StringAttribute{RequiredForImport: true, Description: "Always `organization`."},
		},
	}
}

// Schema describes the limit: an amount, or no limit.
func (r *OrganizationSpendLimitResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Sets the monthly spend limit of the organization of the Admin API key, or removes it. Destroying the resource leaves the limit as it is at Mistral.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Always `organization`: an organization has one spend limit.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"amount": schema.Int64Attribute{
				Optional: true,
				MarkdownDescription: "The monthly spend limit, a whole number in the billing currency of the organization (`currency`): " +
					"`500` means 500 in that currency, not 500 cents. Required unless `no_monthly_limit` is `true`.",
				Validators: []validator.Int64{int64validator.AtLeast(0)},
			},
			"no_monthly_limit": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Whether the organization has no monthly spend limit. Set it to `true`, without `amount`, to remove the limit. Defaults to `false`.",
			},
			"currency": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The billing currency of the organization, the unit of `amount`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

// ValidateConfig requires exactly one of a limit or no limit: amount, or
// no_monthly_limit = true.
func (r *OrganizationSpendLimitResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var cfg spendLimitModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() || cfg.Amount.IsUnknown() || cfg.NoMonthlyLimit.IsUnknown() {
		return
	}
	switch noLimit := cfg.NoMonthlyLimit.ValueBool(); {
	case noLimit && !cfg.Amount.IsNull():
		resp.Diagnostics.AddAttributeError(path.Root("amount"), "Conflicting spend limit",
			"`amount` cannot be set with `no_monthly_limit = true`: remove one of them.")
	case !noLimit && cfg.Amount.IsNull():
		resp.Diagnostics.AddAttributeError(path.Root("amount"), "Missing spend limit",
			"Set `amount` to the monthly limit, or `no_monthly_limit = true` to remove the limit.")
	}
}

// Configure takes the client, and fails without the Admin API key.
func (r *OrganizationSpendLimitResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerdata.Configure(req.ProviderData, providerdata.Admin, &resp.Diagnostics)
}

// Create sets the limit. The organization always has a spend limit, or no
// limit, so there is nothing to create: the create refuses to overwrite a
// limit set outside Terraform with another value, and points to an import
// instead (CLAUDE.md, "No silent adoption"). A limit equal to the plan, or
// no limit at all, is taken as is: overwriting it loses nothing. When both
// the organization and the plan have no limit, and the organization holds
// no whole amount to send with the flag, the create writes nothing.
func (r *OrganizationSpendLimitResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan spendLimitModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	current, err := r.client.GetOrganizationLimits(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Cannot read the organization spend limit", err.Error())
		return
	}
	existing, diags := spendLimitState(*current)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	hasLimit := !existing.NoMonthlyLimit.ValueBool() && !existing.Amount.IsNull()
	if hasLimit && !sameLimit(existing, plan) {
		resp.Diagnostics.AddAttributeError(path.Root("amount"), "Organization spend limit already set",
			fmt.Sprintf("The organization already has a monthly spend limit of %d %s, set outside Terraform. "+
				"Import it with an import block (id = %q) to manage it, or set amount to that value.",
				existing.Amount.ValueInt64(), existing.Currency.ValueString(), organizationID))
		return
	}
	// usage_limit may still hold the last limit when the organization has no
	// limit: that is the amount to send with no_monthly_limit. limitAmount
	// gives null when it holds none, and for a fractional one, whose error
	// only matters with the flag off, where spendLimitState refused it above.
	keep, _ := limitAmount(current.Limits.Completion)
	if plan.NoMonthlyLimit.ValueBool() && existing.NoMonthlyLimit.ValueBool() && keep.IsNull() {
		// The organization already has no limit, as planned, and the POST
		// would need an amount the organization does not hold (see set).
		resp.Diagnostics.Append(writeState(ctx, existing, &resp.State, resp.Identity)...)
		return
	}
	r.set(ctx, plan, keep, &resp.State, resp.Identity, &resp.Diagnostics)
}

// Read refreshes the limit. It never answers 404: the limit of an
// organization always exists.
func (r *OrganizationSpendLimitResource) Read(ctx context.Context, _ resource.ReadRequest, resp *resource.ReadResponse) {
	limits, err := r.client.GetOrganizationLimits(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Cannot read the organization spend limit", err.Error())
		return
	}
	m, diags := spendLimitState(*limits)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(writeState(ctx, m, &resp.State, resp.Identity)...)
}

// Update sets the planned limit. The amount in the state is the one to send
// with no_monthly_limit (see set).
func (r *OrganizationSpendLimitResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state spendLimitModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.set(ctx, plan, state.Amount, &resp.State, resp.Identity, &resp.Diagnostics)
}

// Delete calls nothing: the API has no delete, and the two calls that would
// stand in for one are worse than leaving the limit. Removing the limit
// (no_monthly_limit) would lift a cost cap as a side effect of a destroy,
// and the limit the organization had before Terraform is not known, so it
// cannot be restored. The resource leaves the state with a warning that
// says so.
func (r *OrganizationSpendLimitResource) Delete(_ context.Context, _ resource.DeleteRequest, resp *resource.DeleteResponse) {
	resp.Diagnostics.AddWarning("The organization spend limit stays at Mistral",
		"Terraform no longer manages the monthly spend limit of the organization, which keeps its current value. "+
			"Change it in the Admin Panel, or manage it again with an import block (id = \"organization\").")
}

// ImportState accepts the import ID or identity "organization" only.
func (r *OrganizationSpendLimitResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id := req.ID
	if id == "" {
		var identity spendLimitIdentity
		resp.Diagnostics.Append(req.Identity.Get(ctx, &identity)...)
		if resp.Diagnostics.HasError() {
			return
		}
		id = identity.ID.ValueString()
	}
	if id != organizationID {
		resp.Diagnostics.AddError("Invalid import ID",
			fmt.Sprintf("The ID of the organization spend limit is always %q, got %q.", organizationID, id))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), organizationID)...)
}

// set sends the plan and writes what the API answers. keep is the limit the
// organization has now: the specification requires amount even when
// no_monthly_limit removes the limit, and sending the current limit rather
// than 0 keeps the organization usable if the API ever reads the amount
// despite the flag. Without a limit to keep, set sends nothing and fails:
// any other amount would be made up, and 0 could suspend the organization.
func (r *OrganizationSpendLimitResource) set(ctx context.Context, plan spendLimitModel, keep types.Int64, state *tfsdk.State, identity *tfsdk.ResourceIdentity, diags *diag.Diagnostics) {
	body := client.OrganizationSpendLimitRequest{Amount: plan.Amount.ValueInt64(), NoMonthlyLimit: plan.NoMonthlyLimit.ValueBool()}
	if body.NoMonthlyLimit {
		if keep.IsNull() {
			diags.AddAttributeError(path.Root("no_monthly_limit"), "No amount to send with no_monthly_limit",
				"The API requires an amount with no_monthly_limit = true, and the provider sends the current limit of the organization, never 0: "+
					"if the API read the amount despite the flag, 0 could suspend the organization's API access. "+
					"The organization has no whole monthly limit to send with it. "+
					"Set amount to a whole limit and apply, then apply no_monthly_limit = true.")
			return
		}
		body.Amount = keep.ValueInt64()
	}
	limits, err := r.client.SetOrganizationSpendLimit(ctx, body)
	if err != nil {
		diags.AddError("Cannot set the organization spend limit", err.Error())
		return
	}
	m, d := spendLimitState(*limits)
	diags.Append(d...)
	if diags.HasError() {
		return
	}
	diags.Append(writeState(ctx, m, state, identity)...)
}

// sameLimit reports whether the limit of the organization is the one the
// plan sets.
func sameLimit(existing, plan spendLimitModel) bool {
	return existing.NoMonthlyLimit.Equal(plan.NoMonthlyLimit) && existing.Amount.Equal(plan.Amount)
}

// spendLimitState maps the limits the API answers. amount is null when the
// organization has no monthly limit, whatever usage_limit holds then.
func spendLimitState(l client.OrganizationLimits) (spendLimitModel, diag.Diagnostics) {
	var diags diag.Diagnostics
	m := spendLimitModel{
		ID:             types.StringValue(organizationID),
		Amount:         types.Int64Null(),
		NoMonthlyLimit: types.BoolValue(l.Limits.Completion.NoMonthlyLimit),
		Currency:       types.StringValue(l.Limits.Currency),
	}
	if m.NoMonthlyLimit.ValueBool() {
		return m, diags
	}
	amount, err := limitAmount(l.Limits.Completion)
	if err != nil {
		diags.AddError("Unexpected organization spend limit", err.Error())
	}
	m.Amount = amount
	return m, diags
}

// limitAmount reads usage_limit, which the API writes as a JSON number such
// as 500 or 500.0, as a whole amount, null when the API gives none. A limit
// with a fractional part, which the integer amount of the POST cannot
// produce, is an error rather than a rounded value.
func limitAmount(u client.OrganizationUsageLimits) (types.Int64, error) {
	if u.UsageLimit == nil {
		return types.Int64Null(), nil
	}
	if i, err := u.UsageLimit.Int64(); err == nil {
		return types.Int64Value(i), nil
	}
	// Above 2^53 a float64 no longer holds every whole number exactly.
	const exact = 1 << 53
	f, err := u.UsageLimit.Float64()
	if err != nil || f != math.Trunc(f) || math.Abs(f) > exact {
		return types.Int64Null(), fmt.Errorf("the API answers a monthly limit of %s, which amount, a whole number, cannot hold. "+
			"Set a whole limit in the Admin Panel, then refresh", u.UsageLimit)
	}
	return types.Int64Value(int64(f)), nil
}

// writeState writes the spend limit and its identity.
func writeState(ctx context.Context, m spendLimitModel, state *tfsdk.State, identity *tfsdk.ResourceIdentity) diag.Diagnostics {
	diags := state.Set(ctx, &m)
	if identity != nil {
		diags.Append(identity.Set(ctx, spendLimitIdentity{ID: m.ID})...)
	}
	return diags
}
