// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/identityschema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
	"github.com/ippontech/terraform-provider-mistral/internal/providerdata"
)

var (
	_ resource.ResourceWithConfigure   = &WorkspaceMemberResource{}
	_ resource.ResourceWithImportState = &WorkspaceMemberResource{}
	_ resource.ResourceWithIdentity    = &WorkspaceMemberResource{}
)

// defaultRoleName is the role a new workspace member gets when the request
// names none: "New Workspace members get Workspace contributor by default"
// (https://docs.mistral.ai/admin/identity-access/roles-permissions). The
// schema declares it as the Default of role_names.
const defaultRoleName = "workspace_contributor"

// noColon is an ID that the import ID can join to another with a colon.
var noColon = regexp.MustCompile(`^[^:]+$`)

// NewWorkspaceMemberResource returns the mistral_workspace_member resource.
func NewWorkspaceMemberResource() resource.Resource {
	return &WorkspaceMemberResource{}
}

// WorkspaceMemberResource manages the membership of one user in one
// workspace, with its roles: PATCH and DELETE
// /v1/admin/workspaces/{workspace_uuid}/users and .../remove-users
// (https://docs.mistral.ai/admin/admin-api/manage-workspaces). No route reads
// a membership: Read goes through findWorkspaceMember.
type WorkspaceMemberResource struct {
	client *client.Client
}

// workspaceMemberModel is the state of mistral_workspace_member.
type workspaceMemberModel struct {
	ID          types.String `tfsdk:"id"`
	WorkspaceID types.String `tfsdk:"workspace_id"`
	UserID      types.String `tfsdk:"user_id"`
	RoleNames   []string     `tfsdk:"role_names"`
}

// workspaceMemberIdentity is the resource identity of
// mistral_workspace_member.
type workspaceMemberIdentity struct {
	WorkspaceID types.String `tfsdk:"workspace_id"`
	UserID      types.String `tfsdk:"user_id"`
}

// Metadata names the resource mistral_workspace_member.
func (r *WorkspaceMemberResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_workspace_member"
}

// IdentitySchema declares the identity of a membership: the workspace and the
// user.
func (r *WorkspaceMemberResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = identityschema.Schema{
		Attributes: map[string]identityschema.Attribute{
			"workspace_id": identityschema.StringAttribute{RequiredForImport: true, Description: "The workspace ID."},
			"user_id":      identityschema.StringAttribute{RequiredForImport: true, Description: "The user ID."},
		},
	}
}

// Schema declares the arguments and attributes of mistral_workspace_member.
func (r *WorkspaceMemberResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	idValidators := []validator.String{stringvalidator.RegexMatches(noColon, "must not be empty or contain a colon")}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Makes a user of the organization a member of a workspace, with roles, with the Admin API key.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "`<workspace_id>:<user_id>`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"workspace_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the workspace. Changing it replaces the membership.",
				PlanModifiers:       replace,
				Validators:          idValidators,
			},
			"user_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the user, a member of the organization. Changing it replaces the membership.",
				PlanModifiers:       replace,
				Validators:          idValidators,
			},
			"role_names": schema.SetAttribute{
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
				Default:     setdefault.StaticValue(types.SetValueMust(types.StringType, []attr.Value{types.StringValue(defaultRoleName)})),
				MarkdownDescription: "The workspace roles of the user, whose permissions add up, such as `user`, `dev`, `mistral_code_user`, `billing`, " +
					"`workspace_contributor`, `workspace_admin` or `observability_viewer`. Defaults to `[\"workspace_contributor\"]`, the role " +
					"Mistral gives a new member. `GET /v1/admin/roles` lists the roles of the organization.",
				Validators: []validator.Set{
					setvalidator.SizeAtLeast(1),
					setvalidator.ValueStringsAre(stringvalidator.LengthAtLeast(1)),
				},
			},
		},
	}
}

// Configure takes the client of the provider, and fails without the Admin API
// key.
func (r *WorkspaceMemberResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerdata.Configure(req.ProviderData, providerdata.Admin, &resp.Diagnostics)
}

// Create checks that the user is not a member yet, then adds the user with the
// planned roles.
func (r *WorkspaceMemberResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan workspaceMemberModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	workspaceID, userID := plan.WorkspaceID.ValueString(), plan.UserID.ValueString()

	// The PATCH is an upsert: without this check, a create would take over
	// a membership that exists outside Terraform, such as the admin_user_id
	// of a workspace.
	existing, err := findWorkspaceMember(ctx, r.client, workspaceID, userID)
	if err != nil {
		resp.Diagnostics.AddError("Cannot create the workspace member", err.Error())
		return
	}
	if existing != nil {
		resp.Diagnostics.AddAttributeError(path.Root("user_id"), "Workspace member already exists",
			fmt.Sprintf("The user %q is already a member of the workspace %q, with the roles %s. Import the membership with an import block "+
				"(id = %q) to manage it.", userID, workspaceID, strings.Join(existing.RoleNames, ", "), workspaceID+":"+userID))
		return
	}
	r.put(ctx, plan, &resp.State, resp.Identity, &resp.Diagnostics)
}

// Read refreshes the roles through findWorkspaceMember, and removes the
// membership from the state when the user is no longer a member.
func (r *WorkspaceMemberResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state workspaceMemberModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	found, err := findWorkspaceMember(ctx, r.client, state.WorkspaceID.ValueString(), state.UserID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Cannot read the workspace member", err.Error())
		return
	}
	if found == nil {
		// Set the identity before removing: during an import, terraform-plugin-framework v1.19.0 fails a Read that removes the resource without one.
		if resp.Identity != nil {
			resp.Diagnostics.Append(resp.Identity.Set(ctx, workspaceMemberIdentity{WorkspaceID: state.WorkspaceID, UserID: state.UserID})...)
		}
		resp.State.RemoveResource(ctx)
		return
	}
	state.RoleNames = found.RoleNames
	resp.Diagnostics.Append(writeWorkspaceMember(ctx, state, &resp.State, resp.Identity)...)
}

// Update sets the planned roles.
func (r *WorkspaceMemberResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan workspaceMemberModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.put(ctx, plan, &resp.State, resp.Identity, &resp.Diagnostics)
}

// Delete removes the user from the workspace. The user stays in the
// organization.
func (r *WorkspaceMemberResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state workspaceMemberModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	workspaceID, userID := state.WorkspaceID.ValueString(), state.UserID.ValueString()
	notDeleted, err := r.client.RemoveWorkspaceMembers(ctx, workspaceID, []string{userID})
	if client.IsNotFound(err) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot remove the workspace member", err.Error())
		return
	}
	if !slices.Contains(notDeleted, userID) {
		return
	}
	// The API lists the users it "could not remove" without a reason. Our
	// reading is that a user who is no longer a member is one of them, so
	// the membership itself decides whether the delete failed.
	still, err := findWorkspaceMember(ctx, r.client, workspaceID, userID)
	if err != nil {
		resp.Diagnostics.AddError("Cannot remove the workspace member", err.Error())
		return
	}
	if still != nil {
		resp.Diagnostics.AddError("Cannot remove the workspace member",
			fmt.Sprintf("Mistral did not remove the user %q from the workspace %q, and gave no reason.", userID, workspaceID))
	}
}

// ImportState imports a membership by <workspace_id>:<user_id> or by its
// identity.
func (r *WorkspaceMemberResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	var workspaceID, userID string
	if req.ID != "" {
		var ok bool
		workspaceID, userID, ok = strings.Cut(req.ID, ":")
		if !ok || workspaceID == "" || userID == "" || strings.Contains(userID, ":") {
			resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("The ID must be <workspace_id>:<user_id>, got %q.", req.ID))
			return
		}
	} else {
		var id workspaceMemberIdentity
		resp.Diagnostics.Append(req.Identity.Get(ctx, &id)...)
		if resp.Diagnostics.HasError() {
			return
		}
		workspaceID, userID = id.WorkspaceID.ValueString(), id.UserID.ValueString()
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("workspace_id"), workspaceID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("user_id"), userID)...)
}

// put adds the user to the workspace with the planned roles, or sets the
// roles of a member, and writes the plan as the state. The answer holds
// counts only, so the next Read is the one that reads the roles back.
func (r *WorkspaceMemberResource) put(ctx context.Context, plan workspaceMemberModel, state *tfsdk.State, identity *tfsdk.ResourceIdentity, diags *diag.Diagnostics) {
	workspaceID, userID := plan.WorkspaceID.ValueString(), plan.UserID.ValueString()
	// Sorted, so that a request does not depend on the order of the set.
	member := client.WorkspaceMember{UserID: userID, RoleNames: slices.Sorted(slices.Values(plan.RoleNames))}
	if err := r.client.PutWorkspaceMembers(ctx, workspaceID, []client.WorkspaceMember{member}); err != nil {
		diags.AddError("Cannot set the workspace member", err.Error())
		return
	}
	diags.Append(writeWorkspaceMember(ctx, plan, state, identity)...)
}

// workspaceMembership is what findWorkspaceMember reads of a membership.
type workspaceMembership struct {
	// RoleNames are the roles of the user in the workspace, by name.
	RoleNames []string
}

// findWorkspaceMember returns the membership of the user in the workspace,
// or nil when the user is not a member, or no longer in the organization. No
// route lists the members of a workspace: it reads the workspaces of the
// user (GET /v1/admin/users/{user_id}), the narrowest read there is. It is
// the one function to replace when Mistral adds a read of a membership.
func findWorkspaceMember(ctx context.Context, cl *client.Client, workspaceID, userID string) (*workspaceMembership, error) {
	memberships, err := cl.ListUserMemberships(ctx, userID)
	if client.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(memberships, func(m client.WorkspaceMembership) bool { return m.WorkspaceID == workspaceID })
	if i < 0 {
		return nil, nil
	}
	names, err := roleNames(ctx, cl, memberships[i].RawRoles)
	if err != nil {
		return nil, err
	}
	return &workspaceMembership{RoleNames: names}, nil
}

// roleNames turns the raw roles of a membership into the names the
// configuration and the PATCH use. A membership reads its roles back as
// UUIDs, which GET /v1/admin/roles names, or as the legacy codes A and M. A
// raw role the list does not name is kept as it is, so that the plan shows
// it and the next apply sets the configured roles.
func roleNames(ctx context.Context, cl *client.Client, raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	roles, err := cl.ListWorkspaceRoles(ctx)
	if err != nil {
		return nil, err
	}
	nameByID := make(map[string]string, len(roles))
	for _, role := range roles {
		nameByID[role.ID] = role.Name
	}
	names := make([]string, 0, len(raw))
	for _, r := range raw {
		if name, ok := nameByID[r]; ok {
			r = name
		}
		names = append(names, r)
	}
	return names, nil
}

// writeWorkspaceMember sets m as the state, with its ID, and the resource
// identity.
func writeWorkspaceMember(ctx context.Context, m workspaceMemberModel, state *tfsdk.State, identity *tfsdk.ResourceIdentity) diag.Diagnostics {
	m.ID = types.StringValue(m.WorkspaceID.ValueString() + ":" + m.UserID.ValueString())
	diags := state.Set(ctx, &m)
	if identity != nil {
		diags.Append(identity.Set(ctx, workspaceMemberIdentity{WorkspaceID: m.WorkspaceID, UserID: m.UserID})...)
	}
	return diags
}
