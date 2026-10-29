// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents

import (
	"bytes"
	"encoding/json"
	"reflect"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
)

// Server-side defaults the API fills in when a request leaves a field out
// (CreateAgentRequest in the specification). The schema declares them as
// Defaults, and the state mapping writes them for an absent field, so an
// echoed default never shows as drift.
const (
	defaultToolChoice     = "auto"
	defaultFunctionDesc   = ""
	defaultFunctionStrict = false
	defaultSchemaStrict   = false
)

// agentModel is the state of mistral_agent.
type agentModel struct {
	ID             types.String         `tfsdk:"id"`
	Name           types.String         `tfsdk:"name"`
	Model          types.String         `tfsdk:"model"`
	Description    types.String         `tfsdk:"description"`
	Instructions   types.String         `tfsdk:"instructions"`
	Tools          []toolModel          `tfsdk:"tools"`
	CompletionArgs *completionArgsModel `tfsdk:"completion_args"`
	Handoffs       []string             `tfsdk:"handoffs"`
	Metadata       map[string]string    `tfsdk:"metadata"`
	VersionMessage types.String         `tfsdk:"version_message"`
	Version        types.Int64          `tfsdk:"version"`
	CreatedAt      types.String         `tfsdk:"created_at"`
	UpdatedAt      types.String         `tfsdk:"updated_at"`
}

type toolModel struct {
	Type              types.String            `tfsdk:"type"`
	Function          *functionModel          `tfsdk:"function"`
	LibraryIDs        []string                `tfsdk:"library_ids"`
	ConnectorID       types.String            `tfsdk:"connector_id"`
	ToolConfiguration *toolConfigurationModel `tfsdk:"tool_configuration"`
}

type functionModel struct {
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	Strict      types.Bool   `tfsdk:"strict"`
	Parameters  types.String `tfsdk:"parameters"`
}

type toolConfigurationModel struct {
	Include              []string `tfsdk:"include"`
	Exclude              []string `tfsdk:"exclude"`
	RequiresConfirmation []string `tfsdk:"requires_confirmation"`
}

type completionArgsModel struct {
	Stop             []string             `tfsdk:"stop"`
	PresencePenalty  types.Float64        `tfsdk:"presence_penalty"`
	FrequencyPenalty types.Float64        `tfsdk:"frequency_penalty"`
	Temperature      types.Float64        `tfsdk:"temperature"`
	TopP             types.Float64        `tfsdk:"top_p"`
	MaxTokens        types.Int64          `tfsdk:"max_tokens"`
	RandomSeed       types.Int64          `tfsdk:"random_seed"`
	ResponseFormat   *responseFormatModel `tfsdk:"response_format"`
	ToolChoice       types.String         `tfsdk:"tool_choice"`
	ReasoningEffort  types.String         `tfsdk:"reasoning_effort"`
}

type responseFormatModel struct {
	Type       types.String     `tfsdk:"type"`
	JSONSchema *jsonSchemaModel `tfsdk:"json_schema"`
}

type jsonSchemaModel struct {
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	Schema      types.String `tfsdk:"schema"`
	Strict      types.Bool   `tfsdk:"strict"`
}

var (
	functionAttrTypes = map[string]attr.Type{
		"name":        types.StringType,
		"description": types.StringType,
		"strict":      types.BoolType,
		"parameters":  types.StringType,
	}
	toolConfigurationAttrTypes = map[string]attr.Type{
		"include":               types.ListType{ElemType: types.StringType},
		"exclude":               types.ListType{ElemType: types.StringType},
		"requires_confirmation": types.ListType{ElemType: types.StringType},
	}
	toolAttrTypes = map[string]attr.Type{
		"type":               types.StringType,
		"function":           types.ObjectType{AttrTypes: functionAttrTypes},
		"library_ids":        types.ListType{ElemType: types.StringType},
		"connector_id":       types.StringType,
		"tool_configuration": types.ObjectType{AttrTypes: toolConfigurationAttrTypes},
	}
	jsonSchemaAttrTypes = map[string]attr.Type{
		"name":        types.StringType,
		"description": types.StringType,
		"schema":      types.StringType,
		"strict":      types.BoolType,
	}
	responseFormatAttrTypes = map[string]attr.Type{
		"type":        types.StringType,
		"json_schema": types.ObjectType{AttrTypes: jsonSchemaAttrTypes},
	}
	completionArgsAttrTypes = map[string]attr.Type{
		"stop":              types.ListType{ElemType: types.StringType},
		"presence_penalty":  types.Float64Type,
		"frequency_penalty": types.Float64Type,
		"temperature":       types.Float64Type,
		"top_p":             types.Float64Type,
		"max_tokens":        types.Int64Type,
		"random_seed":       types.Int64Type,
		"response_format":   types.ObjectType{AttrTypes: responseFormatAttrTypes},
		"tool_choice":       types.StringType,
		"reasoning_effort":  types.StringType,
	}
)

// defaultCompletionArgs is completion_args when the configuration leaves it
// out: what the API answers for an agent created without it.
func defaultCompletionArgs() types.Object {
	return types.ObjectValueMust(completionArgsAttrTypes, map[string]attr.Value{
		"stop":              types.ListNull(types.StringType),
		"presence_penalty":  types.Float64Null(),
		"frequency_penalty": types.Float64Null(),
		"temperature":       types.Float64Null(),
		"top_p":             types.Float64Null(),
		"max_tokens":        types.Int64Null(),
		"random_seed":       types.Int64Null(),
		"response_format":   types.ObjectNull(responseFormatAttrTypes),
		"tool_choice":       types.StringValue(defaultToolChoice),
		"reasoning_effort":  types.StringNull(),
	})
}

// --- Terraform to API ---

// request builds the body of a create or an update from the plan. Every
// field is sent, null included: an update replaces the settings.
func (m *agentModel) request() client.AgentRequest {
	req := client.AgentRequest{
		Name:           m.Name.ValueString(),
		Model:          m.Model.ValueString(),
		Description:    m.Description.ValueStringPointer(),
		Instructions:   m.Instructions.ValueStringPointer(),
		Tools:          make([]client.AgentTool, 0, len(m.Tools)),
		Handoffs:       m.Handoffs,
		Metadata:       m.Metadata,
		VersionMessage: m.VersionMessage.ValueStringPointer(),
	}
	for _, t := range m.Tools {
		req.Tools = append(req.Tools, t.request())
	}
	if m.CompletionArgs != nil {
		req.CompletionArgs = m.CompletionArgs.request()
	}
	return req
}

func (t *toolModel) request() client.AgentTool {
	out := client.AgentTool{
		Type:        t.Type.ValueString(),
		LibraryIDs:  t.LibraryIDs,
		ConnectorID: t.ConnectorID.ValueStringPointer(),
	}
	if f := t.Function; f != nil {
		out.Function = &client.FunctionTool{
			Name:        f.Name.ValueString(),
			Description: f.Description.ValueStringPointer(),
			Strict:      f.Strict.ValueBoolPointer(),
			Parameters:  json.RawMessage(f.Parameters.ValueString()),
		}
	}
	if c := t.ToolConfiguration; c != nil {
		out.ToolConfiguration = &client.ToolConfiguration{
			Include:              c.Include,
			Exclude:              c.Exclude,
			RequiresConfirmation: c.RequiresConfirmation,
		}
	}
	return out
}

func (c *completionArgsModel) request() client.CompletionArgs {
	out := client.CompletionArgs{
		Stop:             c.Stop,
		PresencePenalty:  c.PresencePenalty.ValueFloat64Pointer(),
		FrequencyPenalty: c.FrequencyPenalty.ValueFloat64Pointer(),
		Temperature:      c.Temperature.ValueFloat64Pointer(),
		TopP:             c.TopP.ValueFloat64Pointer(),
		MaxTokens:        c.MaxTokens.ValueInt64Pointer(),
		RandomSeed:       c.RandomSeed.ValueInt64Pointer(),
		ToolChoice:       c.ToolChoice.ValueStringPointer(),
		ReasoningEffort:  c.ReasoningEffort.ValueStringPointer(),
	}
	if rf := c.ResponseFormat; rf != nil {
		out.ResponseFormat = &client.ResponseFormat{Type: rf.Type.ValueString()}
		if s := rf.JSONSchema; s != nil {
			out.ResponseFormat.JSONSchema = &client.JSONSchema{
				Name:        s.Name.ValueString(),
				Description: s.Description.ValueStringPointer(),
				Schema:      json.RawMessage(s.Schema.ValueString()),
				Strict:      s.Strict.ValueBoolPointer(),
			}
		}
	}
	return out
}

// --- API to Terraform ---

// agentState maps an agent to the state. prior is the state or plan the
// call started from, with only id set after an import: a JSON document the
// API returns reformatted keeps the prior text when it holds the same value.
func agentState(a client.Agent, prior *agentModel) agentModel {
	m := agentModel{
		ID:             types.StringValue(a.ID),
		Name:           types.StringValue(a.Name),
		Model:          types.StringValue(a.Model),
		Description:    nonEmpty(a.Description),
		Instructions:   nonEmpty(a.Instructions),
		Tools:          make([]toolModel, 0, len(a.Tools)),
		CompletionArgs: completionArgsState(a.CompletionArgs, prior.CompletionArgs),
		Handoffs:       nilIfEmpty(a.Handoffs),
		Metadata:       metadataState(a.Metadata),
		VersionMessage: nonEmpty(a.VersionMessage),
		Version:        types.Int64Value(a.Version),
		CreatedAt:      timestamp(a.CreatedAt),
		UpdatedAt:      timestamp(a.UpdatedAt),
	}
	for i, t := range a.Tools {
		var p *toolModel
		if i < len(prior.Tools) {
			p = &prior.Tools[i]
		}
		m.Tools = append(m.Tools, toolState(t, p))
	}
	return m
}

func toolState(t client.AgentTool, prior *toolModel) toolModel {
	out := toolModel{
		Type:        types.StringValue(t.Type),
		LibraryIDs:  nilIfEmpty(t.LibraryIDs),
		ConnectorID: types.StringPointerValue(t.ConnectorID),
	}
	if f := t.Function; f != nil {
		var priorParams types.String
		if prior != nil && prior.Function != nil {
			priorParams = prior.Function.Parameters
		}
		out.Function = &functionModel{
			Name:        types.StringValue(f.Name),
			Description: types.StringValue(valueOr(f.Description, defaultFunctionDesc)),
			Strict:      types.BoolValue(valueOr(f.Strict, defaultFunctionStrict)),
			Parameters:  jsonState(f.Parameters, priorParams),
		}
	}
	if c := t.ToolConfiguration; c != nil && len(c.Include)+len(c.Exclude)+len(c.RequiresConfirmation) > 0 {
		out.ToolConfiguration = &toolConfigurationModel{
			Include:              nilIfEmpty(c.Include),
			Exclude:              nilIfEmpty(c.Exclude),
			RequiresConfirmation: nilIfEmpty(c.RequiresConfirmation),
		}
	}
	return out
}

func completionArgsState(c *client.CompletionArgs, prior *completionArgsModel) *completionArgsModel {
	if c == nil {
		c = &client.CompletionArgs{}
	}
	out := &completionArgsModel{
		Stop:             nilIfEmpty(c.Stop),
		PresencePenalty:  types.Float64PointerValue(c.PresencePenalty),
		FrequencyPenalty: types.Float64PointerValue(c.FrequencyPenalty),
		Temperature:      types.Float64PointerValue(c.Temperature),
		TopP:             types.Float64PointerValue(c.TopP),
		MaxTokens:        types.Int64PointerValue(c.MaxTokens),
		RandomSeed:       types.Int64PointerValue(c.RandomSeed),
		ToolChoice:       types.StringValue(valueOr(c.ToolChoice, defaultToolChoice)),
		ReasoningEffort:  types.StringPointerValue(c.ReasoningEffort),
	}
	if rf := c.ResponseFormat; rf != nil {
		out.ResponseFormat = &responseFormatModel{Type: types.StringValue(rf.Type)}
		if s := rf.JSONSchema; s != nil {
			var priorSchema types.String
			if prior != nil && prior.ResponseFormat != nil && prior.ResponseFormat.JSONSchema != nil {
				priorSchema = prior.ResponseFormat.JSONSchema.Schema
			}
			out.ResponseFormat.JSONSchema = &jsonSchemaModel{
				Name:        types.StringValue(s.Name),
				Description: nonEmpty(s.Description),
				Schema:      jsonState(s.Schema, priorSchema),
				Strict:      types.BoolValue(valueOr(s.Strict, defaultSchemaStrict)),
			}
		}
	}
	return out
}

// metadataState writes each value as a string: the provider sends strings,
// and a value set outside Terraform as another JSON type reads as its JSON
// text.
func metadataState(md map[string]json.RawMessage) map[string]string {
	if len(md) == 0 {
		return nil
	}
	out := make(map[string]string, len(md))
	for k, raw := range md {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			out[k] = s
			continue
		}
		out[k] = string(raw)
	}
	return out
}

// jsonState keeps prior when it encodes the same JSON value as got, so a
// document the API reformats does not show as drift; otherwise it returns
// got, compacted.
func jsonState(got json.RawMessage, prior types.String) types.String {
	if !prior.IsNull() && !prior.IsUnknown() && sameJSON(got, []byte(prior.ValueString())) {
		return prior
	}
	var b bytes.Buffer
	if err := json.Compact(&b, got); err != nil {
		return types.StringValue(string(got))
	}
	return types.StringValue(b.String())
}

func sameJSON(a, b []byte) bool {
	var va, vb any
	if json.Unmarshal(a, &va) != nil || json.Unmarshal(b, &vb) != nil {
		return false
	}
	return reflect.DeepEqual(va, vb)
}

func nonEmpty(s *string) types.String {
	if s == nil || *s == "" {
		return types.StringNull()
	}
	return types.StringValue(*s)
}

func nilIfEmpty(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return s
}

func valueOr[T any](p *T, def T) T {
	if p == nil {
		return def
	}
	return *p
}

func timestamp(t time.Time) types.String {
	if t.IsZero() {
		return types.StringNull()
	}
	return types.StringValue(t.UTC().Format(time.RFC3339))
}
