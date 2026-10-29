// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package users holds mistral_invite and the mistral_user and mistral_users
// data sources: the invitations and the members of the organization, through
// the Admin API (https://docs.mistral.ai/admin/admin-api/manage-users). The
// data sources give the user IDs that workspace and user group memberships
// take.
package users

import (
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
)

// rawRolesNote says what the roles of a member hold: the API returns them
// as role UUIDs or as legacy letters, never as the role names its requests
// take (MemberWorkspaceInfo and AdminOrganizationMemberOUT in the
// specification).
const rawRolesNote = "as the API returns them: role UUIDs, which the `mistral_roles` data source names, or legacy letters"

// workspaceAttrTypes are the attributes of one workspace of a member.
var workspaceAttrTypes = map[string]attr.Type{
	"workspace_id": types.StringType,
	"name":         types.StringType,
	"is_default":   types.BoolType,
	"roles":        types.ListType{ElemType: types.StringType},
}

// userAttrTypes are the attributes of one member, in the list of
// mistral_users and at the top level of mistral_user.
var userAttrTypes = map[string]attr.Type{
	"id":                 types.StringType,
	"email":              types.StringType,
	"name":               types.StringType,
	"organization_roles": types.ListType{ElemType: types.StringType},
	"workspaces":         types.ListType{ElemType: types.ObjectType{AttrTypes: workspaceAttrTypes}},
}

// userComputedAttributes is the schema of one member, every attribute
// computed. mistral_user turns email into its lookup argument.
func userComputedAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"id":    schema.StringAttribute{Computed: true, MarkdownDescription: "The user ID, which workspace and user group memberships take."},
		"email": schema.StringAttribute{Computed: true, MarkdownDescription: "The email address of the member."},
		"name":  schema.StringAttribute{Computed: true, MarkdownDescription: "The name of the member."},
		"organization_roles": schema.ListAttribute{
			Computed:    true,
			ElementType: types.StringType,
			MarkdownDescription: "The organization roles of the member, " + rawRolesNote +
				": `A` for admin, `M` for member, and `B`, undocumented, for the billing manager by our reading. Null when the API does not return them.",
		},
		"workspaces": schema.ListNestedAttribute{
			Computed:            true,
			MarkdownDescription: "The workspaces the member belongs to. Null when the API does not return them.",
			NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
				"workspace_id": schema.StringAttribute{Computed: true, MarkdownDescription: "The workspace ID."},
				"name":         schema.StringAttribute{Computed: true, MarkdownDescription: "The name of the workspace."},
				"is_default":   schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether this is the default workspace of the organization."},
				"roles": schema.ListAttribute{
					Computed:            true,
					ElementType:         types.StringType,
					MarkdownDescription: "The roles of the member in the workspace, " + rawRolesNote + ": `A` for admin, `M` for member.",
				},
			}},
		},
	}
}

// userValues maps a member to the attribute values of userAttrTypes. Every
// value is built from its own type, so the Must constructors cannot panic on
// a member: a mismatch with userAttrTypes is a programming error, which the
// tests catch.
func userValues(u client.User) map[string]attr.Value {
	workspaces := types.ListNull(types.ObjectType{AttrTypes: workspaceAttrTypes})
	if u.Workspaces != nil {
		objects := make([]attr.Value, 0, len(u.Workspaces))
		for _, w := range u.Workspaces {
			objects = append(objects, types.ObjectValueMust(workspaceAttrTypes, map[string]attr.Value{
				"workspace_id": types.StringValue(w.ID),
				"name":         types.StringValue(w.Name),
				"is_default":   types.BoolValue(w.IsDefault),
				"roles":        stringList(w.RawRoles),
			}))
		}
		workspaces = types.ListValueMust(types.ObjectType{AttrTypes: workspaceAttrTypes}, objects)
	}
	return map[string]attr.Value{
		"id":                 types.StringValue(u.ID),
		"email":              types.StringPointerValue(u.Email),
		"name":               types.StringPointerValue(u.Name),
		"organization_roles": stringList(u.RawRoles),
		"workspaces":         workspaces,
	}
}

// stringList is a list of strings, null when the API returned none: an
// absent field and an empty one differ for a reader of the data source.
func stringList(s []string) types.List {
	if s == nil {
		return types.ListNull(types.StringType)
	}
	values := make([]attr.Value, 0, len(s))
	for _, v := range s {
		values = append(values, types.StringValue(v))
	}
	return types.ListValueMust(types.StringType, values)
}

// sameEmail compares two addresses as Mistral's lookups are likely to:
// without regard to case, since providers treat the local part that way in
// practice. The specification says nothing either way.
func sameEmail(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

// findUser returns the member of dir with the address email, or nil.
func findUser(dir *client.UserDirectory, email string) *client.User {
	for i := range dir.Users {
		if u := &dir.Users[i]; u.Email != nil && sameEmail(*u.Email, email) {
			return u
		}
	}
	return nil
}

// timestamp renders a date of the API in RFC 3339; the zero time means the
// API gave none.
func timestamp(t time.Time) types.String {
	if t.IsZero() {
		return types.StringNull()
	}
	return types.StringValue(t.UTC().Format(time.RFC3339))
}
