// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/identityschema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
	"github.com/ippontech/terraform-provider-mistral/internal/providerdata"
)

var (
	_ resource.ResourceWithConfigure   = &AgentAliasResource{}
	_ resource.ResourceWithImportState = &AgentAliasResource{}
	_ resource.ResourceWithIdentity    = &AgentAliasResource{}
)

// NewAgentAliasResource returns the mistral_agent_alias resource.
func NewAgentAliasResource() resource.Resource {
	return &AgentAliasResource{}
}

// AgentAliasResource manages an alias that names a version of an agent:
// /v1/agents/{agent_id}/aliases, which has no read by name, so Read lists the
// aliases of the agent.
type AgentAliasResource struct {
	client *client.Client
}

// noColon is an agent ID: the import ID joins it to the alias with a colon.
var noColon = regexp.MustCompile(`^[^:]+$`)

type agentAliasModel struct {
	ID        types.String `tfsdk:"id"`
	AgentID   types.String `tfsdk:"agent_id"`
	Alias     types.String `tfsdk:"alias"`
	Version   types.Int64  `tfsdk:"version"`
	CreatedAt types.String `tfsdk:"created_at"`
	UpdatedAt types.String `tfsdk:"updated_at"`
}

type agentAliasIdentity struct {
	AgentID types.String `tfsdk:"agent_id"`
	Alias   types.String `tfsdk:"alias"`
}

func (r *AgentAliasResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_agent_alias"
}

func (r *AgentAliasResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = identityschema.Schema{
		Attributes: map[string]identityschema.Attribute{
			"agent_id": identityschema.StringAttribute{RequiredForImport: true, Description: "The agent ID."},
			"alias":    identityschema.StringAttribute{RequiredForImport: true, Description: "The name of the alias."},
		},
	}
}

func (r *AgentAliasResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Points an alias of an agent at one of its versions, so that a conversation started with the alias uses that version.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "`<agent_id>:<alias>`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"agent_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the agent. Changing it replaces the alias.",
				PlanModifiers:       replace,
				Validators:          []validator.String{stringvalidator.RegexMatches(noColon, "must not be empty or contain a colon")},
			},
			"alias": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The name of the alias, unique per agent, such as `production`. Changing it replaces the alias.",
				PlanModifiers:       replace,
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"version": schema.Int64Attribute{
				Required:            true,
				MarkdownDescription: "The version of the agent the alias points to, such as `mistral_agent.this.version`.",
				Validators:          []validator.Int64{int64validator.AtLeast(0)},
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "When the alias was created, in RFC 3339 format.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "When the alias was last pointed at a version, in RFC 3339 format.",
			},
		},
	}
}

func (r *AgentAliasResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerdata.Configure(req.ProviderData, providerdata.Workspace, &resp.Diagnostics)
}

func (r *AgentAliasResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan agentAliasModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	agentID, alias := plan.AgentID.ValueString(), plan.Alias.ValueString()

	// The PUT is an upsert: without this check, a create would take over an
	// alias that exists outside Terraform.
	existing, err := r.find(ctx, agentID, alias)
	if err != nil {
		resp.Diagnostics.AddError("Cannot create the agent alias", err.Error())
		return
	}
	if existing != nil {
		resp.Diagnostics.AddAttributeError(path.Root("alias"), "Agent alias already exists",
			fmt.Sprintf("The agent %q already has an alias %q, on version %d. Import it with an import block (id = %q) to manage it, "+
				"or choose another name.", agentID, alias, existing.Version, agentID+":"+alias))
		return
	}
	r.put(ctx, plan, &resp.State, resp.Identity, &resp.Diagnostics)
}

func (r *AgentAliasResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state agentAliasModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	found, err := r.find(ctx, state.AgentID.ValueString(), state.Alias.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot read the agent alias", err.Error())
		return
	}
	if found == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(setAliasState(ctx, state.AgentID.ValueString(), *found, &resp.State, resp.Identity)...)
}

func (r *AgentAliasResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan agentAliasModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.put(ctx, plan, &resp.State, resp.Identity, &resp.Diagnostics)
}

func (r *AgentAliasResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state agentAliasModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteAgentAlias(ctx, state.AgentID.ValueString(), state.Alias.ValueString())
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Cannot delete the agent alias", err.Error())
	}
}

func (r *AgentAliasResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	var agentID, alias string
	if req.ID != "" {
		var ok bool
		agentID, alias, ok = strings.Cut(req.ID, ":")
		if !ok || agentID == "" || alias == "" {
			resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("The ID must be <agent_id>:<alias>, got %q.", req.ID))
			return
		}
	} else {
		var id agentAliasIdentity
		resp.Diagnostics.Append(req.Identity.Get(ctx, &id)...)
		if resp.Diagnostics.HasError() {
			return
		}
		agentID, alias = id.AgentID.ValueString(), id.Alias.ValueString()
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("agent_id"), agentID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("alias"), alias)...)
}

// find returns the alias of the agent, nil when the agent has none by that
// name, and a 404 error when the agent does not exist.
func (r *AgentAliasResource) find(ctx context.Context, agentID, alias string) (*client.AgentAlias, error) {
	aliases, err := r.client.ListAgentAliases(ctx, agentID)
	if err != nil {
		return nil, err
	}
	for _, a := range aliases {
		if a.Alias == alias {
			return &a, nil
		}
	}
	return nil, nil
}

func (r *AgentAliasResource) put(ctx context.Context, plan agentAliasModel, state *tfsdk.State, identity *tfsdk.ResourceIdentity, diags *diag.Diagnostics) {
	agentID := plan.AgentID.ValueString()
	got, err := r.client.PutAgentAlias(ctx, agentID, plan.Alias.ValueString(), plan.Version.ValueInt64())
	if err != nil {
		diags.AddError("Cannot point the agent alias at the version", err.Error())
		return
	}
	diags.Append(setAliasState(ctx, agentID, *got, state, identity)...)
}

func setAliasState(ctx context.Context, agentID string, a client.AgentAlias, state *tfsdk.State, identity *tfsdk.ResourceIdentity) diag.Diagnostics {
	m := agentAliasModel{
		ID:        types.StringValue(agentID + ":" + a.Alias),
		AgentID:   types.StringValue(agentID),
		Alias:     types.StringValue(a.Alias),
		Version:   types.Int64Value(a.Version),
		CreatedAt: timestamp(a.CreatedAt),
		UpdatedAt: timestamp(a.UpdatedAt),
	}
	diags := state.Set(ctx, &m)
	if identity != nil {
		diags.Append(identity.Set(ctx, agentAliasIdentity{AgentID: m.AgentID, Alias: m.Alias})...)
	}
	return diags
}
