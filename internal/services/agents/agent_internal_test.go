// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestJSONState(t *testing.T) {
	t.Parallel()
	prior := types.StringValue(`{ "b": 1, "a": [true] }`)
	if got := jsonState(json.RawMessage(`{"a":[true],"b":1}`), prior); got != prior {
		t.Errorf("same value: got %s, want the prior text", got)
	}
	if got := jsonState(json.RawMessage(`{"a": [false]}`), prior); got.ValueString() != `{"a":[false]}` {
		t.Errorf("other value: got %s, want the compacted API text", got)
	}
	if got := jsonState(json.RawMessage(`{"a": 1}`), types.StringNull()); got.ValueString() != `{"a":1}` {
		t.Errorf("no prior: got %s", got)
	}
	if got := jsonState(json.RawMessage(`not json`), types.StringValue(`{}`)); got.ValueString() != "not json" {
		t.Errorf("invalid JSON is kept as returned: got %s", got)
	}
}

func TestMetadataState(t *testing.T) {
	t.Parallel()
	got := metadataState(map[string]json.RawMessage{"s": json.RawMessage(`"x"`), "n": json.RawMessage(`2`), "o": json.RawMessage(`{"k":true}`)})
	if got["s"] != "x" || got["n"] != "2" || got["o"] != `{"k":true}` {
		t.Errorf("metadataState = %v", got)
	}
	if metadataState(map[string]json.RawMessage{}) != nil {
		t.Error("empty metadata must be null")
	}
}

func TestSmallHelpers(t *testing.T) {
	t.Parallel()
	if !timestamp(time.Time{}).IsNull() {
		t.Error("a zero time must be null")
	}
	if !nonEmpty(new("")).IsNull() || nonEmpty(new("a")).ValueString() != "a" {
		t.Error("nonEmpty")
	}
	if _, known := knownString(types.Int64Value(1)); known {
		t.Error("knownString of a non-string")
	}
	if _, known := isSet(nil); known {
		t.Error("isSet(nil) must be unknown")
	}
}

func TestJSONObjectValidator(t *testing.T) {
	t.Parallel()
	v := jsonObject{}
	ctx := context.Background()
	if v.MarkdownDescription(ctx) != v.Description(ctx) {
		t.Error("descriptions differ")
	}
	for value, wantErr := range map[string]bool{`{"a":1}`: false, `[1]`: true, `null`: true, `{`: true} {
		resp := &validator.StringResponse{}
		v.ValidateString(ctx, validator.StringRequest{Path: path.Root("p"), ConfigValue: types.StringValue(value)}, resp)
		if resp.Diagnostics.HasError() != wantErr {
			t.Errorf("%s: error = %v, want %v", value, resp.Diagnostics, wantErr)
		}
	}
	resp := &validator.StringResponse{}
	v.ValidateString(ctx, validator.StringRequest{ConfigValue: types.StringUnknown()}, resp)
	if resp.Diagnostics.HasError() {
		t.Error("an unknown value is checked at apply time")
	}
}

// An agent card without completion_args reads as the default arguments.
func TestCompletionArgsStateOfAnAbsentObject(t *testing.T) {
	t.Parallel()
	got := completionArgsState(nil, nil)
	if got.ToolChoice.ValueString() != defaultToolChoice || !got.Temperature.IsNull() || got.ResponseFormat != nil {
		t.Errorf("completionArgsState(nil) = %+v", got)
	}
}

// Values unknown at plan time are checked at apply time, not refused.
func TestValidateToolSkipsUnknownValues(t *testing.T) {
	t.Parallel()
	var diags diag.Diagnostics
	validateTool(map[string]attr.Value{"type": types.StringUnknown()}, path.Root("tools"), &diags)
	validateTool(map[string]attr.Value{
		"type":         types.StringValue(toolDocumentLibrary),
		"library_ids":  types.ListUnknown(types.StringType),
		"function":     types.ObjectNull(functionAttrTypes),
		"connector_id": types.StringNull(),
	}, path.Root("tools"), &diags)
	if diags.HasError() {
		t.Errorf("diagnostics = %v", diags)
	}
}
