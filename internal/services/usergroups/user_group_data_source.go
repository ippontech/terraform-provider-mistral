// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package usergroups

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
	"github.com/ippontech/terraform-provider-mistral/internal/providerdata"
)

var _ datasource.DataSourceWithConfigure = &UserGroupDataSource{}

// NewUserGroupDataSource returns the mistral_user_group data source.
func NewUserGroupDataSource() datasource.DataSource {
	return &UserGroupDataSource{}
}

// UserGroupDataSource reads a user group that Terraform does not manage, by
// its name: a group an identity provider syncs through SCIM, or one created
// in the Admin Panel, for the group_id of a mistral_user_group_workspace or
// a mistral_user_group_member, or the parent_group_ids of a
// mistral_user_group. Users know such a group by its name, not its UUID.
type UserGroupDataSource struct {
	client *client.Client
}

// userGroupDataModel is the state of the mistral_user_group data source.
type userGroupDataModel struct {
	ID                types.String `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	Description       types.String `tfsdk:"description"`
	ParentGroupIDs    types.Set    `tfsdk:"parent_group_ids"`
	OrganizationRole  types.String `tfsdk:"organization_role"`
	ExternallyManaged types.Bool   `tfsdk:"externally_managed"`
}

// Metadata names the type mistral_user_group.
func (d *UserGroupDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user_group"
}

// Schema looks up by name only: the key users know a synced group by.
func (d *UserGroupDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads a user group of the organization of the Admin API key by its exact name.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The exact name of the group, case included. The read fails when no group, or more than one, has this name.",
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 255)},
			},
			"id":          schema.StringAttribute{Computed: true, MarkdownDescription: "The user group ID, a UUID."},
			"description": schema.StringAttribute{Computed: true, MarkdownDescription: "The description of the group."},
			"parent_group_ids": schema.SetAttribute{
				Computed: true, ElementType: types.StringType,
				MarkdownDescription: "The IDs of the groups this group is directly nested inside.",
			},
			"organization_role": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The organization role the group gives its members, such as `member`.",
			},
			"externally_managed": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether an identity provider manages the group, through SSO or SCIM.",
			},
		},
	}
}

// Configure takes the client, and fails without the Admin API key.
func (d *UserGroupDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = providerdata.Configure(req.ProviderData, providerdata.Admin, &resp.Diagnostics)
}

// Read lists the groups the search matches, every page, and keeps the one
// whose name is exactly the configured one.
func (d *UserGroupDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var name types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("name"), &name)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// search is the narrowest filter the list offers. How it matches (a
	// prefix, a substring, with or without case) is not documented, so the
	// names are compared here, exactly.
	groups, err := d.client.ListUserGroups(ctx, name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Cannot list the user groups", err.Error())
		return
	}
	var matches []client.UserGroup
	for _, g := range groups {
		if g.Name == name.ValueString() {
			matches = append(matches, g)
		}
	}
	switch len(matches) {
	case 0:
		resp.Diagnostics.AddAttributeError(path.Root("name"), "User group not found",
			fmt.Sprintf("No user group of the organization is named %q.", name.ValueString()))
		return
	case 1:
	default:
		ids := make([]string, len(matches))
		for i, g := range matches {
			ids[i] = g.ID
		}
		resp.Diagnostics.AddAttributeError(path.Root("name"), "Several user groups have this name",
			fmt.Sprintf("%d user groups are named %q: %s. Rename them, or reference the group by its ID.",
				len(matches), name.ValueString(), strings.Join(ids, ", ")))
		return
	}
	g := matches[0]
	resp.Diagnostics.Append(resp.State.Set(ctx, userGroupDataModel{
		ID:                types.StringValue(g.ID),
		Name:              types.StringValue(g.Name),
		Description:       nonEmpty(g.Description),
		ParentGroupIDs:    setOf(g.ParentGroupIDs),
		OrganizationRole:  organizationRoleState(g.OrganizationRole),
		ExternallyManaged: externallyManaged(g),
	})...)
}
