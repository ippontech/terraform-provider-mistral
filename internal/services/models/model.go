// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package models holds the mistral_model and mistral_models data sources:
// the models a workspace can use, which mistral_agent and the other types
// that name a model consume.
package models

import (
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
)

// capabilityAttrTypes are the attributes of the capabilities object.
var capabilityAttrTypes = map[string]attr.Type{
	"completion_chat":     types.BoolType,
	"completion_fim":      types.BoolType,
	"function_calling":    types.BoolType,
	"reasoning":           types.BoolType,
	"fine_tuning":         types.BoolType,
	"vision":              types.BoolType,
	"ocr":                 types.BoolType,
	"classification":      types.BoolType,
	"moderation":          types.BoolType,
	"audio":               types.BoolType,
	"audio_transcription": types.BoolType,
	"audio_speech":        types.BoolType,
}

// modelAttrTypes are the attributes of one model, in mistral_models' list
// and at the top level of mistral_model.
var modelAttrTypes = map[string]attr.Type{
	"id":                            types.StringType,
	"name":                          types.StringType,
	"description":                   types.StringType,
	"type":                          types.StringType,
	"owned_by":                      types.StringType,
	"created_at":                    types.StringType,
	"max_context_length":            types.Int64Type,
	"aliases":                       types.ListType{ElemType: types.StringType},
	"deprecation":                   types.StringType,
	"deprecation_replacement_model": types.StringType,
	"default_temperature":           types.Float64Type,
	"base_model":                    types.StringType,
	"archived":                      types.BoolType,
	"capabilities":                  types.ObjectType{AttrTypes: capabilityAttrTypes},
}

// modelAttributeDescriptions documents the attributes of modelAttrTypes.
var modelAttributeDescriptions = map[string]string{
	"id":                            "The model ID.",
	"name":                          "The display name of the model.",
	"description":                   "The description of the model.",
	"type":                          "`base` for a Mistral model, `fine-tuned` for a fine-tuned model.",
	"owned_by":                      "The owner of the model: `mistralai`, or the organization of a fine-tuned model.",
	"created_at":                    "When the model was created, in RFC 3339 format.",
	"max_context_length":            "The context window of the model, in tokens.",
	"aliases":                       "The aliases that resolve to this model, such as `mistral-medium-latest`.",
	"deprecation":                   "When the model is deprecated, in RFC 3339 format; null for a model with no planned deprecation.",
	"deprecation_replacement_model": "The model Mistral recommends instead of a deprecated one.",
	"default_temperature":           "The default sampling temperature of the model.",
	"base_model":                    "The model a fine-tuned model was trained from; null for a Mistral model.",
	"archived":                      "Whether a fine-tuned model is archived; null for a Mistral model.",
	"capabilities":                  "What the model can do.",
}

// capabilityAttributes is the schema of the capabilities object.
func capabilityAttributes() map[string]schema.Attribute {
	out := make(map[string]schema.Attribute, len(capabilityAttrTypes))
	for name := range capabilityAttrTypes {
		out[name] = schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the model supports `" + name + "`."}
	}
	return out
}

// modelComputedAttributes is the schema of one model, every attribute
// computed. mistral_model replaces id with its lookup argument.
func modelComputedAttributes() map[string]schema.Attribute {
	out := map[string]schema.Attribute{}
	for name, t := range modelAttrTypes {
		desc := modelAttributeDescriptions[name]
		switch t {
		case types.StringType:
			out[name] = schema.StringAttribute{Computed: true, MarkdownDescription: desc}
		case types.Int64Type:
			out[name] = schema.Int64Attribute{Computed: true, MarkdownDescription: desc}
		case types.Float64Type:
			out[name] = schema.Float64Attribute{Computed: true, MarkdownDescription: desc}
		case types.BoolType:
			out[name] = schema.BoolAttribute{Computed: true, MarkdownDescription: desc}
		}
	}
	out["aliases"] = schema.ListAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: modelAttributeDescriptions["aliases"]}
	out["capabilities"] = schema.SingleNestedAttribute{
		Computed:            true,
		MarkdownDescription: modelAttributeDescriptions["capabilities"],
		Attributes:          capabilityAttributes(),
	}
	return out
}

// modelValues maps a model card to the attribute values of modelAttrTypes.
// Every value is built from its own type, so the Must constructors cannot
// panic on a card: a mismatch with modelAttrTypes is a programming error,
// which the tests catch.
func modelValues(m client.Model) map[string]attr.Value {
	aliases := make([]attr.Value, 0, len(m.Aliases))
	for _, a := range m.Aliases {
		aliases = append(aliases, types.StringValue(a))
	}
	c := m.Capabilities
	return map[string]attr.Value{
		"id":                            types.StringValue(m.ID),
		"name":                          types.StringPointerValue(m.Name),
		"description":                   types.StringPointerValue(m.Description),
		"type":                          stringOrNull(m.Type),
		"owned_by":                      stringOrNull(m.OwnedBy),
		"created_at":                    unixTime(m.Created),
		"max_context_length":            types.Int64Value(m.MaxContextLength),
		"aliases":                       types.ListValueMust(types.StringType, aliases),
		"deprecation":                   timeOrNull(m.Deprecation),
		"deprecation_replacement_model": types.StringPointerValue(m.DeprecationReplacementModel),
		"default_temperature":           types.Float64PointerValue(m.DefaultModelTemperature),
		"base_model":                    types.StringPointerValue(m.Root),
		"archived":                      types.BoolPointerValue(m.Archived),
		"capabilities": types.ObjectValueMust(capabilityAttrTypes, map[string]attr.Value{
			"completion_chat":     types.BoolValue(c.CompletionChat),
			"completion_fim":      types.BoolValue(c.CompletionFIM),
			"function_calling":    types.BoolValue(c.FunctionCalling),
			"reasoning":           types.BoolValue(c.Reasoning),
			"fine_tuning":         types.BoolValue(c.FineTuning),
			"vision":              types.BoolValue(c.Vision),
			"ocr":                 types.BoolValue(c.OCR),
			"classification":      types.BoolValue(c.Classification),
			"moderation":          types.BoolValue(c.Moderation),
			"audio":               types.BoolValue(c.Audio),
			"audio_transcription": types.BoolValue(c.AudioTranscription),
			"audio_speech":        types.BoolValue(c.AudioSpeech),
		}),
	}
}

// modelObject is modelValues as one object, for the list of mistral_models.
func modelObject(m client.Model) attr.Value {
	return types.ObjectValueMust(modelAttrTypes, modelValues(m))
}

func stringOrNull(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}

// unixTime renders the created field, seconds since the epoch, in RFC 3339;
// 0 means the API did not say.
func unixTime(secs int64) types.String {
	if secs == 0 {
		return types.StringNull()
	}
	return types.StringValue(time.Unix(secs, 0).UTC().Format(time.RFC3339))
}

func timeOrNull(t *time.Time) types.String {
	if t == nil {
		return types.StringNull()
	}
	return types.StringValue(t.UTC().Format(time.RFC3339))
}
