// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
	"github.com/ippontech/terraform-provider-mistral/internal/providerdata"
)

var _ datasource.DataSourceWithConfigure = &WorkspaceDataSource{}

// NewWorkspaceDataSource returns the mistral_workspace data source.
func NewWorkspaceDataSource() datasource.DataSource {
	return &WorkspaceDataSource{}
}

// WorkspaceDataSource reads a workspace created outside Terraform, such as
// the default workspace, for a mistral_workspace_member, a mistral_api_key or
// a user group assignment. It looks the workspace up by name, not by ID:
// users know workspaces by the name the Admin Panel shows, and "Workspace
// names must be unique within an Organization"
// (https://docs.mistral.ai/admin/workspaces/your-first-workspace).
type WorkspaceDataSource struct {
	client *client.Client
}

// workspaceDataModel is the state of the mistral_workspace data source, and
// an element of mistral_workspaces.
type workspaceDataModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	Icon        types.String `tfsdk:"icon"`
	IsDefault   types.Bool   `tfsdk:"is_default"`
}

// Metadata names the data source mistral_workspace.
func (d *WorkspaceDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_workspace"
}

// Schema declares the name to look up and the attributes of the workspace.
func (d *WorkspaceDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := workspaceDataAttributes()
	attrs["name"] = schema.StringAttribute{
		Required:            true,
		MarkdownDescription: "The name of the workspace, unique in the organization. It matches exactly, case included.",
		Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads an active workspace of the organization by its name, with the Admin API key.",
		Attributes:          attrs,
	}
}

// workspaceDataAttributes returns the computed attributes of a workspace, for
// both data sources.
func workspaceDataAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"id":          schema.StringAttribute{Computed: true, MarkdownDescription: "The workspace ID, a UUID."},
		"name":        schema.StringAttribute{Computed: true, MarkdownDescription: "The name of the workspace."},
		"description": schema.StringAttribute{Computed: true, MarkdownDescription: "The description of the workspace, `\"\"` when it has none."},
		"icon":        schema.StringAttribute{Computed: true, MarkdownDescription: "The icon of the workspace, `\"\"` when it has none."},
		"is_default":  schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether this is the default workspace of the organization."},
	}
}

// Configure takes the client of the provider, and fails without the Admin API
// key.
func (d *WorkspaceDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = providerdata.Configure(req.ProviderData, providerdata.Admin, &resp.Diagnostics)
}

// Read finds the active workspace with exactly that name, or fails.
func (d *WorkspaceDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var name types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("name"), &name)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// The search matches part of a name: keep the exact match only.
	ws, err := d.client.FindWorkspace(ctx, client.WorkspaceFilter{Search: name.ValueString()},
		func(w client.Workspace) bool { return w.Name == name.ValueString() })
	if err != nil {
		resp.Diagnostics.AddError("Cannot read the workspace", err.Error())
		return
	}
	if ws == nil {
		resp.Diagnostics.AddAttributeError(path.Root("name"), "Workspace not found",
			fmt.Sprintf("No active workspace of the organization is named %q.", name.ValueString()))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, workspaceData(*ws))...)
}

// workspaceData maps a workspace to the model of the data sources.
func workspaceData(ws client.Workspace) workspaceDataModel {
	return workspaceDataModel{
		ID:          types.StringValue(ws.ID),
		Name:        types.StringValue(ws.Name),
		Description: emptyIfNull(ws.Description),
		Icon:        emptyIfNull(ws.Icon),
		IsDefault:   types.BoolValue(ws.IsDefault),
	}
}
