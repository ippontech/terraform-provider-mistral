// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

const (
	routeCreate      = "POST /v1/agents"
	routeGet         = "GET /v1/agents/{agent_id}"
	routeUpdate      = "PATCH /v1/agents/{agent_id}"
	routeDelete      = "DELETE /v1/agents/{agent_id}"
	routeListAliases = "GET /v1/agents/{agent_id}/aliases"
	routePutAlias    = "PUT /v1/agents/{agent_id}/aliases"
	routeDeleteAlias = "DELETE /v1/agents/{agent_id}/aliases"
)

// fake serves the agents API (https://docs.mistral.ai/api/endpoint/beta/agents)
// over an in-memory store. Besides the transport, it holds what the provider
// depends on: the version a create and each update produce, the server-side
// defaults of a create and an update (an absent tools is [], completion_args
// echoes every field with tool_choice "auto", as the agents guide shows at
// https://docs.mistral.ai/studio/agents/agents-api), and the aliases of each agent.
type fake struct {
	*sdktest.Server
	agents  *sdktest.Collection
	aliases *sdktest.Collection

	mu    sync.Mutex
	clock time.Time
}

func newFake(t *testing.T) *fake {
	t.Helper()
	f := &fake{
		Server:  sdktest.NewServer(t),
		agents:  sdktest.NewCollection("id"),
		aliases: sdktest.NewCollection("key"),
		clock:   time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC),
	}
	f.Handle(routeCreate, f.create)
	f.Handle(routeGet, f.get)
	f.Handle(routeUpdate, f.update)
	f.Handle(routeDelete, f.delete)
	f.Handle(routeListAliases, f.listAliases)
	f.Handle(routePutAlias, f.putAlias)
	f.Handle(routeDeleteAlias, f.deleteAlias)
	return f
}

// now returns the time of the next write: one minute after the previous one.
func (f *fake) now() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clock = f.clock.Add(time.Minute)
	return f.clock.Format(time.RFC3339)
}

// at is the time of the n-th write of the fake, from 1.
func at(n int) string {
	return time.Date(2026, 10, 29, 10, n, 0, 0, time.UTC).Format(time.RFC3339)
}

func (f *fake) create(w http.ResponseWriter, r *http.Request) {
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	now := f.now()
	agent := withDefaults(body)
	agent["id"] = "ag_" + sdktest.NewUUID()
	agent["object"] = "agent"
	agent["version"] = 0
	agent["versions"] = []any{0}
	agent["created_at"], agent["updated_at"] = now, now
	agent["deployment_chat"] = false
	agent["source"] = "api"
	sdktest.WriteJSON(w, http.StatusOK, f.agents.Create(agent))
}

func (f *fake) get(w http.ResponseWriter, r *http.Request) {
	agent, ok := f.agents.Get(r.PathValue("agent_id"))
	if !ok {
		sdktest.WriteError(w, http.StatusNotFound, "Agent not found")
		return
	}
	sdktest.WriteJSON(w, http.StatusOK, agent)
}

// update applies the fields of the body, null included, and creates the
// next version: "Update an agent attributes and create a new version".
func (f *fake) update(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("agent_id")
	agent, ok := f.agents.Get(id)
	if !ok {
		sdktest.WriteError(w, http.StatusNotFound, "Agent not found")
		return
	}
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	next := len(agent["versions"].([]any))
	patch := withDefaults(body)
	patch["version"] = next
	patch["versions"] = append(agent["versions"].([]any), next)
	patch["updated_at"] = f.now()
	updated, _ := f.agents.Patch(id, patch)
	sdktest.WriteJSON(w, http.StatusOK, updated)
}

func (f *fake) delete(w http.ResponseWriter, r *http.Request) {
	if !f.agents.Delete(r.PathValue("agent_id")) {
		sdktest.WriteError(w, http.StatusNotFound, "Agent not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (f *fake) listAliases(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("agent_id")
	if _, ok := f.agents.Get(id); !ok {
		sdktest.WriteError(w, http.StatusNotFound, "Agent not found")
		return
	}
	out := []any{}
	for _, a := range f.aliases.List(func(a map[string]any) bool { return a["agent_id"] == id }) {
		out = append(out, aliasBody(a))
	}
	sdktest.WriteJSON(w, http.StatusOK, out)
}

// putAlias creates the alias or points it at another version; the version
// must exist.
func (f *fake) putAlias(w http.ResponseWriter, r *http.Request) {
	id, name := r.PathValue("agent_id"), r.URL.Query().Get("alias")
	agent, ok := f.agents.Get(id)
	if !ok {
		sdktest.WriteError(w, http.StatusNotFound, "Agent not found")
		return
	}
	version, err := strconv.Atoi(r.URL.Query().Get("version"))
	if err != nil || !slices.ContainsFunc(agent["versions"].([]any), func(v any) bool { return number(v) == version }) {
		sdktest.WriteError(w, http.StatusNotFound, "Agent version not found")
		return
	}
	key := id + ":" + name
	now := f.now()
	alias, exists := f.aliases.Get(key)
	if !exists {
		alias = map[string]any{"key": key, "agent_id": id, "alias": name, "created_at": now}
	}
	alias["version"], alias["updated_at"] = version, now
	f.aliases.Put(alias)
	sdktest.WriteJSON(w, http.StatusOK, aliasBody(alias))
}

func (f *fake) deleteAlias(w http.ResponseWriter, r *http.Request) {
	if !f.aliases.Delete(r.PathValue("agent_id") + ":" + r.URL.Query().Get("alias")) {
		sdktest.WriteError(w, http.StatusNotFound, "Alias not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// withDefaults returns the body with the server-side defaults of the API.
func withDefaults(body map[string]any) map[string]any {
	out := sdktest.DeepCopy(body)
	if out["tools"] == nil {
		out["tools"] = []any{}
	}
	args, _ := out["completion_args"].(map[string]any)
	if args == nil {
		args = map[string]any{}
	}
	for _, k := range []string{"stop", "presence_penalty", "frequency_penalty", "temperature", "top_p", "max_tokens", "random_seed", "prediction", "response_format", "reasoning_effort"} {
		if _, ok := args[k]; !ok {
			args[k] = nil
		}
	}
	if args["tool_choice"] == nil {
		args["tool_choice"] = "auto"
	}
	out["completion_args"] = args
	return out
}

func aliasBody(a map[string]any) map[string]any {
	return map[string]any{"alias": a["alias"], "version": a["version"], "created_at": a["created_at"], "updated_at": a["updated_at"]}
}

// number reads a JSON number of the store, which DeepCopy keeps as
// json.Number.
func number(v any) int {
	switch n := v.(type) {
	case json.Number:
		i, _ := n.Int64()
		return int(i)
	case int:
		return n
	}
	return -1
}

// seed stores an agent created outside Terraform and returns its ID.
func (f *fake) seed(agent map[string]any) string {
	now := f.now()
	a := withDefaults(agent)
	a["id"], a["object"], a["version"], a["versions"] = "ag_"+sdktest.NewUUID(), "agent", 0, []any{0}
	a["created_at"], a["updated_at"] = now, now
	return f.agents.Create(a)["id"].(string)
}
