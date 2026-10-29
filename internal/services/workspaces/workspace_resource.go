// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package workspaces holds mistral_workspace, mistral_workspace_member and
// the mistral_workspace and mistral_workspaces data sources: the workspaces
// of the organization and their members, through the Admin API.
package workspaces

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/identityschema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
	"github.com/ippontech/terraform-provider-mistral/internal/providerdata"
)

var (
	_ resource.ResourceWithConfigure   = &WorkspaceResource{}
	_ resource.ResourceWithImportState = &WorkspaceResource{}
	_ resource.ResourceWithIdentity    = &WorkspaceResource{}
)

// NewWorkspaceResource returns the mistral_workspace resource.
func NewWorkspaceResource() resource.Resource {
	return &WorkspaceResource{}
}

// WorkspaceResource manages a workspace of the organization:
// /v1/admin/workspaces (https://docs.mistral.ai/admin/admin-api/manage-workspaces).
// The API has no read by ID, so Read goes through findWorkspace, and its
// delete archives the workspace for good.
type WorkspaceResource struct {
	client *client.Client
}

// workspaceModel is the state of mistral_workspace.
type workspaceModel struct {
	ID               types.String `tfsdk:"id"`
	Name             types.String `tfsdk:"name"`
	Description      types.String `tfsdk:"description"`
	Icon             types.String `tfsdk:"icon"`
	AdminUserID      types.String `tfsdk:"admin_user_id"`
	AddAllOrgMembers types.Bool   `tfsdk:"add_all_org_members"`
	IsDefault        types.Bool   `tfsdk:"is_default"`
}

// workspaceIdentity is the resource identity of mistral_workspace.
type workspaceIdentity struct {
	ID types.String `tfsdk:"id"`
}

// Metadata names the resource mistral_workspace.
func (r *WorkspaceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_workspace"
}

// IdentitySchema declares the identity of a workspace: its ID.
func (r *WorkspaceResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = identityschema.Schema{
		Attributes: map[string]identityschema.Attribute{
			"id": identityschema.StringAttribute{RequiredForImport: true, Description: "The workspace ID."},
		},
	}
}

// Schema declares the arguments and attributes of mistral_workspace.
func (r *WorkspaceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a workspace of the organization, with the Admin API key. Destroying the resource archives the workspace, which cannot be undone.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The workspace ID, a UUID.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The name of the workspace, unique in the organization, of at most 255 characters.",
				Validators:          []validator.String{stringvalidator.UTF8LengthBetween(1, 255)},
			},
			"description": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
				MarkdownDescription: "The description of the workspace, of at most 255 characters. Defaults to `\"\"`, as at the API.",
				Validators:          []validator.String{stringvalidator.UTF8LengthAtMost(255)},
			},
			"icon": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
				MarkdownDescription: "The icon of the workspace, of at most 2 characters, whose format the documentation does not describe. Defaults to `\"\"`, as at the API.",
				Validators:          []validator.String{stringvalidator.UTF8LengthAtMost(2)},
			},
			"admin_user_id": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "The ID of the user who gets the Workspace Admin role when the workspace is created. " +
					"The API takes it at creation only: changing it later records the new value in the state, with a warning, and changes nothing at Mistral.",
				Validators:    []validator.String{stringvalidator.LengthAtLeast(1)},
				PlanModifiers: []planmodifier.String{createOnly{}},
			},
			"add_all_org_members": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(false),
				MarkdownDescription: "Whether the create adds every member of the organization to the workspace. Defaults to `false`. " +
					"The API takes it at creation only: changing it later records the new value in the state, with a warning, and changes nothing at Mistral.",
				PlanModifiers: []planmodifier.Bool{createOnly{}},
			},
			"is_default": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether this is the default workspace of the organization, which cannot be archived.",
				PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

// Configure takes the client of the provider, and fails without the Admin API
// key.
func (r *WorkspaceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerdata.Configure(req.ProviderData, providerdata.Admin, &resp.Diagnostics)
}

// Create creates the workspace, with its admin and, when asked, every member
// of the organization.
func (r *WorkspaceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan workspaceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ws, err := r.client.CreateWorkspace(ctx, client.CreateWorkspaceRequest{
		Name:             plan.Name.ValueString(),
		Description:      plan.Description.ValueString(),
		Icon:             plan.Icon.ValueString(),
		AddAllOrgMembers: plan.AddAllOrgMembers.ValueBool(),
		AdminUserID:      plan.AdminUserID.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Cannot create the workspace", err.Error())
		return
	}
	resp.Diagnostics.Append(writeWorkspace(ctx, workspaceState(*ws, plan), &resp.State, resp.Identity)...)
}

// Read refreshes the workspace through findWorkspace. A workspace that is gone
// leaves the state, and so does an archived one, with a warning.
func (r *WorkspaceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state workspaceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ws, status, err := findWorkspace(ctx, r.client, state.ID.ValueString(), state.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Cannot read the workspace", err.Error())
		return
	}
	switch status {
	case workspaceGone:
		removeWorkspace(ctx, state.ID, resp)
	case workspaceArchived:
		// The one object that leaves the state while the API still returns
		// it (CLAUDE.md, "State after status changes"): an archived
		// workspace serves nothing, and the archive cannot be undone.
		resp.Diagnostics.AddWarning("Workspace archived outside Terraform",
			fmt.Sprintf("The workspace %q (%s) was archived outside Terraform. An archived workspace serves nothing and cannot be restored, "+
				"so it is removed from the state: the next apply creates a new workspace.", state.Name.ValueString(), state.ID.ValueString()))
		removeWorkspace(ctx, state.ID, resp)
	case workspaceActive:
		resp.Diagnostics.Append(writeWorkspace(ctx, workspaceState(*ws, state), &resp.State, resp.Identity)...)
	}
}

// removeWorkspace removes the workspace id from the state.
func removeWorkspace(ctx context.Context, id types.String, resp *resource.ReadResponse) {
	// Set the identity before removing: during an import, terraform-plugin-framework v1.19.0 fails a Read that removes the resource without one.
	if resp.Identity != nil {
		resp.Diagnostics.Append(resp.Identity.Set(ctx, workspaceIdentity{ID: id})...)
	}
	resp.State.RemoveResource(ctx)
}

// Update sends the name, description and icon, or nothing when only a create-
// only argument changed.
func (r *WorkspaceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state workspaceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if plan.Name.Equal(state.Name) && plan.Description.Equal(state.Description) && plan.Icon.Equal(state.Icon) {
		// Only a create-only argument changed (createOnly): no route takes
		// it, so the state records the new value and nothing is sent.
		resp.Diagnostics.Append(writeWorkspace(ctx, plan, &resp.State, resp.Identity)...)
		return
	}
	ws, err := r.client.UpdateWorkspace(ctx, state.ID.ValueString(), client.UpdateWorkspaceRequest{
		Name:        plan.Name.ValueString(),
		Description: plan.Description.ValueString(),
		Icon:        plan.Icon.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Cannot update the workspace", err.Error())
		return
	}
	resp.Diagnostics.Append(writeWorkspace(ctx, workspaceState(*ws, plan), &resp.State, resp.Identity)...)
}

// Delete archives the workspace: the API has no other delete, and an
// archived workspace cannot be restored.
func (r *WorkspaceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var id types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.ArchiveWorkspace(ctx, id.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Cannot archive the workspace", err.Error())
	}
}

// ImportState imports a workspace by its ID or its identity. admin_user_id
// stays null until the next apply.
func (r *WorkspaceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughWithIdentity(ctx, path.Root("id"), path.Root("id"), req, resp)
}

// workspaceStatus is what findWorkspace learns about a workspace ID.
type workspaceStatus int

const (
	// workspaceGone means no workspace of the organization has the ID.
	workspaceGone workspaceStatus = iota + 1
	// workspaceActive means the workspace serves its members.
	workspaceActive
	// workspaceArchived means the workspace was archived, for good.
	workspaceArchived
)

// findWorkspace returns the workspace id, with its status. There is no read
// by ID (GET /v1/admin/workspaces/{workspace_uuid} does not exist), so it
// lists, in order:
//  1. the active workspaces whose name matches name, the narrowest filter of
//     GET /v1/admin/workspaces (search), when name is known;
//  2. every active workspace, since a workspace renamed outside Terraform, or
//     imported, escapes that search;
//  3. the archived workspaces too (is_archived=true): the list answers no
//     archived flag, so a workspace found only there is archived.
//
// It is the one function to replace when Mistral adds a read by ID.
func findWorkspace(ctx context.Context, cl *client.Client, id, name string) (*client.Workspace, workspaceStatus, error) {
	byID := func(w client.Workspace) bool { return w.ID == id }
	activeFilters := []client.WorkspaceFilter{{}}
	if name != "" {
		activeFilters = []client.WorkspaceFilter{{Search: name}, {}}
	}
	for _, f := range activeFilters {
		ws, err := cl.FindWorkspace(ctx, f, byID)
		if err != nil {
			return nil, 0, err
		}
		if ws != nil {
			return ws, workspaceActive, nil
		}
	}
	archived, err := cl.FindWorkspace(ctx, client.WorkspaceFilter{IncludeArchived: true}, byID)
	switch {
	case err != nil:
		return nil, 0, err
	case archived != nil:
		return nil, workspaceArchived, nil
	}
	return nil, workspaceGone, nil
}

// workspaceState maps ws to the state. prior is the plan or state the call
// started from: the API never returns the create-only arguments, so they
// keep their prior value.
func workspaceState(ws client.Workspace, prior workspaceModel) workspaceModel {
	addAll := prior.AddAllOrgMembers
	if addAll.IsNull() {
		// After an import: Terraform asked nothing at creation, which is
		// the default, false. admin_user_id has no default and stays null.
		addAll = types.BoolValue(false)
	}
	return workspaceModel{
		ID:               types.StringValue(ws.ID),
		Name:             types.StringValue(ws.Name),
		Description:      emptyIfNull(ws.Description),
		Icon:             emptyIfNull(ws.Icon),
		AdminUserID:      prior.AdminUserID,
		AddAllOrgMembers: addAll,
		IsDefault:        types.BoolValue(ws.IsDefault),
	}
}

// writeWorkspace sets m as the state, and its ID as the resource identity.
func writeWorkspace(ctx context.Context, m workspaceModel, state *tfsdk.State, identity *tfsdk.ResourceIdentity) diag.Diagnostics {
	diags := state.Set(ctx, &m)
	if identity != nil {
		diags.Append(identity.Set(ctx, workspaceIdentity{ID: m.ID})...)
	}
	return diags
}

// emptyIfNull reads a description or an icon: the specification types both
// as nullable in an answer, and "" is their default at creation, which the
// schema declares. Reading null as "" keeps the two from alternating.
func emptyIfNull(s *string) types.String {
	if s == nil {
		return types.StringValue("")
	}
	return types.StringValue(*s)
}
