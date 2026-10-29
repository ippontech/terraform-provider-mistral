// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package roles holds the mistral_roles data source: the organization and
// workspace roles of the Admin API, whose names and UUIDs the invites,
// workspace memberships and user groups assign
// (https://docs.mistral.ai/admin/admin-api/manage-groups-roles#roles).
package roles

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

var _ datasource.DataSourceWithConfigure = &RolesDataSource{}

// NewRolesDataSource returns the mistral_roles data source.
func NewRolesDataSource() datasource.DataSource {
	return &RolesDataSource{}
}

// RolesDataSource lists the roles: GET /v1/admin/roles, which is not
// paginated. It is not the list of the roles a service account can hold,
// which GET /v1/service-accounts/assignable-roles gives per workspace with a
// standard key.
type RolesDataSource struct {
	client *client.Client
}

// roleAttrTypes are the attributes of one role.
var roleAttrTypes = map[string]attr.Type{
	"id":          types.StringType,
	"name":        types.StringType,
	"description": types.StringType,
	"is_custom":   types.BoolType,
}

// Metadata names the data source mistral_roles.
func (d *RolesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_roles"
}

// Schema is the two lists of the answer, organization and workspace roles.
func (d *RolesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the organization and workspace roles that members, invites and user groups take, with their names and UUIDs.",
		Attributes: map[string]schema.Attribute{
			"organization_roles": rolesAttribute("The organization roles, such as `member`, `billing_manager` and `organization_admin`."),
			"workspace_roles":    rolesAttribute("The workspace roles, such as `user`, `dev` and `workspace_admin`."),
		},
	}
}

// rolesAttribute is the schema of one list of roles.
func rolesAttribute(desc string) schema.ListNestedAttribute {
	return schema.ListNestedAttribute{
		Computed:            true,
		MarkdownDescription: desc,
		NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Computed: true, MarkdownDescription: "The role UUID, which the `roles` fields of the Admin API take."},
			"name":        schema.StringAttribute{Computed: true, MarkdownDescription: "The role name, which the `role_names` fields of the Admin API take."},
			"description": schema.StringAttribute{Computed: true, MarkdownDescription: "A short description of the role."},
			"is_custom":   schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the organization defined the role, rather than Mistral."},
		}},
	}
}

// Configure takes the client, with the Admin API key.
func (d *RolesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = providerdata.Configure(req.ProviderData, providerdata.Admin, &resp.Diagnostics)
}

// Read lists the roles, in the order the API returns them.
func (d *RolesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	roles, err := d.client.ListRoles(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Cannot list the roles", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("organization_roles"), roleList(roles.OrganizationRoles))...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("workspace_roles"), roleList(roles.WorkspaceRoles))...)
}

// roleList maps roles to a list of roleAttrTypes objects. Every value is
// built from its own type, so the Must constructors cannot panic.
func roleList(roles []client.Role) types.List {
	objects := make([]attr.Value, 0, len(roles))
	for _, r := range roles {
		objects = append(objects, types.ObjectValueMust(roleAttrTypes, map[string]attr.Value{
			"id":          types.StringValue(r.ID),
			"name":        types.StringValue(r.Name),
			"description": types.StringValue(r.Description),
			"is_custom":   types.BoolValue(r.IsCustomRole),
		}))
	}
	return types.ListValueMust(types.ObjectType{AttrTypes: roleAttrTypes}, objects)
}
