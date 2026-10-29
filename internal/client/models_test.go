// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

func TestListModels(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("GET /v1/models", http.StatusOK, map[string]any{
		"object": "list",
		"data": []any{
			map[string]any{
				"id": "mistral-medium-2604", "object": "model", "created": 1777000000, "owned_by": "mistralai",
				"name": "Mistral Medium 3.5", "max_context_length": 131072, "aliases": []string{"mistral-medium-latest"},
				"deprecation": "2027-04-30T00:00:00Z", "deprecation_replacement_model": "mistral-medium-2610",
				"type": "base", "capabilities": map[string]any{"completion_chat": true, "function_calling": true, "vision": true},
			},
			map[string]any{"id": "ft:open-mistral-7b:1", "type": "fine-tuned", "root": "open-mistral-7b", "archived": false,
				"capabilities": map[string]any{"completion_chat": true}},
		},
	})

	got, err := newTestClient(srv).ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("%d models", len(got))
	}
	m := got[0]
	wantDeprecation := time.Date(2027, 4, 30, 0, 0, 0, 0, time.UTC)
	if m.ID != "mistral-medium-2604" || *m.Name != "Mistral Medium 3.5" || m.MaxContextLength != 131072 ||
		m.Aliases[0] != "mistral-medium-latest" || !m.Deprecation.Equal(wantDeprecation) ||
		*m.DeprecationReplacementModel != "mistral-medium-2610" || !m.Capabilities.Vision || m.Capabilities.OCR {
		t.Errorf("model = %+v", m)
	}
	if got[1].Root == nil || *got[1].Root != "open-mistral-7b" || got[1].Archived == nil || *got[1].Archived {
		t.Errorf("fine-tuned model = %+v", got[1])
	}
	srv.AssertCredential("GET /v1/models", sdktest.APIKey)
}

func TestListModelsAnswersAnEmptyListAsEmpty(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("GET /v1/models", http.StatusOK, map[string]any{"object": "list"})
	got, err := newTestClient(srv).ListModels(context.Background())
	if err != nil || got == nil || len(got) != 0 {
		t.Errorf("ListModels = %#v, %v", got, err)
	}
}

func TestGetModel(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Handle("GET /v1/models/{model_id}", func(w http.ResponseWriter, r *http.Request) {
		if id := r.PathValue("model_id"); id != "ft:open-mistral-7b:1" {
			sdktest.WriteError(w, http.StatusNotFound, "Model not found")
			return
		}
		sdktest.WriteJSON(w, http.StatusOK, map[string]any{"id": "ft:open-mistral-7b:1", "capabilities": map[string]any{}})
	})
	c := newTestClient(srv)

	m, err := c.GetModel(context.Background(), "ft:open-mistral-7b:1")
	if err != nil || m.ID != "ft:open-mistral-7b:1" {
		t.Errorf("GetModel = %+v, %v", m, err)
	}
	if _, err := c.GetModel(context.Background(), "nope"); !IsNotFound(err) {
		t.Errorf("err = %v, want a 404", err)
	}
}
