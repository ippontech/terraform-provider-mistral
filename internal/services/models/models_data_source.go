// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package models

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
	"github.com/ippontech/terraform-provider-mistral/internal/providerdata"
)

var _ datasource.DataSourceWithConfigure = &ModelsDataSource{}

// NewModelsDataSource returns the mistral_models data source.
func NewModelsDataSource() datasource.DataSource {
	return &ModelsDataSource{}
}

// ModelsDataSource lists the models: GET /v1/models
// (https://docs.mistral.ai/api/endpoint/models), which is not paginated.
type ModelsDataSource struct {
	client *client.Client
}

func (d *ModelsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_models"
}

func (d *ModelsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the models the workspace of the API key can use, Mistral models and fine-tuned models, " +
			"in the order the API returns them.",
		Attributes: map[string]schema.Attribute{
			"models": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "The models.",
				NestedObject:        schema.NestedAttributeObject{Attributes: modelComputedAttributes()},
			},
		},
	}
}

func (d *ModelsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = providerdata.Configure(req.ProviderData, providerdata.Workspace, &resp.Diagnostics)
}

func (d *ModelsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	models, err := d.client.ListModels(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Cannot list the models", err.Error())
		return
	}
	objects := make([]attr.Value, 0, len(models))
	for _, m := range models {
		objects = append(objects, modelObject(m))
	}
	list := types.ListValueMust(types.ObjectType{AttrTypes: modelAttrTypes}, objects)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("models"), list)...)
}
