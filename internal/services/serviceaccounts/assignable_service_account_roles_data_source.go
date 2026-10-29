// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package serviceaccounts

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

var _ datasource.DataSourceWithConfigure = &AssignableServiceAccountRolesDataSource{}

// NewAssignableServiceAccountRolesDataSource returns the
// mistral_assignable_service_account_roles data source.
func NewAssignableServiceAccountRolesDataSource() datasource.DataSource {
	return &AssignableServiceAccountRolesDataSource{}
}

// AssignableServiceAccountRolesDataSource lists the workspace roles a service
// account can hold, whose IDs the role_ids of mistral_service_account take:
// GET /v1/service-accounts/assignable-roles, named "List assignable service
// account roles" in the API reference
// (https://docs.mistral.ai/api/endpoint/beta/service_accounts). They are not
// the organization roles of the Admin API (GET /v1/admin/roles).
type AssignableServiceAccountRolesDataSource struct {
	client *client.Client
}

// assignableRolesModel is the state of the data source.
type assignableRolesModel struct {
	WorkspaceID types.String          `tfsdk:"workspace_id"`
	Roles       []assignableRoleModel `tfsdk:"roles"`
}

// assignableRoleModel is one role. uuid is renamed id, as every reference to
// it is a role ID, and is_custom_role is renamed custom.
type assignableRoleModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	Custom      types.Bool   `tfsdk:"custom"`
}

// Metadata names the data source mistral_assignable_service_account_roles.
func (d *AssignableServiceAccountRolesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_assignable_service_account_roles"
}

// Schema declares the workspace to list and the roles it answers.
func (d *AssignableServiceAccountRolesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	computed := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{Computed: true, MarkdownDescription: desc}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the workspace roles that a service account of the workspace can hold, in the order the API returns them.",
		Attributes: map[string]schema.Attribute{
			"workspace_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the workspace: the workspace of the provider's API key.",
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"roles": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "The roles.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":          computed("The role ID, for the `role_ids` of `mistral_service_account`."),
						"name":        computed("The name of the role."),
						"description": computed("What the role grants."),
						"custom": schema.BoolAttribute{
							Computed:            true,
							MarkdownDescription: "Whether the role is a custom role rather than one of the predefined roles of Mistral.",
						},
					},
				},
			},
		},
	}
}

// Configure takes the client, and fails without the standard key.
func (d *AssignableServiceAccountRolesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = providerdata.Configure(req.ProviderData, providerdata.Workspace, &resp.Diagnostics)
}

// Read lists the roles of the workspace. The list is not paginated.
func (d *AssignableServiceAccountRolesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var workspaceID types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("workspace_id"), &workspaceID)...)
	if resp.Diagnostics.HasError() {
		return
	}
	roles, err := d.client.ListAssignableServiceAccountRoles(ctx, workspaceID.ValueString())
	if client.IsNotFound(err) {
		resp.Diagnostics.AddAttributeError(path.Root("workspace_id"), "Workspace not found",
			fmt.Sprintf("No workspace %q is visible to the API key: %v", workspaceID.ValueString(), err))
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot list the assignable service account roles", err.Error())
		return
	}
	// Not nil: no role is an empty list, not a null one.
	out := make([]assignableRoleModel, 0, len(roles))
	for _, r := range roles {
		out = append(out, assignableRoleModel{
			ID:          types.StringValue(r.UUID),
			Name:        types.StringValue(r.Name),
			Description: types.StringValue(r.Description),
			Custom:      types.BoolValue(r.IsCustomRole),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, assignableRolesModel{WorkspaceID: workspaceID, Roles: out})...)
}
