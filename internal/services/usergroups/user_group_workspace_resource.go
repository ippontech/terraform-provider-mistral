// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package usergroups

import (
	"context"
	"fmt"

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
	_ resource.ResourceWithConfigure   = &UserGroupWorkspaceResource{}
	_ resource.ResourceWithImportState = &UserGroupWorkspaceResource{}
	_ resource.ResourceWithIdentity    = &UserGroupWorkspaceResource{}
)

// NewUserGroupWorkspaceResource returns the mistral_user_group_workspace
// resource.
func NewUserGroupWorkspaceResource() resource.Resource {
	return &UserGroupWorkspaceResource{}
}

// UserGroupWorkspaceResource manages the access of a user group to a
// workspace, with the workspace roles its members get there:
// /v1/admin/user-groups/{group_uuid}/workspaces, which has no read of one
// assignment, so Read walks the list.
type UserGroupWorkspaceResource struct {
	client *client.Client
}

// userGroupWorkspaceModel is the state of mistral_user_group_workspace.
type userGroupWorkspaceModel struct {
	ID          types.String `tfsdk:"id"`
	GroupID     types.String `tfsdk:"group_id"`
	WorkspaceID types.String `tfsdk:"workspace_id"`
	RoleNames   types.Set    `tfsdk:"role_names"`
}

// userGroupWorkspaceIdentity is the resource identity of
// mistral_user_group_workspace.
type userGroupWorkspaceIdentity struct {
	GroupID     types.String `tfsdk:"group_id"`
	WorkspaceID types.String `tfsdk:"workspace_id"`
}

// workspaceRoleNames are the names role_names takes in the specification
// (AdminAssignGroupToWorkspaceIn). The Admin API guide describes user, dev,
// mistral_code_user, billing, workspace_contributor, workspace_admin and
// observability_viewer (https://docs.mistral.ai/admin/admin-api/manage-groups-roles);
// the others are in the specification only.
var workspaceRoleNames = []string{
	"billing", "user", "contributor", "dev", "dev_contributor", "mistral_code_user", "cloud_user",
	"workspace_contributor", "workspace_admin", "observability_viewer", "workflow_executor", "member",
}

// Metadata names the type mistral_user_group_workspace.
func (r *UserGroupWorkspaceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user_group_workspace"
}

// IdentitySchema makes the group and the workspace the identity of an
// import block.
func (r *UserGroupWorkspaceResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = identityschema.Schema{
		Attributes: map[string]identityschema.Attribute{
			"group_id":     identityschema.StringAttribute{RequiredForImport: true, Description: "The user group ID."},
			"workspace_id": identityschema.StringAttribute{RequiredForImport: true, Description: "The workspace ID."},
		},
	}
}

// Schema describes the assignment: the roles update in place, the IDs
// replace it.
func (r *UserGroupWorkspaceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	idPart := []validator.String{stringvalidator.RegexMatches(noColon, "must not be empty or contain a colon")}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Gives the members of a user group access to a workspace, with the workspace roles they get there.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "`<group_id>:<workspace_id>`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"group_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the user group. Changing it replaces the assignment.",
				PlanModifiers:       replace,
				Validators:          idPart,
			},
			"workspace_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the workspace. Changing it replaces the assignment.",
				PlanModifiers:       replace,
				Validators:          idPart,
			},
			"role_names": schema.SetAttribute{
				Required:    true,
				ElementType: types.StringType,
				MarkdownDescription: "The workspace roles the members of the group get in the workspace, at least one, such as `user` " +
					"(Le Chat), `dev` (Studio) or `workspace_admin`. A change updates the assignment in place.",
				Validators: []validator.Set{
					setvalidator.SizeAtLeast(1),
					setvalidator.ValueStringsAre(stringvalidator.OneOf(workspaceRoleNames...)),
				},
			},
		},
	}
}

// Configure takes the client, and fails without the Admin API key.
func (r *UserGroupWorkspaceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerdata.Configure(req.ProviderData, providerdata.Admin, &resp.Diagnostics)
}

// Create gives the group access to the workspace, once it has checked that
// the group has none yet.
func (r *UserGroupWorkspaceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan userGroupWorkspaceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	groupID, workspaceID := plan.GroupID.ValueString(), plan.WorkspaceID.ValueString()

	// What the POST does with a workspace the group already has access to
	// is not documented: it may replace the roles someone else gave. The
	// create refuses, and points to an import instead.
	existing, err := r.client.FindUserGroupWorkspace(ctx, groupID, workspaceID)
	if err != nil {
		resp.Diagnostics.AddError("Cannot create the user group workspace assignment", err.Error())
		return
	}
	if existing != nil {
		resp.Diagnostics.AddAttributeError(path.Root("workspace_id"), "User group already assigned to the workspace",
			fmt.Sprintf("The user group %q already has access to the workspace %q (%s), with the roles %v. "+
				"Import it with an import block (id = %q) to manage it.",
				groupID, workspaceID, existing.WorkspaceName, roleNames(existing.Roles), groupID+":"+workspaceID))
		return
	}
	if err := r.client.AssignUserGroupWorkspace(ctx, groupID, workspaceID, stringsOf(plan.RoleNames)); err != nil {
		resp.Diagnostics.AddError("Cannot assign the user group to the workspace", err.Error())
		return
	}
	// The POST answers 201 with no body: the state is the plan, and the next
	// refresh reads the roles back.
	resp.Diagnostics.Append(setAssignmentState(ctx, groupID, workspaceID, stringsOf(plan.RoleNames), &resp.State, resp.Identity)...)
}

// Read looks for the workspace among the assignments of the group. An
// assignment or a group gone outside Terraform leaves the state.
func (r *UserGroupWorkspaceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state userGroupWorkspaceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	groupID, workspaceID := state.GroupID.ValueString(), state.WorkspaceID.ValueString()
	assignment, err := r.client.FindUserGroupWorkspace(ctx, groupID, workspaceID)
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Cannot read the user group workspace assignment", err.Error())
		return
	}
	// No assignment: the group lost its access, or the group is gone (a 404)
	// and its assignments with it.
	if assignment == nil {
		// Set the identity before removing: during an import, terraform-plugin-framework v1.19.0 fails a Read that removes the resource without one.
		if resp.Identity != nil {
			resp.Diagnostics.Append(resp.Identity.Set(ctx, userGroupWorkspaceIdentity{GroupID: state.GroupID, WorkspaceID: state.WorkspaceID})...)
		}
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(setAssignmentState(ctx, groupID, workspaceID, roleNames(assignment.Roles), &resp.State, resp.Identity)...)
}

// Update replaces the roles of the group in the workspace.
func (r *UserGroupWorkspaceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan userGroupWorkspaceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	groupID, workspaceID := plan.GroupID.ValueString(), plan.WorkspaceID.ValueString()
	if err := r.client.UpdateUserGroupWorkspace(ctx, groupID, workspaceID, stringsOf(plan.RoleNames)); err != nil {
		resp.Diagnostics.AddError("Cannot update the roles of the user group in the workspace", err.Error())
		return
	}
	resp.Diagnostics.Append(setAssignmentState(ctx, groupID, workspaceID, stringsOf(plan.RoleNames), &resp.State, resp.Identity)...)
}

// Delete removes the access of the group to the workspace. An assignment or
// a group already gone is not an error.
func (r *UserGroupWorkspaceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state userGroupWorkspaceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.RemoveUserGroupWorkspace(ctx, state.GroupID.ValueString(), state.WorkspaceID.ValueString())
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Cannot remove the user group from the workspace", err.Error())
	}
}

// ImportState takes `<group_id>:<workspace_id>`, or the identity.
func (r *UserGroupWorkspaceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	importComposite(ctx, req, resp, "group_id", "workspace_id")
}

// roleNames returns the names of the roles of an assignment. Reading them
// back as the names role_names sends is our reading of the specification:
// WorkspaceRoleRef gives a name and a UUID, and the guide lists the same
// names for GET /v1/admin/roles.
func roleNames(roles []client.UserGroupRole) []string {
	out := make([]string, 0, len(roles))
	for _, r := range roles {
		out = append(out, r.Name)
	}
	return out
}

// setAssignmentState writes the assignment and its identity.
func setAssignmentState(ctx context.Context, groupID, workspaceID string, roles []string, state *tfsdk.State, identity *tfsdk.ResourceIdentity) diag.Diagnostics {
	m := userGroupWorkspaceModel{
		ID:          types.StringValue(groupID + ":" + workspaceID),
		GroupID:     types.StringValue(groupID),
		WorkspaceID: types.StringValue(workspaceID),
		RoleNames:   setOf(roles),
	}
	diags := state.Set(ctx, &m)
	if identity != nil {
		diags.Append(identity.Set(ctx, userGroupWorkspaceIdentity{GroupID: m.GroupID, WorkspaceID: m.WorkspaceID})...)
	}
	return diags
}
