// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package users

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

var _ datasource.DataSourceWithConfigure = &UsersDataSource{}

// NewUsersDataSource returns the mistral_users data source.
func NewUsersDataSource() datasource.DataSource {
	return &UsersDataSource{}
}

// UsersDataSource lists the members of the organization: every page of GET
// /v1/admin/users, without its pending invites, which belong to
// mistral_invite.
type UsersDataSource struct {
	client *client.Client
}

// Metadata names the data source mistral_users.
func (d *UsersDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_users"
}

// Schema is one list of the members of userComputedAttributes.
func (d *UsersDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the members of the organization, in the order the API returns them, without the pending invites.",
		Attributes: map[string]schema.Attribute{
			"users": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "The members of the organization.",
				NestedObject:        schema.NestedAttributeObject{Attributes: userComputedAttributes()},
			},
		},
	}
}

// Configure takes the client, with the Admin API key.
func (d *UsersDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = providerdata.Configure(req.ProviderData, providerdata.Admin, &resp.Diagnostics)
}

// Read walks every page of the members.
func (d *UsersDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	dir, err := d.client.ListUsers(ctx, "")
	if err != nil {
		resp.Diagnostics.AddError("Cannot list the users", err.Error())
		return
	}
	objects := make([]attr.Value, 0, len(dir.Users))
	for _, u := range dir.Users {
		objects = append(objects, types.ObjectValueMust(userAttrTypes, userValues(u)))
	}
	list := types.ListValueMust(types.ObjectType{AttrTypes: userAttrTypes}, objects)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("users"), list)...)
}
