// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
	"github.com/ippontech/terraform-provider-mistral/internal/providerdata"
)

var _ datasource.DataSourceWithConfigure = &WorkspacesDataSource{}

// NewWorkspacesDataSource returns the mistral_workspaces data source.
func NewWorkspacesDataSource() datasource.DataSource {
	return &WorkspacesDataSource{}
}

// WorkspacesDataSource lists the active workspaces of the organization,
// walking every page of GET /v1/admin/workspaces. Archived workspaces are
// left out, as the list does by default: they serve nothing.
type WorkspacesDataSource struct {
	client *client.Client
}

// Metadata names the data source mistral_workspaces.
func (d *WorkspacesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_workspaces"
}

// Schema declares the list of workspaces.
func (d *WorkspacesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the active workspaces of the organization, with the Admin API key, in the order the API returns them.",
		Attributes: map[string]schema.Attribute{
			"workspaces": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "The workspaces.",
				NestedObject:        schema.NestedAttributeObject{Attributes: workspaceDataAttributes()},
			},
		},
	}
}

// Configure takes the client of the provider, and fails without the Admin API
// key.
func (d *WorkspacesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = providerdata.Configure(req.ProviderData, providerdata.Admin, &resp.Diagnostics)
}

// Read lists every active workspace, walking every page.
func (d *WorkspacesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	all, err := d.client.ListWorkspaces(ctx, client.WorkspaceFilter{})
	if err != nil {
		resp.Diagnostics.AddError("Cannot list the workspaces", err.Error())
		return
	}
	workspaces := make([]workspaceDataModel, 0, len(all))
	for _, ws := range all {
		workspaces = append(workspaces, workspaceData(ws))
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("workspaces"), workspaces)...)
}
