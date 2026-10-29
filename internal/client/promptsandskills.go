// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"net/http"
	"strconv"
	"time"
)

// The registry holds the prompts (/v2/prompts, tag beta.prompts,
// https://docs.mistral.ai/api/endpoint/beta/prompts) and the skills
// (/v2/skills, tag beta.skills, https://docs.mistral.ai/api/endpoint/beta/skills)
// of a workspace. The specification calls both "registry objects": they share
// the routes, the envelope (name, version, latestVersion, notes, aliases,
// sharingScope), the version protocol and the sharing settings, and differ
// only by their definition and by the display fields of a prompt.
//
// Unlike the /v1 endpoints, these bodies use the lowerCamelCase field names
// of the specification (sharingScope, createdAt): the API looks like a
// protobuf gateway, which also omits empty fields from its answers. The
// decoding below reads an absent field as its zero value.

// The base paths of the two object types of the registry.
const (
	promptsPath = "/v2/prompts"
	skillsPath  = "/v2/skills"
)

// Prompt is a prompt at one of its versions (Prompt in the specification).
// GET /v2/prompts/{prompt_id} without a query returns the latest version
// (https://docs.mistral.ai/studio/conversations/chat-completion/prompt-registry).
type Prompt struct {
	ID         string           `json:"id"`
	Name       string           `json:"name"`
	Definition PromptDefinition `json:"definition"`
	// Version is the version this answer shows; LatestVersion the newest one.
	Version       int64     `json:"version"`
	LatestVersion int64     `json:"latestVersion"`
	Notes         string    `json:"notes"`
	SharingScope  string    `json:"sharingScope"`
	Title         string    `json:"title"`
	Description   string    `json:"description"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// PromptDefinition is the versioned content of a prompt: a template with
// {{variable}} placeholders, and the variables it uses.
type PromptDefinition struct {
	Content string `json:"content"`
	// Variables is always sent, [] when empty: the specification types it
	// as an array, not nullable.
	Variables []PromptVariable `json:"variables"`
}

// PromptVariable is one variable of a prompt template.
type PromptVariable struct {
	Name string `json:"name"`
}

// PromptRequest is the body of a create (CreatePromptRequest). The
// definition, with Notes, becomes the first version.
type PromptRequest struct {
	Name              string           `json:"name"`
	Definition        PromptDefinition `json:"definition"`
	Title             *string          `json:"title"`
	Description       *string          `json:"description"`
	Notes             *string          `json:"notes"`
	SharingScope      string           `json:"sharingScope"`
	WorkspaceRelation *string          `json:"workspaceRelation"`
}

// PromptUpdate is the body of an update of the prompt itself, not of its
// versions (UpdatePromptRequest). Title and Description are plain strings:
// "" clears them, since a null may mean "leave unchanged" in a body without
// an update mask.
type PromptUpdate struct {
	Title             string  `json:"title"`
	Description       string  `json:"description"`
	SharingScope      string  `json:"sharingScope"`
	WorkspaceRelation *string `json:"workspaceRelation"`
}

// PromptVersionRequest is the body of a new version
// (CreatePromptVersionRequest). It never carries aliases: the provider does
// not manage them.
type PromptVersionRequest struct {
	Definition PromptDefinition `json:"definition"`
	Notes      *string          `json:"notes"`
}

// Skill is a skill at one of its versions (Skill in the specification). GET
// /v2/skills/{skill_id} without a query returns the latest version, as for
// a prompt.
type Skill struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	Definition    SkillDefinition `json:"definition"`
	Version       int64           `json:"version"`
	LatestVersion int64           `json:"latestVersion"`
	Notes         string          `json:"notes"`
	SharingScope  string          `json:"sharingScope"`
	CreatedAt     time.Time       `json:"createdAt"`
	UpdatedAt     time.Time       `json:"updatedAt"`
}

// SkillDefinition is the versioned content of a skill. Every field is sent,
// empty included: none is nullable in the specification. The assets travel
// inline in the JSON body; the specification has no multipart or archive
// upload for skills.
type SkillDefinition struct {
	// Description is the model-facing description that triggers the skill.
	Description string `json:"description"`
	Body        string `json:"body"`
	// Assets maps a file name to its content.
	Assets map[string]SkillAsset `json:"assets"`
}

// SkillAsset is one file of a skill (SkillAssetContent), a union of
// RawContent, base64-encoded bytes, and TextContent. Both are kept as
// strings: the provider sends the base64 text it was given and compares the
// decoded bytes of the answer, whatever base64 alphabet the API returns.
type SkillAsset struct {
	RawContent   *string `json:"rawContent,omitempty"`
	TextContent  *string `json:"textContent,omitempty"`
	IsExecutable bool    `json:"isExecutable"`
}

// SkillRequest is the body of a create (CreateSkillRequest). The definition,
// with Notes, becomes the first version.
type SkillRequest struct {
	Name              string          `json:"name"`
	Definition        SkillDefinition `json:"definition"`
	Notes             *string         `json:"notes"`
	SharingScope      string          `json:"sharingScope"`
	WorkspaceRelation *string         `json:"workspaceRelation"`
}

// SkillUpdate is the body of an update of the skill itself
// (UpdateSkillRequest): its sharing settings only.
type SkillUpdate struct {
	SharingScope      string  `json:"sharingScope"`
	WorkspaceRelation *string `json:"workspaceRelation"`
}

// SkillVersionRequest is the body of a new version
// (CreateSkillVersionRequest), without aliases.
type SkillVersionRequest struct {
	Definition SkillDefinition `json:"definition"`
	Notes      *string         `json:"notes"`
}

// CreatedVersion answers a new version (CreatePromptVersionResponse,
// CreateSkillVersionResponse). Deduplicated is true when the API matched the
// definition to a version it already holds instead of creating one; which
// versions it compares with is not documented.
type CreatedVersion struct {
	Version      int64 `json:"version"`
	Deduplicated bool  `json:"deduplicated"`
}

// versionNotes is the body of an update of a version's metadata
// (UpdatePromptVersionRequest, UpdateSkillVersionRequest). It leaves out
// aliases, an AliasList whose absence keeps the version's aliases unchanged.
type versionNotes struct {
	Notes string `json:"notes"`
}

// CreatePrompt creates a prompt at its first version: POST /v2/prompts. It
// is not idempotent.
func (cl *Client) CreatePrompt(ctx context.Context, req PromptRequest) (*Prompt, error) {
	req.Definition = withVariables(req.Definition)
	return decodeCall[Prompt](ctx, cl, call{method: http.MethodPost, path: promptsPath, body: req, audience: workspaceKey})
}

// GetPrompt returns the latest version of the prompt: GET
// /v2/prompts/{prompt_id}.
func (cl *Client) GetPrompt(ctx context.Context, id string) (*Prompt, error) {
	return decodeCall[Prompt](ctx, cl, call{method: http.MethodGet, path: escapePath(promptsPath, id), audience: workspaceKey})
}

// UpdatePrompt sets the display fields and the sharing of the prompt, which
// no version holds: PATCH /v2/prompts/{prompt_id}. It creates no version.
func (cl *Client) UpdatePrompt(ctx context.Context, id string, req PromptUpdate) (*Prompt, error) {
	return decodeCall[Prompt](ctx, cl, call{method: http.MethodPatch, path: escapePath(promptsPath, id), body: req, audience: workspaceKey})
}

// DeletePrompt deletes the prompt and its versions: DELETE
// /v2/prompts/{prompt_id}.
func (cl *Client) DeletePrompt(ctx context.Context, id string) error {
	return cl.do(ctx, call{method: http.MethodDelete, path: escapePath(promptsPath, id), audience: workspaceKey}, nil)
}

// CreatePromptVersion adds a version to the prompt: POST
// /v2/prompts/{prompt_id}/versions. It is not idempotent: a replay could add
// another version.
func (cl *Client) CreatePromptVersion(ctx context.Context, id string, req PromptVersionRequest) (*CreatedVersion, error) {
	req.Definition = withVariables(req.Definition)
	return decodeCall[CreatedVersion](ctx, cl, call{method: http.MethodPost, path: escapePath(promptsPath, id, "versions"), body: req, audience: workspaceKey})
}

// SetPromptVersionNotes replaces the notes of a version, "" to clear them,
// and keeps its aliases: PATCH /v2/prompts/{prompt_id}/versions/{version}.
func (cl *Client) SetPromptVersionNotes(ctx context.Context, id string, version int64, notes string) (*Prompt, error) {
	return decodeCall[Prompt](ctx, cl, call{method: http.MethodPatch, path: versionPath(promptsPath, id, version), body: versionNotes{notes}, audience: workspaceKey})
}

// CreateSkill creates a skill at its first version: POST /v2/skills. It is
// not idempotent.
func (cl *Client) CreateSkill(ctx context.Context, req SkillRequest) (*Skill, error) {
	req.Definition = withAssets(req.Definition)
	return decodeCall[Skill](ctx, cl, call{method: http.MethodPost, path: skillsPath, body: req, audience: workspaceKey})
}

// GetSkill returns the latest version of the skill: GET /v2/skills/{skill_id}.
func (cl *Client) GetSkill(ctx context.Context, id string) (*Skill, error) {
	return decodeCall[Skill](ctx, cl, call{method: http.MethodGet, path: escapePath(skillsPath, id), audience: workspaceKey})
}

// UpdateSkill sets the sharing of the skill: PATCH /v2/skills/{skill_id}. It
// creates no version.
func (cl *Client) UpdateSkill(ctx context.Context, id string, req SkillUpdate) (*Skill, error) {
	return decodeCall[Skill](ctx, cl, call{method: http.MethodPatch, path: escapePath(skillsPath, id), body: req, audience: workspaceKey})
}

// DeleteSkill deletes the skill and its versions: DELETE /v2/skills/{skill_id}.
func (cl *Client) DeleteSkill(ctx context.Context, id string) error {
	return cl.do(ctx, call{method: http.MethodDelete, path: escapePath(skillsPath, id), audience: workspaceKey}, nil)
}

// CreateSkillVersion adds a version to the skill: POST
// /v2/skills/{skill_id}/versions. It is not idempotent.
func (cl *Client) CreateSkillVersion(ctx context.Context, id string, req SkillVersionRequest) (*CreatedVersion, error) {
	req.Definition = withAssets(req.Definition)
	return decodeCall[CreatedVersion](ctx, cl, call{method: http.MethodPost, path: escapePath(skillsPath, id, "versions"), body: req, audience: workspaceKey})
}

// SetSkillVersionNotes replaces the notes of a version, "" to clear them, and
// keeps its aliases: PATCH /v2/skills/{skill_id}/versions/{version}.
func (cl *Client) SetSkillVersionNotes(ctx context.Context, id string, version int64, notes string) (*Skill, error) {
	return decodeCall[Skill](ctx, cl, call{method: http.MethodPatch, path: versionPath(skillsPath, id, version), body: versionNotes{notes}, audience: workspaceKey})
}

// decodeCall sends c and returns its JSON answer decoded as a T.
func decodeCall[T any](ctx context.Context, cl *Client, c call) (*T, error) {
	var out T
	if err := cl.do(ctx, c, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// versionPath is the path of one version of a registry object:
// <base>/<id>/versions/<version>.
func versionPath(base, id string, version int64) string {
	return escapePath(base, id, "versions", strconv.FormatInt(version, 10))
}

// withVariables sends an empty variables array rather than null: the
// specification types it as an array, not nullable.
func withVariables(d PromptDefinition) PromptDefinition {
	if d.Variables == nil {
		d.Variables = []PromptVariable{}
	}
	return d
}

// withAssets sends an empty assets object rather than null: the
// specification types it as an object, not nullable.
func withAssets(d SkillDefinition) SkillDefinition {
	if d.Assets == nil {
		d.Assets = map[string]SkillAsset{}
	}
	return d
}
