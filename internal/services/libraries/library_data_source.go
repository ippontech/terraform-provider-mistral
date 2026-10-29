// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package libraries

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

var _ datasource.DataSourceWithConfigure = &LibraryDataSource{}

// NewLibraryDataSource returns the mistral_library data source.
func NewLibraryDataSource() datasource.DataSource {
	return &LibraryDataSource{}
}

// LibraryDataSource reads a library created outside Terraform, such as in
// AI Studio or Vibe, for the library_ids of a document_library tool of a
// mistral_agent.
type LibraryDataSource struct {
	client *client.Client
}

// Metadata names the data source mistral_library.
func (d *LibraryDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_library"
}

// Schema describes the mistral_library data source: the attributes of the
// resource, all computed but id.
func (d *LibraryDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	computed := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{Computed: true, MarkdownDescription: desc}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads a library that the API key can access, by its ID.",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Required: true, MarkdownDescription: "The library ID."},
			"name":        computed("The name of the library."),
			"description": computed("The description of the library."),
			"owner_type":  computed("Who owns the library: `Workspace` or `User`."),
			"owner_id":    computed("The ID of the workspace or the user that owns the library."),
			"nb_documents": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "The number of documents of the library.",
			},
			"total_size": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "The total size of the documents of the library.",
			},
			"created_at": computed("When the library was created, in RFC 3339 format."),
			"updated_at": computed("When the library was last updated, in RFC 3339 format."),
		},
	}
}

// Configure takes the client of the standard API key.
func (d *LibraryDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = providerdata.Configure(req.ProviderData, providerdata.Workspace, &resp.Diagnostics)
}

// Read reads the library by its ID; a library the key cannot see is an
// error on id.
func (d *LibraryDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var id types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		return
	}
	lib, err := d.client.GetLibrary(ctx, id.ValueString())
	if client.IsNotFound(err) {
		resp.Diagnostics.AddAttributeError(path.Root("id"), "Library not found",
			fmt.Sprintf("No library %q is visible to the API key: %v", id.ValueString(), err))
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot read the library", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, libraryState(*lib))...)
}
