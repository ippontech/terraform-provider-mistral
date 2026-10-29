// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package usergroups

import (
	"context"
	"fmt"

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
	_ resource.ResourceWithConfigure   = &UserGroupMemberResource{}
	_ resource.ResourceWithImportState = &UserGroupMemberResource{}
	_ resource.ResourceWithIdentity    = &UserGroupMemberResource{}
)

// NewUserGroupMemberResource returns the mistral_user_group_member resource.
func NewUserGroupMemberResource() resource.Resource {
	return &UserGroupMemberResource{}
}

// UserGroupMemberResource manages one user in one user group:
// /v1/admin/user-groups/{group_uuid}/members, which adds and removes lists
// of users and has no read of one member, so Read walks the list.
type UserGroupMemberResource struct {
	client *client.Client
}

// userGroupMemberModel is the state of mistral_user_group_member.
type userGroupMemberModel struct {
	ID      types.String `tfsdk:"id"`
	GroupID types.String `tfsdk:"group_id"`
	UserID  types.String `tfsdk:"user_id"`
}

// userGroupMemberIdentity is the resource identity of
// mistral_user_group_member.
type userGroupMemberIdentity struct {
	GroupID types.String `tfsdk:"group_id"`
	UserID  types.String `tfsdk:"user_id"`
}

// Metadata names the type mistral_user_group_member.
func (r *UserGroupMemberResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user_group_member"
}

// IdentitySchema makes the group and the user the identity of an import
// block.
func (r *UserGroupMemberResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = identityschema.Schema{
		Attributes: map[string]identityschema.Attribute{
			"group_id": identityschema.StringAttribute{RequiredForImport: true, Description: "The user group ID."},
			"user_id":  identityschema.StringAttribute{RequiredForImport: true, Description: "The user ID."},
		},
	}
}

// Schema describes the membership: both arguments replace it.
func (r *UserGroupMemberResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	idPart := []validator.String{stringvalidator.RegexMatches(noColon, "must not be empty or contain a colon")}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Adds a user of the organization to a user group. The user gets the roles the group gives, in the organization and in the workspaces the group is assigned to.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "`<group_id>:<user_id>`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"group_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the user group. Changing it moves the user to another group.",
				PlanModifiers:       replace,
				Validators:          idPart,
			},
			"user_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the user, a member of the organization. Changing it replaces the member.",
				PlanModifiers:       replace,
				Validators:          idPart,
			},
		},
	}
}

// Configure takes the client, and fails without the Admin API key.
func (r *UserGroupMemberResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerdata.Configure(req.ProviderData, providerdata.Admin, &resp.Diagnostics)
}

// Create adds the user to the group, once it has checked that the user is
// not in it yet.
func (r *UserGroupMemberResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan userGroupMemberModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	groupID, userID := plan.GroupID.ValueString(), plan.UserID.ValueString()

	// Adding a user who is already in the group would not fail, and the
	// destroy of this resource would then take out a member that someone
	// else added: the create refuses, and points to an import instead.
	existing, err := r.client.FindUserGroupMember(ctx, groupID, userID)
	if err != nil {
		resp.Diagnostics.AddError("Cannot create the user group member", err.Error())
		return
	}
	if existing != nil {
		resp.Diagnostics.AddAttributeError(path.Root("user_id"), "User already in the group",
			fmt.Sprintf("The user %q is already a member of the user group %q. Import it with an import block (id = %q) to manage it.",
				userID, groupID, groupID+":"+userID))
		return
	}
	if err := r.client.AddUserGroupMember(ctx, groupID, userID); err != nil {
		resp.Diagnostics.AddError("Cannot add the user to the group", err.Error())
		return
	}
	resp.Diagnostics.Append(setMemberState(ctx, groupID, userID, &resp.State, resp.Identity)...)
}

// Read looks for the user among the members of the group. A membership or a
// group gone outside Terraform leaves the state.
func (r *UserGroupMemberResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state userGroupMemberModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	groupID, userID := state.GroupID.ValueString(), state.UserID.ValueString()
	member, err := r.client.FindUserGroupMember(ctx, groupID, userID)
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Cannot read the user group member", err.Error())
		return
	}
	// No member: the user left the group, or the group is gone (a 404) and
	// the membership with it.
	if member == nil {
		// Set the identity before removing: during an import, terraform-plugin-framework v1.19.0 fails a Read that removes the resource without one.
		if resp.Identity != nil {
			resp.Diagnostics.Append(resp.Identity.Set(ctx, userGroupMemberIdentity{GroupID: state.GroupID, UserID: state.UserID})...)
		}
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(setMemberState(ctx, groupID, userID, &resp.State, resp.Identity)...)
}

// Update is never called: every argument requires a replacement. It leaves
// the prior state in place, so a future argument that forgets
// RequiresReplace fails the apply with an inconsistent result instead of
// changing nothing at Mistral.
func (r *UserGroupMemberResource) Update(context.Context, resource.UpdateRequest, *resource.UpdateResponse) {
}

// Delete removes the user from the group, not from the organization. A user
// or a group already gone is not an error.
func (r *UserGroupMemberResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state userGroupMemberModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.RemoveUserGroupMember(ctx, state.GroupID.ValueString(), state.UserID.ValueString())
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Cannot remove the user from the group", err.Error())
	}
}

// ImportState takes `<group_id>:<user_id>`, or the identity.
func (r *UserGroupMemberResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importComposite(ctx, req, resp, "group_id", "user_id")
}

// setMemberState writes the membership and its identity. The member list
// also gives the user's name and email, which belong to the user, not to
// the membership: they are left out.
func setMemberState(ctx context.Context, groupID, userID string, state *tfsdk.State, identity *tfsdk.ResourceIdentity) diag.Diagnostics {
	m := userGroupMemberModel{
		ID:      types.StringValue(groupID + ":" + userID),
		GroupID: types.StringValue(groupID),
		UserID:  types.StringValue(userID),
	}
	diags := state.Set(ctx, &m)
	if identity != nil {
		diags.Append(identity.Set(ctx, userGroupMemberIdentity{GroupID: m.GroupID, UserID: m.UserID})...)
	}
	return diags
}
