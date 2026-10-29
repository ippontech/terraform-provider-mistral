// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package promptsandskills

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
)

// definition is the versioned content of the plan. An absent description or
// body is sent as "", the value a version without it holds.
func (m *skillModel) definition() client.SkillDefinition {
	d := client.SkillDefinition{Description: m.Description.ValueString(), Body: m.Body.ValueString()}
	if len(m.Assets) > 0 {
		d.Assets = make(map[string]client.SkillAsset, len(m.Assets))
		for name, a := range m.Assets {
			d.Assets[name] = client.SkillAsset{
				TextContent:  a.Content.ValueStringPointer(),
				RawContent:   a.ContentBase64.ValueStringPointer(),
				IsExecutable: a.Executable.ValueBool(),
			}
		}
	}
	return d
}

// skillState maps a skill to the state. prior is the plan or state the call
// started from: it keeps workspace_relation, which the API does not return,
// and the text of each asset the API returns with the same content.
func skillState(s client.Skill, prior *skillModel) skillModel {
	m := skillModel{
		ID:           types.StringValue(s.ID),
		Name:         types.StringValue(s.Name),
		Description:  nonEmpty(s.Definition.Description),
		Body:         nonEmpty(s.Definition.Body),
		Notes:        nonEmpty(s.Notes),
		SharingScope: scopeState(s.SharingScope),
		Version:      types.Int64Value(s.Version),
		CreatedAt:    timestamp(s.CreatedAt),
		UpdatedAt:    timestamp(s.UpdatedAt),
	}
	m.WorkspaceRelation = relationState(m.SharingScope, prior.WorkspaceRelation)
	if len(s.Definition.Assets) > 0 {
		m.Assets = make(map[string]assetModel, len(s.Definition.Assets))
		for name, a := range s.Definition.Assets {
			var p *assetModel
			if pa, ok := prior.Assets[name]; ok {
				p = &pa
			}
			m.Assets[name] = assetState(a, p)
		}
	}
	return m
}

// assetState maps an asset to the state. When the content the API returns
// equals the prior one, the prior attribute and text are kept: the API may
// answer a text file as bytes or the reverse, or in another base64
// alphabet, neither of which the specification rules out. Otherwise the
// asset reads as the API returns it; one with neither field set reads as an
// empty text file, the protobuf zero value.
func assetState(a client.SkillAsset, prior *assetModel) assetModel {
	out := assetModel{Content: types.StringNull(), ContentBase64: types.StringNull(), Executable: types.BoolValue(a.IsExecutable)}
	if content, ok := assetContent(a); ok && prior != nil {
		if !prior.Content.IsNull() && prior.Content.ValueString() == string(content) {
			out.Content = prior.Content
			return out
		}
		if b, err := decodeBase64(prior.ContentBase64.ValueString()); !prior.ContentBase64.IsNull() && err == nil && bytes.Equal(b, content) {
			out.ContentBase64 = prior.ContentBase64
			return out
		}
	}
	switch {
	case a.RawContent != nil:
		out.ContentBase64 = types.StringValue(*a.RawContent)
	case a.TextContent != nil:
		out.Content = types.StringValue(*a.TextContent)
	default:
		out.Content = types.StringValue("")
	}
	return out
}

// assetContent returns the bytes of an asset, and false when its rawContent
// is not base64.
func assetContent(a client.SkillAsset) ([]byte, bool) {
	switch {
	case a.RawContent != nil:
		b, err := decodeBase64(*a.RawContent)
		return b, err == nil
	case a.TextContent != nil:
		return []byte(*a.TextContent), true
	}
	return []byte{}, true
}

// base64Encodings are the alphabets a protobuf API accepts for bytes:
// standard or URL-safe, padded or not.
var base64Encodings = []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding}

// errNotBase64 is the error of a string no base64 alphabet decodes.
var errNotBase64 = errors.New("not base64 in the standard or URL-safe alphabet")

// decodeBase64 decodes s in the first alphabet that accepts it.
func decodeBase64(s string) ([]byte, error) {
	for _, enc := range base64Encodings {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, errNotBase64
}

// validateAssets requires each asset to set exactly one of content and
// content_base64, the two branches of SkillAssetContent. A value unknown at
// plan time is checked at apply time.
func validateAssets(ctx context.Context, config tfsdk.Config, diags *diag.Diagnostics) {
	var assets types.Map
	diags.Append(config.GetAttribute(ctx, path.Root("assets"), &assets)...)
	if assets.IsNull() || assets.IsUnknown() {
		return
	}
	for name, v := range assets.Elements() {
		obj, ok := v.(types.Object)
		if !ok || obj.IsNull() || obj.IsUnknown() {
			continue
		}
		text, raw := obj.Attributes()["content"], obj.Attributes()["content_base64"]
		if text.IsUnknown() || raw.IsUnknown() || text.IsNull() != raw.IsNull() {
			continue
		}
		diags.AddAttributeError(path.Root("assets").AtMapKey(name), "Invalid asset",
			fmt.Sprintf("The asset %q must set exactly one of content, for a text file, and content_base64, for a binary file.", name))
	}
}

// base64String validates that a string decodes as base64.
type base64String struct{}

// Description says what the validator accepts.
func (base64String) Description(context.Context) string {
	return "value must be base64-encoded, such as the result of filebase64"
}

// MarkdownDescription is Description.
func (v base64String) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }

// ValidateString refuses a known value that does not decode.
func (v base64String) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if _, err := decodeBase64(req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid base64", v.Description(ctx)+": "+err.Error())
	}
}
