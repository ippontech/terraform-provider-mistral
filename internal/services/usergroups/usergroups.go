// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package usergroups holds mistral_user_group, mistral_user_group_member,
// mistral_user_group_workspace and the mistral_user_group data source: the
// user groups of the organization of the Admin API key, their members, and
// their access to workspaces
// (https://docs.mistral.ai/admin/admin-api/manage-groups-roles).
package usergroups

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
)

// noColon matches an ID that can be one part of a composite import ID, which
// joins its parts with a colon.
var noColon = regexp.MustCompile(`^[^:]+$`)

// importComposite fills the attributes parent and child of an imported
// resource, from an import ID "<parent>:<child>" or from the resource
// identity, whose attributes carry the same names.
func importComposite(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse, parent, child string) {
	var parentID, childID string
	if req.ID != "" {
		var ok bool
		parentID, childID, ok = strings.Cut(req.ID, ":")
		if !ok || parentID == "" || childID == "" || strings.Contains(childID, ":") {
			resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("The ID must be <%s>:<%s>, got %q.", parent, child, req.ID))
			return
		}
	} else {
		var p, c types.String
		resp.Diagnostics.Append(req.Identity.GetAttribute(ctx, path.Root(parent), &p)...)
		resp.Diagnostics.Append(req.Identity.GetAttribute(ctx, path.Root(child), &c)...)
		if resp.Diagnostics.HasError() {
			return
		}
		parentID, childID = p.ValueString(), c.ValueString()
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(parent), parentID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(child), childID)...)
}

// stringsOf returns the elements of a set of strings, sorted, so that a
// request body does not depend on the order Terraform hands the set in. A
// null set gives an empty slice.
func stringsOf(s types.Set) []string {
	out := make([]string, 0, len(s.Elements()))
	for _, e := range s.Elements() {
		if v, ok := e.(types.String); ok {
			out = append(out, v.ValueString())
		}
	}
	slices.Sort(out)
	return out
}

// setOf returns values as a set of strings, null when there are none: the
// schemas refuse an empty set, so that [] and null never alternate in the
// state.
func setOf(values []string) types.Set {
	if len(values) == 0 {
		return types.SetNull(types.StringType)
	}
	elems := make([]attr.Value, len(values))
	for i, v := range values {
		elems[i] = types.StringValue(v)
	}
	return types.SetValueMust(types.StringType, elems)
}

// nonEmpty returns s, null when the API gives no value or an empty one: an
// update clears the description with "", which the API may echo back.
func nonEmpty(s *string) types.String {
	if s == nil || *s == "" {
		return types.StringNull()
	}
	return types.StringValue(*s)
}

// externallyManaged reads the externally_managed flag of a group, false when
// the API leaves it out or answers null: such a group was created in
// Mistral, through the Admin Panel or the API, not by an identity provider.
func externallyManaged(g client.UserGroup) types.Bool {
	return types.BoolValue(g.ExternallyManaged != nil && *g.ExternallyManaged)
}
