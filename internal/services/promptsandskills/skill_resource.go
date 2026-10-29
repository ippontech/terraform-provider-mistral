// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package promptsandskills

import (
	"context"
	"maps"

	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
	"github.com/ippontech/terraform-provider-mistral/internal/providerdata"
)

// The interfaces SkillResource implements, checked at compile time.
var (
	_ resource.ResourceWithConfigure      = &SkillResource{}
	_ resource.ResourceWithImportState    = &SkillResource{}
	_ resource.ResourceWithIdentity       = &SkillResource{}
	_ resource.ResourceWithValidateConfig = &SkillResource{}
)

// NewSkillResource returns the mistral_skill resource.
func NewSkillResource() resource.Resource {
	return &SkillResource{}
}

// SkillResource manages a skill of the registry: /v2/skills
// (https://docs.mistral.ai/api/endpoint/beta/skills), with its versions
// folded into it as for a prompt.
type SkillResource struct {
	client *client.Client
}

// skillLayout splits the attributes of a skill between its versions
// (SkillDefinition) and the skill itself (UpdateSkillRequest, which takes
// the sharing settings only).
var skillLayout = layout{
	definition: []path.Path{path.Root("description"), path.Root("body"), path.Root("assets")},
	parent:     []path.Path{path.Root("sharing_scope"), path.Root("workspace_relation")},
}

// skillModel is the state of mistral_skill.
type skillModel struct {
	ID                types.String          `tfsdk:"id"`
	Name              types.String          `tfsdk:"name"`
	Description       types.String          `tfsdk:"description"`
	Body              types.String          `tfsdk:"body"`
	Assets            map[string]assetModel `tfsdk:"assets"`
	Notes             types.String          `tfsdk:"notes"`
	SharingScope      types.String          `tfsdk:"sharing_scope"`
	WorkspaceRelation types.String          `tfsdk:"workspace_relation"`
	Version           types.Int64           `tfsdk:"version"`
	CreatedAt         types.String          `tfsdk:"created_at"`
	UpdatedAt         types.String          `tfsdk:"updated_at"`
}

// assetModel is one file of a skill: its text in content, or its bytes in
// content_base64.
type assetModel struct {
	Content       types.String `tfsdk:"content"`
	ContentBase64 types.String `tfsdk:"content_base64"`
	Executable    types.Bool   `tfsdk:"executable"`
}

// Metadata names the resource mistral_skill.
func (r *SkillResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_skill"
}

// IdentitySchema declares the skill ID as the identity.
func (r *SkillResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = idIdentitySchema("The skill ID.")
}

// Schema describes mistral_skill.
func (r *SkillResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	attributes := map[string]schema.Attribute{
		"id": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "The skill ID.",
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
		"name": nameAttribute("skill"),
		"description": schema.StringAttribute{
			Optional:            true,
			MarkdownDescription: "The description the model reads to decide when and how to use the skill. Changing it creates a new version.",
			Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
		},
		"body": schema.StringAttribute{
			Optional:            true,
			MarkdownDescription: "The body of the skill: the instructions the model follows when it uses the skill. Changing it creates a new version.",
			Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
		},
		"assets": schema.MapNestedAttribute{
			Optional: true,
			MarkdownDescription: "The additional files of the skill, by file name. Each one sets either `content` or `content_base64`. " +
				"Changing them creates a new version. The state holds their content.",
			Validators: []validator.Map{
				mapvalidator.SizeAtLeast(1),
				mapvalidator.KeysAre(stringvalidator.LengthAtLeast(1)),
			},
			NestedObject: schema.NestedAttributeObject{
				Attributes: map[string]schema.Attribute{
					"content": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "The content of a text file, such as `file(\"scripts/extract.py\")`.",
					},
					"content_base64": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "The content of a binary file, base64-encoded, such as `filebase64(\"assets/logo.png\")`.",
						Validators:          []validator.String{base64String{}},
					},
					"executable": schema.BoolAttribute{
						Optional:            true,
						Computed:            true,
						Default:             booldefault.StaticBool(false),
						MarkdownDescription: "Whether the file is executable. Defaults to `false`.",
					},
				},
			},
		},
		"notes": notesAttribute("description, body or assets"),
		"version": schema.Int64Attribute{
			Computed:            true,
			MarkdownDescription: versionAttributeDescription("skill", "`description`, `body` or `assets`"),
			PlanModifiers:       []planmodifier.Int64{versionPlan{definition: skillLayout.definition}},
		},
		"created_at": createdAtAttribute("skill"),
		"updated_at": updatedAtAttribute("skill"),
	}
	maps.Copy(attributes, sharingAttributes("skill"))
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a skill of the workspace of the API key: versioned instructions and files that a model loads for a task.",
		Attributes:          attributes,
	}
}

// Configure takes the client of the standard key.
func (r *SkillResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerdata.Configure(req.ProviderData, providerdata.Workspace, &resp.Diagnostics)
}

// ValidateConfig checks the sharing settings and the union of each asset.
func (r *SkillResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	validateSharing(ctx, req.Config, &resp.Diagnostics)
	validateAssets(ctx, req.Config, &resp.Diagnostics)
}

// Create creates the skill with its first version.
func (r *SkillResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan skillModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	s, err := r.client.CreateSkill(ctx, client.SkillRequest{
		Name:              plan.Name.ValueString(),
		Definition:        plan.definition(),
		Notes:             plan.Notes.ValueStringPointer(),
		SharingScope:      plan.SharingScope.ValueString(),
		WorkspaceRelation: relationRequest(plan.SharingScope, plan.WorkspaceRelation),
	})
	if err != nil {
		addCreateError(&resp.Diagnostics, "skill", plan.Name.ValueString(), err)
		return
	}
	resp.Diagnostics.Append(setSkillState(ctx, *s, &plan, &resp.State, resp.Identity)...)
}

// Read reads the latest version of the skill.
func (r *SkillResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state skillModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	s, err := r.client.GetSkill(ctx, state.ID.ValueString())
	if client.IsNotFound(err) {
		removeGone(ctx, resp, state.ID)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot read the skill", err.Error())
		return
	}
	resp.Diagnostics.Append(setSkillState(ctx, *s, &state, &resp.State, resp.Identity)...)
}

// Update creates a version for a new definition, updates the notes of the
// latest version for new notes alone, and updates the sharing of the skill,
// in the order versionedUpdate.apply documents.
func (r *SkillResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state skillModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	changes, diags := skillLayout.changesOf(ctx, req.Plan, req.State)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, version := state.ID.ValueString(), state.Version.ValueInt64()
	s := versionedUpdate[client.Skill]{
		noun:    "skill",
		changes: changes,
		version: version,
		newVersion: func(ctx context.Context) (*client.CreatedVersion, error) {
			return r.client.CreateSkillVersion(ctx, id, client.SkillVersionRequest{Definition: plan.definition(), Notes: plan.Notes.ValueStringPointer()})
		},
		setNotes: func(ctx context.Context) (*client.Skill, error) {
			return r.client.SetSkillVersionNotes(ctx, id, version, plan.Notes.ValueString())
		},
		updateParent: func(ctx context.Context) (*client.Skill, error) {
			return r.client.UpdateSkill(ctx, id, client.SkillUpdate{
				SharingScope:      plan.SharingScope.ValueString(),
				WorkspaceRelation: relationRequest(plan.SharingScope, plan.WorkspaceRelation),
			})
		},
		get:       func(ctx context.Context) (*client.Skill, error) { return r.client.GetSkill(ctx, id) },
		versionOf: func(s *client.Skill) int64 { return s.Version },
	}.apply(ctx, &resp.Diagnostics)
	if s == nil {
		return
	}
	resp.Diagnostics.Append(setSkillState(ctx, *s, &plan, &resp.State, resp.Identity)...)
}

// Delete deletes the skill and its versions.
func (r *SkillResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var id types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteSkill(ctx, id.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Cannot delete the skill", err.Error())
	}
}

// ImportState imports a skill by its ID, or by its identity.
func (r *SkillResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughWithIdentity(ctx, path.Root("id"), path.Root("id"), req, resp)
}

// setSkillState writes the skill and its identity. prior is the plan or
// state the call started from.
func setSkillState(ctx context.Context, s client.Skill, prior *skillModel, state *tfsdk.State, identity *tfsdk.ResourceIdentity) diag.Diagnostics {
	m := skillState(s, prior)
	diags := state.Set(ctx, &m)
	if identity != nil {
		diags.Append(identity.Set(ctx, idIdentity{ID: m.ID})...)
	}
	return diags
}
