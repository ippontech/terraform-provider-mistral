// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package connectors

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

var _ datasource.DataSourceWithConfigure = &ConnectorDataSource{}

// NewConnectorDataSource returns the mistral_connector data source.
func NewConnectorDataSource() datasource.DataSource {
	return &ConnectorDataSource{}
}

// ConnectorDataSource reads a connector built outside Terraform, such as in
// AI Studio, for the connector_id of a connector tool of a mistral_agent.
//
// It looks up by name: the ID of a connector built in AI Studio is a UUID
// users do not know, while its name is what they chose and is unique in the
// workspace (https://docs.mistral.ai/studio/connectors/management), and
// GET /v1/connectors/{connector_id_or_name} takes it.
type ConnectorDataSource struct {
	client *client.Client
}

// connectorDataModel is the state of the mistral_connector data source.
type connectorDataModel struct {
	Name        types.String `tfsdk:"name"`
	ID          types.String `tfsdk:"id"`
	Title       types.String `tfsdk:"title"`
	Description types.String `tfsdk:"description"`
	Server      types.String `tfsdk:"server"`
	Protocol    types.String `tfsdk:"protocol"`
	Visibility  types.String `tfsdk:"visibility"`
	CreatedAt   types.String `tfsdk:"created_at"`
	ModifiedAt  types.String `tfsdk:"modified_at"`
}

// Metadata names the type mistral_connector.
func (d *ConnectorDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_connector"
}

// Schema takes the name and returns the ID and what identifies the
// connector to a reader.
func (d *ConnectorDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	computed := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{Computed: true, MarkdownDescription: desc}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads a connector the API key can use, by its name.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The name of the connector, unique in the workspace.",
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"id":          computed("The connector ID. A `connector` tool of a `mistral_agent` takes it as `connector_id`."),
			"title":       computed("The human-readable title of the connector."),
			"description": computed("What the connector does."),
			"server":      computed("The URL of the connector server."),
			"protocol":    computed("`mcp` or `http`."),
			"visibility":  computed("Who can use the connector: `private`, `shared_workspace`, `shared_org`, or `shared_global` for a connector Mistral publishes."),
			"created_at":  computed("When the connector was created, in RFC 3339 format."),
			"modified_at": computed("When the connector was last modified, in RFC 3339 format."),
		},
	}
}

// Configure takes the client of the standard API key.
func (d *ConnectorDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = providerdata.Configure(req.ProviderData, providerdata.Workspace, &resp.Diagnostics)
}

// Read gets the connector by name. The API answers an ID the same way, so
// a connector whose name differs from the one asked for is not found: the
// lookup stays by name.
func (d *ConnectorDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var name types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("name"), &name)...)
	if resp.Diagnostics.HasError() {
		return
	}
	c, err := d.client.GetConnector(ctx, name.ValueString())
	if client.IsNotFound(err) || (err == nil && c.Name != name.ValueString()) {
		resp.Diagnostics.AddAttributeError(path.Root("name"), "Connector not found",
			fmt.Sprintf("No connector named %q is visible to the API key. A private connector is visible only to a key of its creator "+
				"with the \"Private and shared connectors\" access scope.", name.ValueString()))
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot read the connector", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, connectorDataModel{
		Name:        types.StringValue(c.Name),
		ID:          types.StringValue(c.ID),
		Title:       nonEmpty(c.Title),
		Description: types.StringValue(c.Description),
		Server:      nonEmpty(c.Server),
		Protocol:    types.StringValue(protocolOrDefault(c.Protocol)),
		Visibility:  types.StringValue(c.Visibility),
		CreatedAt:   timestamp(c.CreatedAt),
		ModifiedAt:  timestamp(c.ModifiedAt),
	})...)
}
