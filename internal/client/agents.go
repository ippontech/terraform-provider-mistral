// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Agent is an agent at its active version (Agent in the specification,
// https://docs.mistral.ai/api/endpoint/beta/agents).
type Agent struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Model          string          `json:"model"`
	Description    *string         `json:"description"`
	Instructions   *string         `json:"instructions"`
	Tools          []AgentTool     `json:"tools"`
	CompletionArgs *CompletionArgs `json:"completion_args"`
	Handoffs       []string        `json:"handoffs"`
	// Metadata values are any JSON value; the provider writes strings only.
	Metadata       map[string]json.RawMessage `json:"metadata"`
	Version        int64                      `json:"version"`
	VersionMessage *string                    `json:"version_message"`
	CreatedAt      time.Time                  `json:"created_at"`
	UpdatedAt      time.Time                  `json:"updated_at"`
}

// AgentTool is one entry of an agent's tools, a union on Type: function,
// web_search, web_search_premium, code_interpreter, image_generation,
// document_library or connector.
type AgentTool struct {
	Type              string             `json:"type"`
	Function          *FunctionTool      `json:"function,omitempty"`
	LibraryIDs        []string           `json:"library_ids,omitempty"`
	ConnectorID       *string            `json:"connector_id,omitempty"`
	ToolConfiguration *ToolConfiguration `json:"tool_configuration,omitempty"`
}

// FunctionTool is the function of a function tool. Parameters is a JSON
// schema object.
type FunctionTool struct {
	Name        string          `json:"name"`
	Description *string         `json:"description,omitempty"`
	Strict      *bool           `json:"strict,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

// ToolConfiguration selects the functions of a tool the agent may call, and
// those that need a confirmation.
type ToolConfiguration struct {
	Include              []string `json:"include,omitempty"`
	Exclude              []string `json:"exclude,omitempty"`
	RequiresConfirmation []string `json:"requires_confirmation,omitempty"`
}

// CompletionArgs are the sampling arguments of an agent. Every field is sent,
// null included, so that an update clears what the configuration removed.
type CompletionArgs struct {
	Stop             []string        `json:"stop"`
	PresencePenalty  *float64        `json:"presence_penalty"`
	FrequencyPenalty *float64        `json:"frequency_penalty"`
	Temperature      *float64        `json:"temperature"`
	TopP             *float64        `json:"top_p"`
	MaxTokens        *int64          `json:"max_tokens"`
	RandomSeed       *int64          `json:"random_seed"`
	ResponseFormat   *ResponseFormat `json:"response_format"`
	ToolChoice       *string         `json:"tool_choice"`
	ReasoningEffort  *string         `json:"reasoning_effort"`
}

// ResponseFormat is text, json_object, or json_schema with its JSONSchema.
type ResponseFormat struct {
	Type       string      `json:"type"`
	JSONSchema *JSONSchema `json:"json_schema,omitempty"`
}

// JSONSchema is the schema of a json_schema response format.
type JSONSchema struct {
	Name        string          `json:"name"`
	Description *string         `json:"description,omitempty"`
	Schema      json.RawMessage `json:"schema"`
	Strict      *bool           `json:"strict,omitempty"`
}

// AgentRequest is the body of a create (CreateAgentRequest) and of an update
// (UpdateAgentRequest). Every field is sent, null included: an update
// replaces the agent's settings with the configuration's.
type AgentRequest struct {
	Name           string            `json:"name"`
	Model          string            `json:"model"`
	Description    *string           `json:"description"`
	Instructions   *string           `json:"instructions"`
	Tools          []AgentTool       `json:"tools"`
	CompletionArgs CompletionArgs    `json:"completion_args"`
	Handoffs       []string          `json:"handoffs"`
	Metadata       map[string]string `json:"metadata"`
	VersionMessage *string           `json:"version_message"`
}

// AgentAlias names a version of an agent.
type AgentAlias struct {
	Alias     string    `json:"alias"`
	Version   int64     `json:"version"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CreateAgent creates an agent at version 0: POST /v1/agents. It is not
// idempotent.
func (cl *Client) CreateAgent(ctx context.Context, req AgentRequest) (*Agent, error) {
	var out Agent
	if err := cl.do(ctx, call{method: http.MethodPost, path: "/v1/agents", body: withTools(req), audience: workspaceKey}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetAgent returns the agent at its active version: GET /v1/agents/{agent_id}.
func (cl *Client) GetAgent(ctx context.Context, id string) (*Agent, error) {
	var out Agent
	if err := cl.do(ctx, call{method: http.MethodGet, path: escapePath("/v1/agents", id), audience: workspaceKey}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateAgent creates a new version of the agent with req, which becomes the
// active one: PATCH /v1/agents/{agent_id}. It is not idempotent: a replay
// creates another version.
func (cl *Client) UpdateAgent(ctx context.Context, id string, req AgentRequest) (*Agent, error) {
	var out Agent
	if err := cl.do(ctx, call{method: http.MethodPatch, path: escapePath("/v1/agents", id), body: withTools(req), audience: workspaceKey}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteAgent deletes the agent and its versions: DELETE /v1/agents/{agent_id}.
func (cl *Client) DeleteAgent(ctx context.Context, id string) error {
	return cl.do(ctx, call{method: http.MethodDelete, path: escapePath("/v1/agents", id), audience: workspaceKey}, nil)
}

// ListAgentAliases returns every alias of the agent: GET
// /v1/agents/{agent_id}/aliases, which is not paginated.
func (cl *Client) ListAgentAliases(ctx context.Context, agentID string) ([]AgentAlias, error) {
	out := []AgentAlias{}
	if err := cl.do(ctx, call{method: http.MethodGet, path: escapePath("/v1/agents", agentID, "aliases"), audience: workspaceKey}, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []AgentAlias{}
	}
	return out, nil
}

// PutAgentAlias points alias to version, creating the alias when the agent
// has none by that name: PUT /v1/agents/{agent_id}/aliases?alias=&version=.
func (cl *Client) PutAgentAlias(ctx context.Context, agentID, alias string, version int64) (*AgentAlias, error) {
	var out AgentAlias
	q := url.Values{"alias": {alias}, "version": {strconv.FormatInt(version, 10)}}
	if err := cl.do(ctx, call{method: http.MethodPut, path: escapePath("/v1/agents", agentID, "aliases"), query: q, audience: workspaceKey}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteAgentAlias deletes alias: DELETE /v1/agents/{agent_id}/aliases?alias=.
func (cl *Client) DeleteAgentAlias(ctx context.Context, agentID, alias string) error {
	q := url.Values{"alias": {alias}}
	return cl.do(ctx, call{method: http.MethodDelete, path: escapePath("/v1/agents", agentID, "aliases"), query: q, audience: workspaceKey}, nil)
}

// withTools sends an empty tools array rather than null: the specification
// types tools as an array, not nullable.
func withTools(req AgentRequest) AgentRequest {
	if req.Tools == nil {
		req.Tools = []AgentTool{}
	}
	return req
}
