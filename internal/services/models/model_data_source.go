// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package models

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
	"github.com/ippontech/terraform-provider-mistral/internal/providerdata"
)

var _ datasource.DataSourceWithConfigure = &ModelDataSource{}

// NewModelDataSource returns the mistral_model data source.
func NewModelDataSource() datasource.DataSource {
	return &ModelDataSource{}
}

// ModelDataSource reads one model: GET /v1/models/{model_id}
// (https://docs.mistral.ai/api/endpoint/models), which takes a model ID or
// an alias.
type ModelDataSource struct {
	client *client.Client
}

func (d *ModelDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_model"
}

func (d *ModelDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attributes := modelComputedAttributes()
	attributes["id"] = schema.StringAttribute{
		Required:            true,
		MarkdownDescription: "The model ID, such as `mistral-medium-2604`, or one of its aliases, such as `mistral-medium-latest`.",
	}
	attributes["canonical_id"] = schema.StringAttribute{
		Computed:            true,
		MarkdownDescription: "The ID of the model the API answered with: the same as `id`, unless `id` is an alias.",
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads a model the workspace of the API key can use, by its ID or one of its aliases.",
		Attributes:          attributes,
	}
}

func (d *ModelDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = providerdata.Configure(req.ProviderData, providerdata.Workspace, &resp.Diagnostics)
}

func (d *ModelDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var id types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		return
	}

	model, err := d.client.GetModel(ctx, id.ValueString())
	if client.IsNotFound(err) {
		resp.Diagnostics.AddAttributeError(path.Root("id"), "Model not found",
			fmt.Sprintf("No model %q is available to the workspace of the API key: %v", id.ValueString(), err))
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot read the model", err.Error())
		return
	}

	resp.Diagnostics.Append(setModelState(ctx, &resp.State, id.ValueString(), *model)...)
	if model.Deprecation != nil {
		replacement := "Mistral names no replacement."
		if model.DeprecationReplacementModel != nil {
			replacement = fmt.Sprintf("Mistral recommends %q instead.", *model.DeprecationReplacementModel)
		}
		resp.Diagnostics.AddAttributeWarning(path.Root("id"), "Deprecated model",
			fmt.Sprintf("The model %q is deprecated on %s. %s", model.ID, model.Deprecation.UTC().Format("2006-01-02"), replacement))
	}
}

// setModelState writes the model to the state, keeping id as configured.
func setModelState(ctx context.Context, state *tfsdk.State, id string, model client.Model) (diags diag.Diagnostics) {
	for name, v := range modelValues(model) {
		if name == "id" {
			continue
		}
		diags.Append(state.SetAttribute(ctx, path.Root(name), v)...)
	}
	diags.Append(state.SetAttribute(ctx, path.Root("id"), id)...)
	diags.Append(state.SetAttribute(ctx, path.Root("canonical_id"), model.ID)...)
	return diags
}
