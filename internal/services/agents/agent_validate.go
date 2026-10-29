// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// toolFields maps each tool type to the one union field it requires; every
// other type takes none of them.
var toolFields = map[string]string{
	toolFunction:        "function",
	toolDocumentLibrary: "library_ids",
	toolConnector:       "connector_id",
}

// ValidateConfig checks the unions of the schema: the field each tool type
// requires and refuses, and json_schema for the json_schema response format.
// Values unknown at plan time are checked at apply time.
func (r *AgentResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var tools types.List
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("tools"), &tools)...)
	if !tools.IsNull() && !tools.IsUnknown() {
		for i, e := range tools.Elements() {
			if obj, ok := e.(types.Object); ok && !obj.IsNull() && !obj.IsUnknown() {
				validateTool(obj.Attributes(), path.Root("tools").AtListIndex(i), &resp.Diagnostics)
			}
		}
	}

	var format types.Object
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("completion_args").AtName("response_format"), &format)...)
	if format.IsNull() || format.IsUnknown() {
		return
	}
	attrs := format.Attributes()
	kind, known := knownString(attrs["type"])
	if !known {
		return
	}
	at := path.Root("completion_args").AtName("response_format").AtName("json_schema")
	switch set, known := isSet(attrs["json_schema"]); {
	case !known:
	case kind == "json_schema" && !set:
		resp.Diagnostics.AddAttributeError(at, "Missing json_schema", "A json_schema response format needs json_schema.")
	case kind != "json_schema" && set:
		resp.Diagnostics.AddAttributeError(at, "Unexpected json_schema", fmt.Sprintf("A %s response format takes no json_schema.", kind))
	}
}

func validateTool(attrs map[string]attr.Value, at path.Path, diags *diag.Diagnostics) {
	kind, known := knownString(attrs["type"])
	if !known {
		return
	}
	for t, field := range toolFields {
		set, known := isSet(attrs[field])
		switch {
		case !known:
		case kind == t && !set:
			diags.AddAttributeError(at.AtName(field), "Missing "+field, fmt.Sprintf("A %s tool needs %s.", kind, field))
		case kind != t && set:
			diags.AddAttributeError(at.AtName(field), "Unexpected "+field, fmt.Sprintf("A %s tool takes no %s: it belongs to %s tools.", kind, field, t))
		}
	}
}

func knownString(v attr.Value) (string, bool) {
	s, ok := v.(types.String)
	if !ok || s.IsNull() || s.IsUnknown() {
		return "", false
	}
	return s.ValueString(), true
}

// isSet reports whether v is set, and whether that is known at plan time.
func isSet(v attr.Value) (set, known bool) {
	if v == nil || v.IsUnknown() {
		return false, false
	}
	return !v.IsNull(), true
}
