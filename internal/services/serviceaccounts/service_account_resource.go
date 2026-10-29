// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package serviceaccounts holds mistral_service_account and the
// mistral_assignable_service_account_roles data source: the non-human
// identities of a workspace and the workspace roles they can hold.
package serviceaccounts

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/identityschema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
	"github.com/ippontech/terraform-provider-mistral/internal/providerdata"
)

var (
	_ resource.ResourceWithConfigure   = &ServiceAccountResource{}
	_ resource.ResourceWithImportState = &ServiceAccountResource{}
	_ resource.ResourceWithIdentity    = &ServiceAccountResource{}
)

// NewServiceAccountResource returns the mistral_service_account resource.
func NewServiceAccountResource() resource.Resource {
	return &ServiceAccountResource{}
}

// ServiceAccountResource manages a service account and its roles:
// /v1/service-accounts (https://docs.mistral.ai/api/endpoint/beta/service_accounts).
// Its credentials, API keys and workload identities, are created in the Admin
// Console only.
type ServiceAccountResource struct {
	client *client.Client
}

// Limits of the API: the name is a slug of 2 to 63 characters
// (https://docs.mistral.ai/admin/identity-access/service-accounts), the
// description at most 4096 characters and role_ids at most 14 IDs
// (CreateServiceAccountRequest in the specification).
const (
	maxDescription = 4096
	maxRoles       = 14
)

// slug is the name rule of the service accounts guide: a lowercase letter
// followed by lowercase letters, digits or hyphens. The API lowercases a
// mixed-case name, which would plan a replacement on every run: the
// validator refuses it instead.
var slug = regexp.MustCompile(`^[a-z][a-z0-9-]{1,62}$`)

// serviceAccountModel is the state of mistral_service_account.
type serviceAccountModel struct {
	ID             types.String `tfsdk:"id"`
	WorkspaceID    types.String `tfsdk:"workspace_id"`
	Name           types.String `tfsdk:"name"`
	Description    types.String `tfsdk:"description"`
	RoleIDs        []string     `tfsdk:"role_ids"`
	OrganizationID types.String `tfsdk:"organization_id"`
	CreatedAt      types.String `tfsdk:"created_at"`
	UpdatedAt      types.String `tfsdk:"updated_at"`
}

// serviceAccountIdentity is the resource identity of mistral_service_account:
// the account ID, which GET /v1/service-accounts/{service_account_id} reads
// on its own.
type serviceAccountIdentity struct {
	ID types.String `tfsdk:"id"`
}

// Metadata names the resource mistral_service_account.
func (r *ServiceAccountResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_service_account"
}

// IdentitySchema declares the identity an import block can use instead of the
// ID.
func (r *ServiceAccountResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = identityschema.Schema{
		Attributes: map[string]identityschema.Attribute{
			"id": identityschema.StringAttribute{RequiredForImport: true, Description: "The service account ID."},
		},
	}
}

// Schema declares the arguments and attributes. name and workspace_id force a
// replacement: UpdateServiceAccountRequest has description only.
func (r *ServiceAccountResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a service account of a workspace, a non-human identity for a workload, and the workspace roles it holds.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The service account ID, a UUID.",
				PlanModifiers:       keep,
			},
			"workspace_id": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "The ID of the workspace the service account belongs to: the workspace of the provider's API key. " +
					"Changing it replaces the service account.",
				PlanModifiers: replace,
				Validators:    []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"name": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "The name of the service account, unique in the workspace: a lowercase letter followed by lowercase letters, " +
					"digits or hyphens, 2 to 63 characters, such as `prod-worker`. Changing it replaces the service account.",
				PlanModifiers: replace,
				Validators: []validator.String{stringvalidator.RegexMatches(slug,
					"must be a lowercase letter followed by lowercase letters, digits or hyphens, 2 to 63 characters")},
			},
			"description": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "What the service account is for, up to 4096 characters.",
				Validators:          []validator.String{stringvalidator.LengthBetween(1, maxDescription)},
			},
			"role_ids": schema.SetAttribute{
				Required:    true,
				ElementType: types.StringType,
				MarkdownDescription: "The IDs of the workspace roles the service account holds, 1 to 14 of them. " +
					"See the `mistral_assignable_service_account_roles` data source. A change replaces the roles as a whole.",
				Validators: []validator.Set{
					setvalidator.SizeBetween(1, maxRoles),
					setvalidator.ValueStringsAre(stringvalidator.LengthAtLeast(1)),
				},
			},
			"organization_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The ID of the organization of the workspace.",
				PlanModifiers:       keep,
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "When the service account was created, in RFC 3339 format.",
				PlanModifiers:       keep,
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "When the service account was last updated, in RFC 3339 format.",
			},
		},
	}
}

// Configure takes the client, and fails without the standard key.
func (r *ServiceAccountResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerdata.Configure(req.ProviderData, providerdata.Workspace, &resp.Diagnostics)
}

// Create sends the roles with the account: one call, so a failure leaves no
// account without its roles. The answer has no roles, and the state takes
// those of the plan; the next Read compares them with the API's.
func (r *ServiceAccountResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan serviceAccountModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	sa, err := r.client.CreateServiceAccount(ctx, client.CreateServiceAccountRequest{
		Name:        plan.Name.ValueString(),
		WorkspaceID: plan.WorkspaceID.ValueString(),
		Description: plan.Description.ValueStringPointer(),
		// Sorted, so the body does not depend on the order of the set.
		RoleIDs: slices.Sorted(slices.Values(plan.RoleIDs)),
	})
	// The name is unique in the workspace. A create that met a 5xx may have
	// made the account: the next apply ends here, pointing to the import.
	if client.IsConflict(err) {
		resp.Diagnostics.AddAttributeError(path.Root("name"), "Service account already exists",
			fmt.Sprintf("The workspace %q already has a service account named %q: names are unique in a workspace. "+
				"Import it with an import block and its ID to manage it, or choose another name. %v",
				plan.WorkspaceID.ValueString(), plan.Name.ValueString(), err))
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot create the service account", err.Error())
		return
	}
	resp.Diagnostics.Append(setServiceAccountState(ctx, accountState(*sa, plan.RoleIDs), &resp.State, resp.Identity)...)
}

// removeGone removes the account from the state after setting its identity:
// during an import, terraform-plugin-framework v1.19.0 fails a Read that
// removes the resource without one, instead of Terraform's "Cannot import
// non-existent remote object".
func removeGone(ctx context.Context, resp *resource.ReadResponse, id types.String) {
	if resp.Identity != nil {
		resp.Diagnostics.Append(resp.Identity.Set(ctx, serviceAccountIdentity{ID: id})...)
	}
	resp.State.RemoveResource(ctx)
}

// Read refreshes the account and its roles, and removes the resource when
// either call answers 404 or the account is marked deleted.
func (r *ServiceAccountResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var id types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		return
	}
	sa, err := r.client.GetServiceAccount(ctx, id.ValueString())
	// ServiceAccount has a deleted_at and the list an include_deleted filter:
	// the API may keep deleted accounts. One that GET still returns is gone
	// all the same.
	if client.IsNotFound(err) || (err == nil && sa.DeletedAt != nil) {
		removeGone(ctx, resp, id)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot read the service account", err.Error())
		return
	}
	roles, err := r.client.ListServiceAccountRoles(ctx, id.ValueString())
	// The account was deleted between the two calls.
	if client.IsNotFound(err) {
		removeGone(ctx, resp, id)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot read the roles of the service account", err.Error())
		return
	}
	resp.Diagnostics.Append(setServiceAccountState(ctx, accountState(*sa, roles), &resp.State, resp.Identity)...)
}

// Update sends the description and the roles with their own calls, each only
// when it changed. The state is written after each one, so a failure of the
// second keeps what the first changed, and the next plan sends only the
// rest.
func (r *ServiceAccountResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state serviceAccountModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.ID.ValueString()
	if !plan.Description.Equal(state.Description) {
		// A removed description is sent as null, which our reading of
		// UpdateServiceAccountRequest (anyOf string or null) takes as a clear.
		sa, err := r.client.UpdateServiceAccount(ctx, id, client.UpdateServiceAccountRequest{Description: plan.Description.ValueStringPointer()})
		if err != nil {
			resp.Diagnostics.AddError("Cannot update the service account", err.Error())
			return
		}
		state = accountState(*sa, state.RoleIDs)
		resp.Diagnostics.Append(setServiceAccountState(ctx, state, &resp.State, resp.Identity)...)
	}
	if !sameSet(plan.RoleIDs, state.RoleIDs) {
		roles, err := r.client.SetServiceAccountRoles(ctx, id, slices.Sorted(slices.Values(plan.RoleIDs)))
		if err != nil {
			resp.Diagnostics.AddError("Cannot set the roles of the service account", err.Error())
			return
		}
		// The answer lists the roles the account holds after the replace.
		state.RoleIDs = nonEmpty(roles)
	}
	// After a change of the roles only, updated_at keeps its prior value: the
	// answer of the PUT has no date, and the next refresh reads the API's.
	resp.Diagnostics.Append(setServiceAccountState(ctx, state, &resp.State, resp.Identity)...)
}

// Delete deletes the account, which deletes its credentials. A 404 means it
// is already gone.
func (r *ServiceAccountResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var id types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteServiceAccount(ctx, id.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Cannot delete the service account", err.Error())
	}
}

// ImportState takes the account ID, or the identity; Read fills the rest.
func (r *ServiceAccountResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughWithIdentity(ctx, path.Root("id"), path.Root("id"), req, resp)
}

// accountState maps a service account and the IDs of its roles. An empty
// description or role list reads as null.
func accountState(sa client.ServiceAccount, roleIDs []string) serviceAccountModel {
	description := types.StringNull()
	if sa.Description != nil && *sa.Description != "" {
		description = types.StringValue(*sa.Description)
	}
	return serviceAccountModel{
		ID:             types.StringValue(sa.ID),
		WorkspaceID:    types.StringValue(sa.WorkspaceID),
		Name:           types.StringValue(sa.Name),
		Description:    description,
		RoleIDs:        nonEmpty(roleIDs),
		OrganizationID: types.StringValue(sa.OrganizationID),
		CreatedAt:      timestamp(sa.CreatedAt),
		UpdatedAt:      timestamp(sa.UpdatedAt),
	}
}

// setServiceAccountState writes m and the identity it carries.
func setServiceAccountState(ctx context.Context, m serviceAccountModel, state *tfsdk.State, identity *tfsdk.ResourceIdentity) diag.Diagnostics {
	diags := state.Set(ctx, &m)
	if identity != nil {
		diags.Append(identity.Set(ctx, serviceAccountIdentity{ID: m.ID})...)
	}
	return diags
}

// sameSet reports whether a and b hold the same strings, in any order.
func sameSet(a, b []string) bool {
	return slices.Equal(slices.Sorted(slices.Values(a)), slices.Sorted(slices.Values(b)))
}

// nonEmpty returns nil for an empty list, which the state holds as null.
func nonEmpty(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	return ids
}

// timestamp formats t in RFC 3339, in UTC, and a zero time as null.
func timestamp(t time.Time) types.String {
	if t.IsZero() {
		return types.StringNull()
	}
	return types.StringValue(t.UTC().Format(time.RFC3339))
}
