// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package usergroups

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/identityschema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
	"github.com/ippontech/terraform-provider-mistral/internal/providerdata"
)

var (
	_ resource.ResourceWithConfigure   = &UserGroupResource{}
	_ resource.ResourceWithImportState = &UserGroupResource{}
	_ resource.ResourceWithIdentity    = &UserGroupResource{}
)

// NewUserGroupResource returns the mistral_user_group resource.
func NewUserGroupResource() resource.Resource {
	return &UserGroupResource{}
}

// UserGroupResource manages a user group of the organization:
// /v1/admin/user-groups/{group_uuid}, with its organization role through
// PATCH .../organization-role and its parent groups through
// parent_group_ids.
type UserGroupResource struct {
	client *client.Client
}

// userGroupModel is the state of mistral_user_group.
type userGroupModel struct {
	ID                types.String `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	Description       types.String `tfsdk:"description"`
	ParentGroupIDs    types.Set    `tfsdk:"parent_group_ids"`
	OrganizationRole  types.String `tfsdk:"organization_role"`
	ExternallyManaged types.Bool   `tfsdk:"externally_managed"`
}

// userGroupIdentity is the resource identity of mistral_user_group.
type userGroupIdentity struct {
	ID types.String `tfsdk:"id"`
}

// organizationRoles are the role names PATCH .../organization-role takes
// (AdminUpdateUserGroupOrganizationRoleIn), and the ones the Admin API
// guide lists (https://docs.mistral.ai/admin/admin-api/manage-groups-roles).
// The route also takes a role UUID, which the provider does not accept: the
// state could not tell it from the name the API may answer with.
var organizationRoles = []string{"member", "billing_manager", "organization_admin"}

// legacyOrganizationRoles maps the letters of the deprecated UserRole enum
// (A, M, B) to the role names, in case the API answers a group's role in
// that form. A is Admin and M is Member in the user provisioning guide
// (https://docs.mistral.ai/admin/admin-api/user-provisioning); B as billing
// manager is our reading.
var legacyOrganizationRoles = map[string]string{"A": "organization_admin", "M": "member", "B": "billing_manager"}

// Metadata names the type mistral_user_group.
func (r *UserGroupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user_group"
}

// IdentitySchema makes the group ID the identity of an import block.
func (r *UserGroupResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = identityschema.Schema{
		Attributes: map[string]identityschema.Attribute{
			"id": identityschema.StringAttribute{RequiredForImport: true, Description: "The user group ID."},
		},
	}
}

// Schema describes the group. organization_role is also computed, since the
// API cannot clear it.
func (r *UserGroupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a user group of the organization of the Admin API key: its name, description, parent groups and organization role.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The user group ID, a UUID.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The name of the group, up to 255 characters.",
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 255)},
			},
			"description": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "The description of the group, up to 255 characters. Removing it clears the description.",
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 255)},
			},
			"parent_group_ids": schema.SetAttribute{
				Optional:    true,
				ElementType: types.StringType,
				MarkdownDescription: "The IDs of the groups this group is directly nested inside. Removing one takes the group out of that parent; " +
					"removing the argument makes it a top-level group.",
				Validators: []validator.Set{
					setvalidator.SizeAtLeast(1),
					setvalidator.ValueStringsAre(stringvalidator.LengthAtLeast(1)),
				},
			},
			"organization_role": schema.StringAttribute{
				Optional: true,
				// Computed, because the API cannot clear the role: the
				// organization-role route requires a value.
				Computed: true,
				MarkdownDescription: "The organization role the group gives its members: `member`, `billing_manager` or `organization_admin`. " +
					"When it is left out, Terraform does not manage the role. The API cannot clear a role: removing the argument " +
					"leaves the role as it is, and `member` is the lowest one.",
				Validators:    []validator.String{stringvalidator.OneOf(organizationRoles...)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"externally_managed": schema.BoolAttribute{
				Computed: true,
				MarkdownDescription: "Whether an identity provider manages the group, through SSO or SCIM. The fields the identity provider " +
					"owns cannot be changed in Mistral.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

// Configure takes the client, and fails without the Admin API key.
func (r *UserGroupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerdata.Configure(req.ProviderData, providerdata.Admin, &resp.Diagnostics)
}

// Create creates the group, then sets its organization role when the
// configuration gives one.
func (r *UserGroupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan userGroupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	group, err := r.client.CreateUserGroup(ctx, client.CreateUserGroupRequest{
		Name:           plan.Name.ValueString(),
		Description:    plan.Description.ValueStringPointer(),
		ParentGroupIDs: stringsOf(plan.ParentGroupIDs),
	})
	if err != nil {
		resp.Diagnostics.AddError("Cannot create the user group", err.Error())
		return
	}
	// The group exists from here on. Its state is saved before the role is
	// set, so that a failure of that second call leaves a tainted group,
	// which the next apply replaces, rather than a group Terraform lost.
	resp.Diagnostics.Append(setUserGroupState(ctx, *group, &resp.State, resp.Identity)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// A role left out of the configuration is unknown in the plan: the
	// group keeps the role the API gives a new group.
	if !plan.OrganizationRole.IsUnknown() && !plan.OrganizationRole.IsNull() {
		r.setOrganizationRole(ctx, group.ID, plan.OrganizationRole.ValueString(), &resp.State, resp.Identity, &resp.Diagnostics)
	}
}

// Read refreshes the group. A group deleted outside Terraform answers 404
// and leaves the state.
func (r *UserGroupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var id types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		return
	}
	group, err := r.client.GetUserGroup(ctx, id.ValueString())
	if client.IsNotFound(err) {
		// Set the identity before removing: during an import, terraform-plugin-framework v1.19.0 fails a Read that removes the resource without one.
		if resp.Identity != nil {
			resp.Diagnostics.Append(resp.Identity.Set(ctx, userGroupIdentity{ID: id})...)
		}
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot read the user group", err.Error())
		return
	}
	resp.Diagnostics.Append(setUserGroupState(ctx, *group, &resp.State, resp.Identity)...)
}

// Update sends the group fields and the organization role, each through its
// own route and only when they change.
func (r *UserGroupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state userGroupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.ID.ValueString()
	// The group update and the role update are two routes: each is called
	// only when its own fields change, so a role change does not rewrite the
	// fields an identity provider may own on an externally managed group.
	if !plan.Name.Equal(state.Name) || !plan.Description.Equal(state.Description) || !plan.ParentGroupIDs.Equal(state.ParentGroupIDs) {
		group, err := r.client.UpdateUserGroup(ctx, id, client.UpdateUserGroupRequest{
			Name:           plan.Name.ValueString(),
			Description:    plan.Description.ValueString(),
			ParentGroupIDs: stringsOf(plan.ParentGroupIDs),
		})
		if err != nil {
			resp.Diagnostics.AddError("Cannot update the user group", err.Error())
			return
		}
		resp.Diagnostics.Append(setUserGroupState(ctx, *group, &resp.State, resp.Identity)...)
	}
	// A role left out of the configuration plans its prior value
	// (UseStateForUnknown), so the role route is called only for a change
	// the configuration asks for.
	if !plan.OrganizationRole.IsNull() && !plan.OrganizationRole.Equal(state.OrganizationRole) {
		r.setOrganizationRole(ctx, id, plan.OrganizationRole.ValueString(), &resp.State, resp.Identity, &resp.Diagnostics)
	}
}

// Delete deletes the group, whose members stay in the organization. A group
// already gone is not an error.
func (r *UserGroupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var id types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteUserGroup(ctx, id.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Cannot delete the user group", err.Error())
	}
}

// ImportState takes the group ID, as an import ID or as the identity.
func (r *UserGroupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughWithIdentity(ctx, path.Root("id"), path.Root("id"), req, resp)
}

// setOrganizationRole sets the organization role of the group, then reads
// the group back: the role route answers 204, with no group to map.
func (r *UserGroupResource) setOrganizationRole(ctx context.Context, id, role string, state *tfsdk.State, identity *tfsdk.ResourceIdentity, diags *diag.Diagnostics) {
	if err := r.client.SetUserGroupOrganizationRole(ctx, id, role); err != nil {
		diags.AddError("Cannot set the organization role of the user group", err.Error())
		return
	}
	group, err := r.client.GetUserGroup(ctx, id)
	if err != nil {
		diags.AddError("Cannot read the user group", err.Error())
		return
	}
	diags.Append(setUserGroupState(ctx, *group, state, identity)...)
}

// setUserGroupState writes the group and its identity.
func setUserGroupState(ctx context.Context, g client.UserGroup, state *tfsdk.State, identity *tfsdk.ResourceIdentity) diag.Diagnostics {
	m := userGroupModel{
		ID:                types.StringValue(g.ID),
		Name:              types.StringValue(g.Name),
		Description:       nonEmpty(g.Description),
		ParentGroupIDs:    setOf(g.ParentGroupIDs),
		OrganizationRole:  organizationRoleState(g.OrganizationRole),
		ExternallyManaged: externallyManaged(g),
	}
	diags := state.Set(ctx, &m)
	if identity != nil {
		diags.Append(identity.Set(ctx, userGroupIdentity{ID: m.ID})...)
	}
	return diags
}

// organizationRoleState maps the organization role the API answers to the
// name the configuration uses: a legacy letter becomes its name, and any
// other value, a role UUID included, is kept as the API wrote it.
func organizationRoleState(role *string) types.String {
	if role == nil || *role == "" {
		return types.StringNull()
	}
	if name, ok := legacyOrganizationRoles[*role]; ok {
		return types.StringValue(name)
	}
	return types.StringValue(*role)
}
