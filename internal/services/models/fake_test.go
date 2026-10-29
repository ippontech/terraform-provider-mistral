// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package models_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

const (
	routeList = "GET /v1/models"
	routeGet  = "GET /v1/models/{model_id}"
)

// medium is a Mistral model card, with the fields the provider maps
// (BaseModelCard in the specification).
func medium() map[string]any {
	return map[string]any{
		"id": "mistral-medium-2604", "object": "model", "created": 1777000000, "owned_by": "mistralai",
		"name": "Mistral Medium 3.5", "description": "Frontier model", "max_context_length": 131072,
		"aliases": []any{"mistral-medium-latest"}, "deprecation": nil, "deprecation_replacement_model": nil,
		"default_model_temperature": 0.3, "type": "base",
		"capabilities": map[string]any{"completion_chat": true, "function_calling": true, "vision": true},
	}
}

// fineTuned is a fine-tuned model card (FTModelCard), deprecated.
func fineTuned() map[string]any {
	return map[string]any{
		"id": "ft:open-mistral-7b:abc:20240514:7e77", "object": "model", "created": 1715700000, "owned_by": "org-1",
		"max_context_length": 32768, "aliases": []any{}, "deprecation": "2026-12-31T00:00:00Z",
		"deprecation_replacement_model": "mistral-small-latest", "type": "fine-tuned",
		"job": "job-1", "root": "open-mistral-7b", "archived": false,
		"capabilities": map[string]any{"completion_chat": true, "fine_tuning": true},
	}
}

// newFake serves the models API over cards: GET /v1/models lists them, GET
// /v1/models/{model_id} resolves an ID or an alias, and answers 404
// otherwise (https://docs.mistral.ai/api/endpoint/models).
func newFake(t *testing.T, cards ...map[string]any) *sdktest.Server {
	t.Helper()
	srv := sdktest.NewServer(t)
	srv.Handle(routeList, func(w http.ResponseWriter, _ *http.Request) {
		data := make([]any, 0, len(cards))
		for _, c := range cards {
			data = append(data, c)
		}
		sdktest.WriteJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
	})
	srv.Handle(routeGet, func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("model_id")
		for _, c := range cards {
			aliases, _ := c["aliases"].([]any)
			if c["id"] == id || slices.Contains(aliases, any(id)) {
				sdktest.WriteJSON(w, http.StatusOK, c)
				return
			}
		}
		sdktest.WriteError(w, http.StatusNotFound, "Model not found")
	})
	return srv
}
