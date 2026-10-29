// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package users

import (
	"context"
	"fmt"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
	"github.com/ippontech/terraform-provider-mistral/internal/providerdata"
)

var _ datasource.DataSourceWithConfigure = &UserDataSource{}

// NewUserDataSource returns the mistral_user data source.
func NewUserDataSource() datasource.DataSource {
	return &UserDataSource{}
}

// UserDataSource reads a member of the organization by email, the key users
// know: GET /v1/admin/users?email= finds the ID, then GET
// /v1/admin/users/{user_id} reads the member, the one route that returns
// the first and last name.
type UserDataSource struct {
	client *client.Client
}

// Metadata names the data source mistral_user.
func (d *UserDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user"
}

// Schema is the member of userComputedAttributes, looked up by email, with
// the first and last name of the read by ID.
func (d *UserDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attributes := userComputedAttributes()
	attributes["email"] = schema.StringAttribute{
		Required:            true,
		MarkdownDescription: "The email address of the member. The lookup ignores case.",
		Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
	}
	attributes["first_name"] = schema.StringAttribute{Computed: true, MarkdownDescription: "The first name of the member."}
	attributes["last_name"] = schema.StringAttribute{Computed: true, MarkdownDescription: "The last name of the member."}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads a member of the organization by email address, for the user ID that workspace and user group memberships take.",
		Attributes:          attributes,
	}
}

// Configure takes the client, with the Admin API key.
func (d *UserDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = providerdata.Configure(req.ProviderData, providerdata.Admin, &resp.Diagnostics)
}

// Read finds the member by email, then reads it by ID.
func (d *UserDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var email types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("email"), &email)...)
	if resp.Diagnostics.HasError() {
		return
	}

	dir, err := d.client.ListUsers(ctx, email.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Cannot read the user", err.Error())
		return
	}
	found := findUser(dir, email.ValueString())
	if found == nil {
		resp.Diagnostics.Append(userNotFound(email.ValueString(), dir)...)
		return
	}
	// The member can leave between the two calls: the read by ID then
	// answers 404, which is the same answer as no member at all.
	u, err := d.client.GetUser(ctx, found.ID)
	if client.IsNotFound(err) {
		resp.Diagnostics.Append(userNotFound(email.ValueString(), &client.UserDirectory{})...)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot read the user", err.Error())
		return
	}
	resp.Diagnostics.Append(setUserState(ctx, &resp.State, email, *u)...)
}

// userNotFound is the error of an address with no member. A pending invite
// for that address gets a hint: the person is not a member until they
// accept it.
func userNotFound(email string, dir *client.UserDirectory) (diags diag.Diagnostics) {
	detail := fmt.Sprintf("No member of the organization has the email %q.", email)
	if slices.ContainsFunc(dir.Invites, func(inv client.Invite) bool { return sameEmail(inv.Email, email) }) {
		detail += " An invite for that address is pending: the person becomes a member once they accept it."
	}
	diags.AddAttributeError(path.Root("email"), "User not found", detail)
	return diags
}

// setUserState writes the member to the state, keeping email as configured:
// the lookup ignores case, and the state must hold the configured value of
// a required argument.
func setUserState(ctx context.Context, state *tfsdk.State, email types.String, u client.User) (diags diag.Diagnostics) {
	for name, v := range userValues(u) {
		if name == "email" {
			continue
		}
		diags.Append(state.SetAttribute(ctx, path.Root(name), v)...)
	}
	diags.Append(state.SetAttribute(ctx, path.Root("email"), email)...)
	diags.Append(state.SetAttribute(ctx, path.Root("first_name"), types.StringPointerValue(u.FirstName))...)
	diags.Append(state.SetAttribute(ctx, path.Root("last_name"), types.StringPointerValue(u.LastName))...)
	return diags
}
