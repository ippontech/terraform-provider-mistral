// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package promptsandskills

import (
	"context"
	"maps"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
	"github.com/ippontech/terraform-provider-mistral/internal/providerdata"
)

// The interfaces PromptResource implements, checked at compile time.
var (
	_ resource.ResourceWithConfigure      = &PromptResource{}
	_ resource.ResourceWithImportState    = &PromptResource{}
	_ resource.ResourceWithIdentity       = &PromptResource{}
	_ resource.ResourceWithValidateConfig = &PromptResource{}
)

// NewPromptResource returns the mistral_prompt resource.
func NewPromptResource() resource.Resource {
	return &PromptResource{}
}

// PromptResource manages a prompt of the registry: /v2/prompts
// (https://docs.mistral.ai/api/endpoint/beta/prompts). Its versions are
// folded into it: versions cannot be deleted, so there is no version
// resource, and version is the latest one.
type PromptResource struct {
	client *client.Client
}

// promptLayout splits the attributes of a prompt between its versions
// (PromptDefinition) and the prompt itself (UpdatePromptRequest).
var promptLayout = layout{
	definition: []path.Path{path.Root("content"), path.Root("variables")},
	parent:     []path.Path{path.Root("title"), path.Root("description"), path.Root("sharing_scope"), path.Root("workspace_relation")},
}

// promptModel is the state of mistral_prompt.
type promptModel struct {
	ID                types.String    `tfsdk:"id"`
	Name              types.String    `tfsdk:"name"`
	Title             types.String    `tfsdk:"title"`
	Description       types.String    `tfsdk:"description"`
	Content           types.String    `tfsdk:"content"`
	Variables         []variableModel `tfsdk:"variables"`
	Notes             types.String    `tfsdk:"notes"`
	SharingScope      types.String    `tfsdk:"sharing_scope"`
	WorkspaceRelation types.String    `tfsdk:"workspace_relation"`
	Version           types.Int64     `tfsdk:"version"`
	CreatedAt         types.String    `tfsdk:"created_at"`
	UpdatedAt         types.String    `tfsdk:"updated_at"`
}

// variableModel is one variable of the template. It is an object rather
// than a string, as PromptVariable is: a field the API adds to it later
// then needs no change of type.
type variableModel struct {
	Name types.String `tfsdk:"name"`
}

// Metadata names the resource mistral_prompt.
func (r *PromptResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_prompt"
}

// IdentitySchema declares the prompt ID as the identity.
func (r *PromptResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = idIdentitySchema("The prompt ID.")
}

// Schema describes mistral_prompt.
func (r *PromptResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	attributes := map[string]schema.Attribute{
		"id": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "The prompt ID.",
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
		"name": nameAttribute("prompt"),
		"title": schema.StringAttribute{
			Optional:            true,
			MarkdownDescription: "The display title of the prompt. Changing it creates no version.",
			Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
		},
		"description": schema.StringAttribute{
			Optional:            true,
			MarkdownDescription: "The display description of the prompt. Changing it creates no version.",
			Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
		},
		"content": schema.StringAttribute{
			Required:            true,
			MarkdownDescription: "The prompt template, with a `{{name}}` placeholder for each variable. Changing it creates a new version.",
			Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
		},
		"variables": schema.ListNestedAttribute{
			Optional:            true,
			MarkdownDescription: "The variables of the template, such as `{ name = \"language\" }` for `{{language}}`. Changing them creates a new version.",
			Validators:          []validator.List{listvalidator.SizeAtLeast(1)},
			NestedObject: schema.NestedAttributeObject{
				Attributes: map[string]schema.Attribute{
					"name": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "The name of the variable, without the braces.",
						Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
					},
				},
			},
		},
		"notes": notesAttribute("template or its variables"),
		"version": schema.Int64Attribute{
			Computed:            true,
			MarkdownDescription: versionAttributeDescription("prompt", "`content` or `variables`"),
			PlanModifiers:       []planmodifier.Int64{versionPlan{definition: promptLayout.definition}},
		},
		"created_at": createdAtAttribute("prompt"),
		"updated_at": updatedAtAttribute("prompt"),
	}
	maps.Copy(attributes, sharingAttributes("prompt"))
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a prompt of the workspace of the API key: a versioned template that applications fetch by name, ID, version or alias.",
		Attributes:          attributes,
	}
}

// Configure takes the client of the standard key.
func (r *PromptResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerdata.Configure(req.ProviderData, providerdata.Workspace, &resp.Diagnostics)
}

// ValidateConfig checks the sharing settings.
func (r *PromptResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	validateSharing(ctx, req.Config, &resp.Diagnostics)
}

// Create creates the prompt with its first version.
func (r *PromptResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan promptModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := r.client.CreatePrompt(ctx, plan.createRequest())
	if err != nil {
		addCreateError(&resp.Diagnostics, "prompt", plan.Name.ValueString(), err)
		return
	}
	resp.Diagnostics.Append(setPromptState(ctx, *p, &plan, &resp.State, resp.Identity)...)
}

// Read reads the latest version of the prompt.
func (r *PromptResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state promptModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := r.client.GetPrompt(ctx, state.ID.ValueString())
	if client.IsNotFound(err) {
		removeGone(ctx, resp, state.ID)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot read the prompt", err.Error())
		return
	}
	resp.Diagnostics.Append(setPromptState(ctx, *p, &state, &resp.State, resp.Identity)...)
}

// Update creates a version for a new definition, updates the notes of the
// latest version for new notes alone, and updates the prompt for new display
// fields or sharing, in the order versionedUpdate.apply documents.
func (r *PromptResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state promptModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	changes, diags := promptLayout.changesOf(ctx, req.Plan, req.State)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	id, version := state.ID.ValueString(), state.Version.ValueInt64()
	p := versionedUpdate[client.Prompt]{
		noun:    "prompt",
		changes: changes,
		version: version,
		newVersion: func(ctx context.Context) (*client.CreatedVersion, error) {
			return r.client.CreatePromptVersion(ctx, id, client.PromptVersionRequest{Definition: plan.definition(), Notes: plan.Notes.ValueStringPointer()})
		},
		setNotes: func(ctx context.Context) (*client.Prompt, error) {
			return r.client.SetPromptVersionNotes(ctx, id, version, plan.Notes.ValueString())
		},
		updateParent: func(ctx context.Context) (*client.Prompt, error) {
			return r.client.UpdatePrompt(ctx, id, plan.updateRequest())
		},
		get:       func(ctx context.Context) (*client.Prompt, error) { return r.client.GetPrompt(ctx, id) },
		versionOf: func(p *client.Prompt) int64 { return p.Version },
	}.apply(ctx, &resp.Diagnostics)
	if p == nil {
		return
	}
	resp.Diagnostics.Append(setPromptState(ctx, *p, &plan, &resp.State, resp.Identity)...)
}

// Delete deletes the prompt and its versions.
func (r *PromptResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var id types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeletePrompt(ctx, id.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Cannot delete the prompt", err.Error())
	}
}

// ImportState imports a prompt by its ID, or by its identity.
func (r *PromptResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughWithIdentity(ctx, path.Root("id"), path.Root("id"), req, resp)
}

// definition is the versioned content of the plan.
func (m *promptModel) definition() client.PromptDefinition {
	d := client.PromptDefinition{Content: m.Content.ValueString()}
	for _, v := range m.Variables {
		d.Variables = append(d.Variables, client.PromptVariable{Name: v.Name.ValueString()})
	}
	return d
}

// createRequest is the body of the create: the first version and the
// prompt's own fields, every one sent, null for an absent one.
func (m *promptModel) createRequest() client.PromptRequest {
	return client.PromptRequest{
		Name:              m.Name.ValueString(),
		Definition:        m.definition(),
		Title:             m.Title.ValueStringPointer(),
		Description:       m.Description.ValueStringPointer(),
		Notes:             m.Notes.ValueStringPointer(),
		SharingScope:      m.SharingScope.ValueString(),
		WorkspaceRelation: relationRequest(m.SharingScope, m.WorkspaceRelation),
	}
}

// updateRequest is the body of the update of the prompt: an absent title or
// description is sent as "", which clears it.
func (m *promptModel) updateRequest() client.PromptUpdate {
	return client.PromptUpdate{
		Title:             m.Title.ValueString(),
		Description:       m.Description.ValueString(),
		SharingScope:      m.SharingScope.ValueString(),
		WorkspaceRelation: relationRequest(m.SharingScope, m.WorkspaceRelation),
	}
}

// promptState maps a prompt to the state. prior is the plan or state the
// call started from, for workspace_relation, which the API does not return.
func promptState(p client.Prompt, prior *promptModel) promptModel {
	m := promptModel{
		ID:           types.StringValue(p.ID),
		Name:         types.StringValue(p.Name),
		Title:        nonEmpty(p.Title),
		Description:  nonEmpty(p.Description),
		Content:      types.StringValue(p.Definition.Content),
		Notes:        nonEmpty(p.Notes),
		SharingScope: scopeState(p.SharingScope),
		Version:      types.Int64Value(p.Version),
		CreatedAt:    timestamp(p.CreatedAt),
		UpdatedAt:    timestamp(p.UpdatedAt),
	}
	m.WorkspaceRelation = relationState(m.SharingScope, prior.WorkspaceRelation)
	for _, v := range p.Definition.Variables {
		m.Variables = append(m.Variables, variableModel{Name: types.StringValue(v.Name)})
	}
	return m
}

// setPromptState writes the prompt and its identity.
func setPromptState(ctx context.Context, p client.Prompt, prior *promptModel, state *tfsdk.State, identity *tfsdk.ResourceIdentity) diag.Diagnostics {
	m := promptState(p, prior)
	diags := state.Set(ctx, &m)
	if identity != nil {
		diags.Append(identity.Set(ctx, idIdentity{ID: m.ID})...)
	}
	return diags
}
