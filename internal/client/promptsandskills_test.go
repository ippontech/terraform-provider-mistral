// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"testing"

	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

// A create sends every field in the camelCase of the specification, an
// empty variables array rather than null, and is not replayed after a 5xx.
func TestCreatePromptSendsTheSpecificationShape(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("POST /v2/prompts", http.StatusOK, map[string]any{
		"id": "p_1", "name": "support", "version": 1, "latestVersion": 1,
		"definition":   map[string]any{"content": "Hello"},
		"sharingScope": "workspace", "createdAt": "2026-10-29T10:00:00Z", "updatedAt": "2026-10-29T10:01:00Z",
	})
	c := newTestClient(srv)

	p, err := c.CreatePrompt(context.Background(), PromptRequest{
		Name: "support", Definition: PromptDefinition{Content: "Hello"},
		Title: new("Support"), SharingScope: "workspace", WorkspaceRelation: new("reader"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "p_1" || p.Version != 1 || p.Definition.Content != "Hello" || p.SharingScope != "workspace" || p.UpdatedAt.Minute() != 1 {
		t.Errorf("prompt = %+v", p)
	}
	srv.AssertJSONBodies("POST /v2/prompts", `{
		"name": "support", "definition": {"content": "Hello", "variables": []},
		"title": "Support", "description": null, "notes": null,
		"sharingScope": "workspace", "workspaceRelation": "reader"
	}`)

	srv.FailNext("POST /v2/prompts", http.StatusBadGateway, 1)
	if _, err := c.CreatePrompt(context.Background(), PromptRequest{Name: "a"}); StatusCode(err) != http.StatusBadGateway {
		t.Errorf("err = %v, want the 502", err)
	}
	srv.AssertCalled("POST /v2/prompts", 2)
	srv.AssertCredential("", sdktest.APIKey)
}

// The prompt calls send their bodies to their paths, and a read is retried
// on a 5xx.
func TestPromptCallsAndTheirRetries(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("GET /v2/prompts/{prompt_id}", http.StatusOK, map[string]any{"id": "p_1", "version": 3, "latestVersion": 3})
	srv.Respond("PATCH /v2/prompts/{prompt_id}", http.StatusOK, map[string]any{"id": "p_1", "title": "T"})
	srv.Respond("DELETE /v2/prompts/{prompt_id}", http.StatusOK, map[string]any{})
	srv.Respond("POST /v2/prompts/{prompt_id}/versions", http.StatusOK, map[string]any{"version": 4, "deduplicated": false})
	srv.Respond("PATCH /v2/prompts/{prompt_id}/versions/{version}", http.StatusOK, map[string]any{"id": "p_1", "version": 4, "notes": "n"})
	c := newTestClient(srv)
	ctx := context.Background()

	srv.FailNext("GET /v2/prompts/{prompt_id}", http.StatusServiceUnavailable, 1)
	if p, err := c.GetPrompt(ctx, "p_1"); err != nil || p.Version != 3 || p.LatestVersion != 3 {
		t.Errorf("GetPrompt = %+v, %v", p, err)
	}
	if p, err := c.UpdatePrompt(ctx, "p_1", PromptUpdate{Title: "T", SharingScope: "private"}); err != nil || p.Title != "T" {
		t.Errorf("UpdatePrompt = %+v, %v", p, err)
	}
	if v, err := c.CreatePromptVersion(ctx, "p_1", PromptVersionRequest{Definition: PromptDefinition{Content: "v4"}, Notes: new("n")}); err != nil || v.Version != 4 || v.Deduplicated {
		t.Errorf("CreatePromptVersion = %+v, %v", v, err)
	}
	if p, err := c.SetPromptVersionNotes(ctx, "p_1", 4, ""); err != nil || p.Notes != "n" {
		t.Errorf("SetPromptVersionNotes = %+v, %v", p, err)
	}
	if err := c.DeletePrompt(ctx, "p_1"); err != nil {
		t.Error(err)
	}

	srv.AssertCalled("GET /v2/prompts/{prompt_id}", 2)
	srv.AssertJSONBodies("PATCH /v2/prompts/{prompt_id}", `{"title": "T", "description": "", "sharingScope": "private", "workspaceRelation": null}`)
	srv.AssertJSONBodies("POST /v2/prompts/{prompt_id}/versions", `{"definition": {"content": "v4", "variables": []}, "notes": "n"}`)
	srv.AssertJSONBodies("PATCH /v2/prompts/{prompt_id}/versions/{version}", `{"notes": ""}`)
	if got := srv.Requests("PATCH /v2/prompts/{prompt_id}/versions/{version}")[0].Path; got != "/v2/prompts/p_1/versions/4" {
		t.Errorf("path = %s", got)
	}
	srv.AssertQueries("GET /v2/prompts/{prompt_id}", "", "")
}

// The writes that are not upserts are sent once on a 5xx: a new version may
// have landed, and an update is not documented as idempotent.
func TestPromptWritesAreSentOnceOnA5xx(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("POST /v2/prompts/{prompt_id}/versions", http.StatusOK, map[string]any{"version": 2})
	srv.Respond("PATCH /v2/prompts/{prompt_id}", http.StatusOK, map[string]any{})
	srv.Respond("PATCH /v2/prompts/{prompt_id}/versions/{version}", http.StatusOK, map[string]any{})
	for _, route := range []string{"POST /v2/prompts/{prompt_id}/versions", "PATCH /v2/prompts/{prompt_id}", "PATCH /v2/prompts/{prompt_id}/versions/{version}"} {
		srv.FailNext(route, http.StatusInternalServerError, 1)
	}
	c := newTestClient(srv)
	ctx := context.Background()

	if _, err := c.CreatePromptVersion(ctx, "p_1", PromptVersionRequest{}); StatusCode(err) != http.StatusInternalServerError {
		t.Errorf("CreatePromptVersion err = %v", err)
	}
	if _, err := c.UpdatePrompt(ctx, "p_1", PromptUpdate{}); StatusCode(err) != http.StatusInternalServerError {
		t.Errorf("UpdatePrompt err = %v", err)
	}
	if _, err := c.SetPromptVersionNotes(ctx, "p_1", 1, "n"); StatusCode(err) != http.StatusInternalServerError {
		t.Errorf("SetPromptVersionNotes err = %v", err)
	}
	for _, route := range []string{"POST /v2/prompts/{prompt_id}/versions", "PATCH /v2/prompts/{prompt_id}", "PATCH /v2/prompts/{prompt_id}/versions/{version}"} {
		srv.AssertCalled(route, 1)
	}
}

// A skill's definition sends every field, an empty assets object rather than
// null, and each asset with the one branch of its union.
func TestCreateSkillSendsItsAssetsInline(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("POST /v2/skills", http.StatusOK, map[string]any{
		"id": "s_1", "name": "extract", "version": 1,
		"definition": map[string]any{"body": "Run it", "assets": map[string]any{
			"run.py": map[string]any{"textContent": "print(1)", "isExecutable": true},
		}},
	})
	c := newTestClient(srv)
	ctx := context.Background()

	s, err := c.CreateSkill(ctx, SkillRequest{
		Name: "extract", SharingScope: "private",
		Definition: SkillDefinition{Body: "Run it", Assets: map[string]SkillAsset{
			"run.py":   {TextContent: new("print(1)"), IsExecutable: true},
			"logo.png": {RawContent: new("iVBORw0KGgo=")},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if a := s.Definition.Assets["run.py"]; s.ID != "s_1" || a.TextContent == nil || *a.TextContent != "print(1)" || !a.IsExecutable {
		t.Errorf("skill = %+v", s)
	}
	if _, err := c.CreateSkill(ctx, SkillRequest{Name: "empty", SharingScope: "private"}); err != nil {
		t.Fatal(err)
	}
	srv.AssertJSONBodies("POST /v2/skills", `{
		"name": "extract", "notes": null, "sharingScope": "private", "workspaceRelation": null,
		"definition": {"description": "", "body": "Run it", "assets": {
			"run.py": {"textContent": "print(1)", "isExecutable": true},
			"logo.png": {"rawContent": "iVBORw0KGgo=", "isExecutable": false}
		}}
	}`, `{
		"name": "empty", "notes": null, "sharingScope": "private", "workspaceRelation": null,
		"definition": {"description": "", "body": "", "assets": {}}
	}`)
}

// The skill calls send their bodies to their paths and return the API's
// errors.
func TestSkillCallsAndTheirErrors(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("GET /v2/skills/{skill_id}", http.StatusOK, map[string]any{"id": "s_1", "version": 2})
	srv.Respond("PATCH /v2/skills/{skill_id}", http.StatusOK, map[string]any{"id": "s_1", "sharingScope": "workspace"})
	srv.Respond("DELETE /v2/skills/{skill_id}", http.StatusNotFound, map[string]any{"detail": "Skill not found"})
	srv.Respond("POST /v2/skills/{skill_id}/versions", http.StatusOK, map[string]any{"version": 2, "deduplicated": true})
	srv.Respond("PATCH /v2/skills/{skill_id}/versions/{version}", http.StatusNotFound, map[string]any{"detail": "Version not found"})
	c := newTestClient(srv)
	ctx := context.Background()

	if s, err := c.GetSkill(ctx, "s_1"); err != nil || s.Version != 2 {
		t.Errorf("GetSkill = %+v, %v", s, err)
	}
	if s, err := c.UpdateSkill(ctx, "s_1", SkillUpdate{SharingScope: "workspace", WorkspaceRelation: new("writer")}); err != nil || s.SharingScope != "workspace" {
		t.Errorf("UpdateSkill = %+v, %v", s, err)
	}
	if v, err := c.CreateSkillVersion(ctx, "s_1", SkillVersionRequest{Definition: SkillDefinition{Body: "b"}}); err != nil || !v.Deduplicated {
		t.Errorf("CreateSkillVersion = %+v, %v", v, err)
	}
	if _, err := c.SetSkillVersionNotes(ctx, "s_1", 9, "n"); !IsNotFound(err) {
		t.Errorf("SetSkillVersionNotes err = %v, want a 404", err)
	}
	if err := c.DeleteSkill(ctx, "s_1"); !IsNotFound(err) {
		t.Errorf("DeleteSkill err = %v, want a 404", err)
	}

	srv.AssertJSONBodies("PATCH /v2/skills/{skill_id}", `{"sharingScope": "workspace", "workspaceRelation": "writer"}`)
	srv.AssertJSONBodies("POST /v2/skills/{skill_id}/versions", `{"definition": {"description": "", "body": "b", "assets": {}}, "notes": null}`)
	srv.AssertJSONBodies("PATCH /v2/skills/{skill_id}/versions/{version}", `{"notes": "n"}`)
}

// A 404 and a 409 come back as errors IsNotFound and IsConflict recognize.
func TestRegistryCallsReturnTheAPIError(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("GET /v2/prompts/{prompt_id}", http.StatusNotFound, map[string]any{"detail": "Prompt not found"})
	srv.Respond("POST /v2/skills", http.StatusConflict, map[string]any{"detail": "Skill already exists"})
	c := newTestClient(srv)
	ctx := context.Background()

	if _, err := c.GetPrompt(ctx, "p_1"); !IsNotFound(err) {
		t.Errorf("GetPrompt err = %v, want a 404", err)
	}
	if _, err := c.CreateSkill(ctx, SkillRequest{Name: "a"}); !IsConflict(err) {
		t.Errorf("CreateSkill err = %v, want a 409", err)
	}
}
