// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"time"
)

// Model is a model card: BaseModelCard, or FTModelCard for a fine-tuned
// model, which adds job, root and archived
// (https://docs.mistral.ai/api/endpoint/models).
type Model struct {
	ID                          string            `json:"id"`
	Object                      string            `json:"object"`
	Created                     int64             `json:"created"`
	OwnedBy                     string            `json:"owned_by"`
	Name                        *string           `json:"name"`
	Description                 *string           `json:"description"`
	MaxContextLength            int64             `json:"max_context_length"`
	Aliases                     []string          `json:"aliases"`
	Deprecation                 *time.Time        `json:"deprecation"`
	DeprecationReplacementModel *string           `json:"deprecation_replacement_model"`
	DefaultModelTemperature     *float64          `json:"default_model_temperature"`
	Type                        string            `json:"type"`
	Capabilities                ModelCapabilities `json:"capabilities"`
	// Root is the base model of a fine-tuned model.
	Root *string `json:"root"`
	// Archived is set on fine-tuned models.
	Archived *bool `json:"archived"`
}

// ModelCapabilities says what a model can do.
type ModelCapabilities struct {
	CompletionChat     bool `json:"completion_chat"`
	CompletionFIM      bool `json:"completion_fim"`
	FunctionCalling    bool `json:"function_calling"`
	Reasoning          bool `json:"reasoning"`
	FineTuning         bool `json:"fine_tuning"`
	Vision             bool `json:"vision"`
	OCR                bool `json:"ocr"`
	Classification     bool `json:"classification"`
	Moderation         bool `json:"moderation"`
	Audio              bool `json:"audio"`
	AudioTranscription bool `json:"audio_transcription"`
	AudioSpeech        bool `json:"audio_speech"`
}

// ListModels returns every model the key's workspace can use: GET /v1/models,
// which is not paginated and answers {"object": "list", "data": [...]}.
func (cl *Client) ListModels(ctx context.Context) ([]Model, error) {
	var out struct {
		Data []Model `json:"data"`
	}
	err := cl.do(ctx, call{method: http.MethodGet, path: "/v1/models", audience: workspaceKey}, &out)
	if err != nil {
		return nil, err
	}
	if out.Data == nil {
		out.Data = []Model{}
	}
	return out.Data, nil
}

// GetModel returns the model id, an ID or an alias: GET /v1/models/{model_id}.
func (cl *Client) GetModel(ctx context.Context, id string) (*Model, error) {
	var out Model
	if err := cl.do(ctx, call{method: http.MethodGet, path: escapePath("/v1/models", id), audience: workspaceKey}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
