// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
	"github.com/ippontech/terraform-provider-mistral/internal/providerdata"
)

var _ datasource.DataSourceWithConfigure = &AgentDataSource{}

// NewAgentDataSource returns the mistral_agent data source.
func NewAgentDataSource() datasource.DataSource {
	return &AgentDataSource{}
}

// AgentDataSource reads an agent created outside Terraform, such as in AI
// Studio, for a mistral_agent_alias or the handoffs of a mistral_agent.
type AgentDataSource struct {
	client *client.Client
}

type agentDataModel struct {
	ID           types.String `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	Model        types.String `tfsdk:"model"`
	Description  types.String `tfsdk:"description"`
	Instructions types.String `tfsdk:"instructions"`
	Version      types.Int64  `tfsdk:"version"`
	CreatedAt    types.String `tfsdk:"created_at"`
	UpdatedAt    types.String `tfsdk:"updated_at"`
}

func (d *AgentDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_agent"
}

func (d *AgentDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	computed := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{Computed: true, MarkdownDescription: desc}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads an agent of the workspace of the API key, at its active version.",
		Attributes: map[string]schema.Attribute{
			"id":           schema.StringAttribute{Required: true, MarkdownDescription: "The agent ID."},
			"name":         computed("The name of the agent."),
			"model":        computed("The model the agent runs on."),
			"description":  computed("The description of the agent."),
			"instructions": computed("The instruction prompt of the agent."),
			"version": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "The active version of the agent.",
			},
			"created_at": computed("When the agent was created, in RFC 3339 format."),
			"updated_at": computed("When the agent was last updated, in RFC 3339 format."),
		},
	}
}

func (d *AgentDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = providerdata.Configure(req.ProviderData, providerdata.Workspace, &resp.Diagnostics)
}

func (d *AgentDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var id types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		return
	}
	a, err := d.client.GetAgent(ctx, id.ValueString())
	if client.IsNotFound(err) {
		resp.Diagnostics.AddAttributeError(path.Root("id"), "Agent not found",
			fmt.Sprintf("No agent %q is visible to the API key: %v", id.ValueString(), err))
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot read the agent", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, agentDataModel{
		ID:           types.StringValue(a.ID),
		Name:         types.StringValue(a.Name),
		Model:        types.StringValue(a.Model),
		Description:  nonEmpty(a.Description),
		Instructions: nonEmpty(a.Instructions),
		Version:      types.Int64Value(a.Version),
		CreatedAt:    timestamp(a.CreatedAt),
		UpdatedAt:    timestamp(a.UpdatedAt),
	})...)
}
