// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

func TestCreateAgentSendsEveryFieldAndAnEmptyToolList(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("POST /v1/agents", http.StatusOK, map[string]any{
		"id": "ag_1", "name": "support", "model": "mistral-medium-latest", "version": 0,
		"created_at": "2026-10-29T10:00:00Z", "updated_at": "2026-10-29T10:00:00Z",
		"tools": []any{}, "metadata": map[string]any{"team": "support", "tier": 2},
	})

	a, err := newTestClient(srv).CreateAgent(context.Background(), AgentRequest{
		Name: "support", Model: "mistral-medium-latest",
		CompletionArgs: CompletionArgs{ToolChoice: new("auto")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != "ag_1" || a.Version != 0 || string(a.Metadata["tier"]) != "2" {
		t.Errorf("agent = %+v", a)
	}
	srv.AssertJSONBodies("POST /v1/agents", `{
		"name": "support", "model": "mistral-medium-latest", "description": null, "instructions": null,
		"tools": [], "handoffs": null, "metadata": null, "version_message": null,
		"completion_args": {"stop": null, "presence_penalty": null, "frequency_penalty": null, "temperature": null,
			"top_p": null, "max_tokens": null, "random_seed": null, "response_format": null,
			"tool_choice": "auto", "reasoning_effort": null}
	}`)
	srv.AssertCredential("", sdktest.APIKey)
}

func TestCreateAgentIsSentOnceOnA5xx(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("POST /v1/agents", http.StatusOK, map[string]any{"id": "ag_1"})
	srv.FailNext("POST /v1/agents", http.StatusBadGateway, 1)

	if _, err := newTestClient(srv).CreateAgent(context.Background(), AgentRequest{Name: "a", Model: "m"}); StatusCode(err) != http.StatusBadGateway {
		t.Errorf("err = %v, want the 502", err)
	}
	srv.AssertCalled("POST /v1/agents", 1)
}

func TestAgentToolsEncodeTheirUnionBranchOnly(t *testing.T) {
	t.Parallel()
	b, err := json.Marshal([]AgentTool{
		{Type: "web_search"},
		{Type: "document_library", LibraryIDs: []string{"lib_1"}, ToolConfiguration: &ToolConfiguration{RequiresConfirmation: []string{"search"}}},
		{Type: "connector", ConnectorID: new("con_1")},
		{Type: "function", Function: &FunctionTool{Name: "lookup", Strict: new(true), Parameters: json.RawMessage(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"type":"web_search"},` +
		`{"type":"document_library","library_ids":["lib_1"],"tool_configuration":{"requires_confirmation":["search"]}},` +
		`{"type":"connector","connector_id":"con_1"},` +
		`{"type":"function","function":{"name":"lookup","strict":true,"parameters":{"type":"object"}}}]`
	if diff := sdktest.JSONDiff(string(b), want); diff != "" {
		t.Error(diff)
	}
}

func TestGetUpdateDeleteAgent(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("GET /v1/agents/{agent_id}", http.StatusOK, map[string]any{"id": "ag_1", "version": 3})
	srv.Respond("PATCH /v1/agents/{agent_id}", http.StatusOK, map[string]any{"id": "ag_1", "version": 4})
	srv.Respond("DELETE /v1/agents/{agent_id}", http.StatusNoContent, nil)
	c := newTestClient(srv)
	ctx := context.Background()

	if a, err := c.GetAgent(ctx, "ag_1"); err != nil || a.Version != 3 {
		t.Errorf("GetAgent = %+v, %v", a, err)
	}
	if a, err := c.UpdateAgent(ctx, "ag_1", AgentRequest{Name: "a", Model: "m"}); err != nil || a.Version != 4 {
		t.Errorf("UpdateAgent = %+v, %v", a, err)
	}
	if err := c.DeleteAgent(ctx, "ag_1"); err != nil {
		t.Error(err)
	}
	srv.FailNext("PATCH /v1/agents/{agent_id}", http.StatusServiceUnavailable, 1)
	if _, err := c.UpdateAgent(ctx, "ag_1", AgentRequest{}); StatusCode(err) != http.StatusServiceUnavailable {
		t.Errorf("an update after a 503 must not be replayed: %v", err)
	}
	srv.AssertCalled("PATCH /v1/agents/{agent_id}", 2)
}

func TestAgentAliases(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("GET /v1/agents/{agent_id}/aliases", http.StatusOK, []any{
		map[string]any{"alias": "production", "version": 2, "created_at": "2026-10-29T10:00:00Z", "updated_at": "2026-10-29T10:00:00Z"},
	})
	srv.Respond("PUT /v1/agents/{agent_id}/aliases", http.StatusOK, map[string]any{"alias": "production", "version": 3})
	srv.Respond("DELETE /v1/agents/{agent_id}/aliases", http.StatusNoContent, nil)
	c := newTestClient(srv)
	ctx := context.Background()

	if got, err := c.ListAgentAliases(ctx, "ag_1"); err != nil || len(got) != 1 || got[0].Version != 2 {
		t.Errorf("ListAgentAliases = %+v, %v", got, err)
	}
	srv.FailNext("PUT /v1/agents/{agent_id}/aliases", http.StatusServiceUnavailable, 1)
	if a, err := c.PutAgentAlias(ctx, "ag_1", "production", 3); err != nil || a.Version != 3 {
		t.Errorf("PutAgentAlias = %+v, %v", a, err)
	}
	if err := c.DeleteAgentAlias(ctx, "ag_1", "production"); err != nil {
		t.Error(err)
	}
	srv.AssertQueries("PUT /v1/agents/{agent_id}/aliases", "alias=production&version=3", "alias=production&version=3")
	srv.AssertQueries("DELETE /v1/agents/{agent_id}/aliases", "alias=production")
}

func TestListAgentAliasesAnswersNullAsEmpty(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("GET /v1/agents/{agent_id}/aliases", http.StatusOK, json.RawMessage("null"))
	got, err := newTestClient(srv).ListAgentAliases(context.Background(), "ag_1")
	if err != nil || got == nil || len(got) != 0 {
		t.Errorf("ListAgentAliases = %#v, %v", got, err)
	}
	missing := sdktest.NewServer(t)
	missing.Respond("GET /v1/agents/{agent_id}/aliases", http.StatusNotFound, map[string]any{"detail": "Agent not found"})
	if _, err := newTestClient(missing).ListAgentAliases(context.Background(), "ag_1"); !IsNotFound(err) {
		t.Errorf("err = %v, want a 404", err)
	}
}

func TestAgentCallsReturnTheAPIError(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("GET /v1/agents/{agent_id}", http.StatusNotFound, map[string]any{"detail": "Agent not found"})
	srv.Respond("PUT /v1/agents/{agent_id}/aliases", http.StatusNotFound, map[string]any{"detail": "Agent version not found"})
	srv.Respond("GET /v1/agents/{agent_id}/aliases", http.StatusForbidden, map[string]any{"detail": "Forbidden"})
	c := newTestClient(srv)
	ctx := context.Background()

	if _, err := c.GetAgent(ctx, "ag_1"); !IsNotFound(err) {
		t.Errorf("GetAgent err = %v, want a 404", err)
	}
	if _, err := c.PutAgentAlias(ctx, "ag_1", "production", 9); !IsNotFound(err) {
		t.Errorf("PutAgentAlias err = %v, want a 404", err)
	}
	if _, err := c.ListAgentAliases(ctx, "ag_1"); StatusCode(err) != http.StatusForbidden {
		t.Errorf("ListAgentAliases err = %v, want a 403", err)
	}
}
