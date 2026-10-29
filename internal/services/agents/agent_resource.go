// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package agents holds mistral_agent, mistral_agent_alias and the
// mistral_agent data source: the agents of a workspace and the aliases that
// name their versions.
package agents

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/float64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/identityschema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
	"github.com/ippontech/terraform-provider-mistral/internal/providerdata"
)

var (
	_ resource.ResourceWithConfigure      = &AgentResource{}
	_ resource.ResourceWithImportState    = &AgentResource{}
	_ resource.ResourceWithIdentity       = &AgentResource{}
	_ resource.ResourceWithValidateConfig = &AgentResource{}
)

// NewAgentResource returns the mistral_agent resource.
func NewAgentResource() resource.Resource {
	return &AgentResource{}
}

// AgentResource manages an agent: /v1/agents
// (https://docs.mistral.ai/api/endpoint/beta/agents). Every update creates a
// version of the agent, which becomes its active one.
type AgentResource struct {
	client *client.Client
}

// agentIdentity is the resource identity of mistral_agent.
type agentIdentity struct {
	ID types.String `tfsdk:"id"`
}

// Tool types (the discriminator of the tools union in the specification).
const (
	toolFunction        = "function"
	toolDocumentLibrary = "document_library"
	toolConnector       = "connector"
)

var toolTypes = []string{toolFunction, "web_search", "web_search_premium", "code_interpreter", "image_generation", toolDocumentLibrary, toolConnector}

func (r *AgentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_agent"
}

func (r *AgentResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = identityschema.Schema{
		Attributes: map[string]identityschema.Attribute{
			"id": identityschema.StringAttribute{RequiredForImport: true, Description: "The agent ID."},
		},
	}
}

func (r *AgentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages an agent of the workspace of the API key. Every update creates a new version of the agent, which becomes its active version.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The agent ID, such as `ag_0684fe0e0b98773e8000323fc71a3986`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The name of the agent.",
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"model": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The model the agent runs on: a model ID or an alias, such as `mistral-medium-latest`. See the `mistral_model` data source.",
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"description": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "The description of the agent.",
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"instructions": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "The instruction prompt the model follows in every conversation with the agent.",
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"tools":           toolsAttribute(),
			"completion_args": completionArgsAttribute(),
			"handoffs": schema.ListAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "The IDs of the agents this agent can hand a conversation over to.",
				Validators:          []validator.List{listvalidator.SizeAtLeast(1)},
			},
			"metadata": schema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Free-form string metadata of the agent. A value set outside Terraform as another JSON type reads as its JSON text.",
				Validators:          []validator.Map{mapvalidator.SizeAtLeast(1)},
			},
			"version_message": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "A message describing the version that the next create or update produces. Changing it alone creates a new version.",
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"version": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "The active version of the agent: 0 after the create, then one more on each update. Point a `mistral_agent_alias` at it.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "When the agent was created, in RFC 3339 format.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "When the agent was last updated, in RFC 3339 format.",
			},
		},
	}
}

func toolsAttribute() schema.ListNestedAttribute {
	emptyTools := types.ListValueMust(types.ObjectType{AttrTypes: toolAttrTypes}, []attr.Value{})
	return schema.ListNestedAttribute{
		Optional:            true,
		Computed:            true,
		Default:             listdefault.StaticValue(emptyTools),
		MarkdownDescription: "The tools the agent can use, in order. `type` selects the tool; `function`, `library_ids` and `connector_id` belong to the `function`, `document_library` and `connector` types only.",
		NestedObject: schema.NestedAttributeObject{
			Attributes: map[string]schema.Attribute{
				"type": schema.StringAttribute{
					Required:            true,
					MarkdownDescription: "The tool: `function`, `web_search`, `web_search_premium`, `code_interpreter`, `image_generation`, `document_library` or `connector`.",
					Validators:          []validator.String{stringvalidator.OneOf(toolTypes...)},
				},
				"function": schema.SingleNestedAttribute{
					Optional:            true,
					MarkdownDescription: "The function the model can call, for a `function` tool.",
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{Required: true, MarkdownDescription: "The name of the function."},
						"description": schema.StringAttribute{
							Optional: true, Computed: true, Default: stringdefault.StaticString(defaultFunctionDesc),
							MarkdownDescription: "What the function does, for the model.",
						},
						"strict": schema.BoolAttribute{
							Optional: true, Computed: true, Default: booldefault.StaticBool(defaultFunctionStrict),
							MarkdownDescription: "Whether the model must follow `parameters` exactly. Defaults to `false`.",
						},
						"parameters": schema.StringAttribute{
							Required:            true,
							MarkdownDescription: "The JSON schema of the arguments of the function, as a JSON object: use `jsonencode`.",
							Validators:          []validator.String{jsonObject{}},
						},
					},
				},
				"library_ids": schema.ListAttribute{
					Optional:            true,
					ElementType:         types.StringType,
					MarkdownDescription: "The IDs of the libraries the agent searches, for a `document_library` tool.",
					Validators:          []validator.List{listvalidator.SizeAtLeast(1)},
				},
				"connector_id": schema.StringAttribute{
					Optional:            true,
					MarkdownDescription: "The ID of the connector, for a `connector` tool.",
				},
				"tool_configuration": schema.SingleNestedAttribute{
					Optional:            true,
					MarkdownDescription: "Which functions of the tool the agent may call, and which ones need the user's confirmation.",
					Attributes: map[string]schema.Attribute{
						"include":               nonEmptyStringList("The only functions of the tool the agent may call."),
						"exclude":               nonEmptyStringList("The functions of the tool the agent may not call."),
						"requires_confirmation": nonEmptyStringList("The functions of the tool that need the user's confirmation before each call."),
					},
				},
			},
		},
	}
}

func completionArgsAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Optional:            true,
		Computed:            true,
		Default:             objectdefault.StaticValue(defaultCompletionArgs()),
		MarkdownDescription: "The sampling arguments of the agent's responses. A conversation can override them.",
		Attributes: map[string]schema.Attribute{
			"stop": nonEmptyStringList("The sequences that stop the generation."),
			"presence_penalty": schema.Float64Attribute{
				Optional: true, MarkdownDescription: "Penalizes tokens that already appear in the response, from -2 to 2.",
				Validators: []validator.Float64{float64validator.Between(-2, 2)},
			},
			"frequency_penalty": schema.Float64Attribute{
				Optional: true, MarkdownDescription: "Penalizes tokens by how often they appear in the response, from -2 to 2.",
				Validators: []validator.Float64{float64validator.Between(-2, 2)},
			},
			"temperature": schema.Float64Attribute{
				Optional: true, MarkdownDescription: "The sampling temperature. When null, the model's default applies (see `default_temperature` of `mistral_model`).",
				Validators: []validator.Float64{float64validator.AtLeast(0)},
			},
			"top_p": schema.Float64Attribute{
				Optional: true, MarkdownDescription: "Nucleus sampling: the probability mass of the tokens considered, from 0 to 1.",
				Validators: []validator.Float64{float64validator.Between(0, 1)},
			},
			"max_tokens": schema.Int64Attribute{
				Optional: true, MarkdownDescription: "The maximum number of tokens of a response.",
				Validators: []validator.Int64{int64validator.AtLeast(1)},
			},
			"random_seed": schema.Int64Attribute{
				Optional: true, MarkdownDescription: "The seed of the random sampling, for reproducible responses.",
				Validators: []validator.Int64{int64validator.AtLeast(0)},
			},
			"response_format": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "The format of the responses. When null, the agent answers in text.",
				Attributes: map[string]schema.Attribute{
					"type": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "`text`, `json_object`, or `json_schema` with `json_schema`.",
						Validators:          []validator.String{stringvalidator.OneOf("text", "json_object", "json_schema")},
					},
					"json_schema": schema.SingleNestedAttribute{
						Optional:            true,
						MarkdownDescription: "The schema the responses follow, for the `json_schema` type.",
						Attributes: map[string]schema.Attribute{
							"name": schema.StringAttribute{Required: true, MarkdownDescription: "The name of the schema."},
							"description": schema.StringAttribute{
								Optional: true, MarkdownDescription: "The description of the schema.",
								Validators: []validator.String{stringvalidator.LengthAtLeast(1)},
							},
							"schema": schema.StringAttribute{
								Required:            true,
								MarkdownDescription: "The JSON schema, as a JSON object: use `jsonencode`.",
								Validators:          []validator.String{jsonObject{}},
							},
							"strict": schema.BoolAttribute{
								Optional: true, Computed: true, Default: booldefault.StaticBool(defaultSchemaStrict),
								MarkdownDescription: "Whether the responses must follow the schema exactly. Defaults to `false`.",
							},
						},
					},
				},
			},
			"tool_choice": schema.StringAttribute{
				Optional: true, Computed: true, Default: stringdefault.StaticString(defaultToolChoice),
				MarkdownDescription: "Whether the model calls tools: `auto` (the default), `none`, `any` or `required`.",
				Validators:          []validator.String{stringvalidator.OneOf("auto", "none", "any", "required")},
			},
			"reasoning_effort": schema.StringAttribute{
				Optional: true, MarkdownDescription: "The reasoning effort of a reasoning model: `none`, `minimal`, `low`, `medium`, `high` or `xhigh`.",
				Validators: []validator.String{stringvalidator.OneOf("none", "minimal", "low", "medium", "high", "xhigh")},
			},
		},
	}
}

func nonEmptyStringList(desc string) schema.ListAttribute {
	return schema.ListAttribute{
		Optional:            true,
		ElementType:         types.StringType,
		MarkdownDescription: desc,
		Validators:          []validator.List{listvalidator.SizeAtLeast(1)},
	}
}

func (r *AgentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerdata.Configure(req.ProviderData, providerdata.Workspace, &resp.Diagnostics)
}

func (r *AgentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan agentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	agent, err := r.client.CreateAgent(ctx, plan.request())
	if err != nil {
		resp.Diagnostics.AddError("Cannot create the agent", err.Error())
		return
	}
	resp.Diagnostics.Append(setAgentState(ctx, *agent, &plan, &resp.State, resp.Identity)...)
}

func (r *AgentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state agentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	agent, err := r.client.GetAgent(ctx, state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot read the agent", err.Error())
		return
	}
	resp.Diagnostics.Append(setAgentState(ctx, *agent, &state, &resp.State, resp.Identity)...)
}

func (r *AgentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state agentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	agent, err := r.client.UpdateAgent(ctx, state.ID.ValueString(), plan.request())
	if err != nil {
		resp.Diagnostics.AddError("Cannot update the agent", err.Error())
		return
	}
	resp.Diagnostics.Append(setAgentState(ctx, *agent, &plan, &resp.State, resp.Identity)...)
}

func (r *AgentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var id types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteAgent(ctx, id.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Cannot delete the agent", err.Error())
	}
}

func (r *AgentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughWithIdentity(ctx, path.Root("id"), path.Root("id"), req, resp)
}

// setAgentState writes the agent and its identity. prior is the plan or
// state the call started from.
func setAgentState(ctx context.Context, a client.Agent, prior *agentModel, state *tfsdk.State, identity *tfsdk.ResourceIdentity) diag.Diagnostics {
	m := agentState(a, prior)
	diags := state.Set(ctx, &m)
	if identity != nil {
		diags.Append(identity.Set(ctx, agentIdentity{ID: m.ID})...)
	}
	return diags
}

// jsonObject validates that a string holds a JSON object.
type jsonObject struct{}

func (jsonObject) Description(context.Context) string {
	return "value must be a JSON object"
}

func (v jsonObject) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }

func (jsonObject) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(req.ConfigValue.ValueString()), &obj); err != nil || obj == nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid JSON object",
			fmt.Sprintf("The value must be a JSON object, such as the result of jsonencode({ type = \"object\" }): %v", err))
	}
}
