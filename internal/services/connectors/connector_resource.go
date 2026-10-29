// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package connectors holds mistral_connector, mistral_connector_credential
// and the mistral_connector data source: the MCP and HTTP connectors of a
// workspace, which agents call as tools, and the credentials they call them
// with.
package connectors

import (
	"context"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/identityschema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
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
	_ resource.ResourceWithConfigure   = &ConnectorResource{}
	_ resource.ResourceWithImportState = &ConnectorResource{}
	_ resource.ResourceWithIdentity    = &ConnectorResource{}
)

// NewConnectorResource returns the mistral_connector resource.
func NewConnectorResource() resource.Resource {
	return &ConnectorResource{}
}

// ConnectorResource manages a connector: /v1/connectors
// (https://docs.mistral.ai/api/endpoint/beta/connectors). Its visibility is
// not a field of the update: the share calls change it.
type ConnectorResource struct {
	client *client.Client
}

// connectorModel is the state of mistral_connector.
type connectorModel struct {
	ID           types.String `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	Title        types.String `tfsdk:"title"`
	Description  types.String `tfsdk:"description"`
	Server       types.String `tfsdk:"server"`
	Protocol     types.String `tfsdk:"protocol"`
	Visibility   types.String `tfsdk:"visibility"`
	IconURL      types.String `tfsdk:"icon_url"`
	SystemPrompt types.String `tfsdk:"system_prompt"`
	CreatedAt    types.String `tfsdk:"created_at"`
	ModifiedAt   types.String `tfsdk:"modified_at"`
}

// connectorIdentity is the resource identity of mistral_connector.
type connectorIdentity struct {
	ID types.String `tfsdk:"id"`
}

// namePattern is the rule the specification gives for the name of a connector
// ("64 char length maximum, alphanumeric, only underscores/dashes") and the
// pattern of a credentials name.
var namePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// visibilities are the values a create accepts (PublicResourceVisibility).
var visibilities = []string{client.ConnectorPrivate, client.ConnectorSharedWorkspace, client.ConnectorSharedOrg}

// Metadata names the type mistral_connector.
func (r *ConnectorResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_connector"
}

// IdentitySchema declares the connector ID as the identity.
func (r *ConnectorResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = identityschema.Schema{
		Attributes: map[string]identityschema.Attribute{
			"id": identityschema.StringAttribute{RequiredForImport: true, Description: "The connector ID."},
		},
	}
}

// Schema describes the connector. Its authentication methods, headers and
// OAuth2 metadata are not arguments yet (see the CLAUDE.md of this package).
func (r *ConnectorResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages an MCP or HTTP connector of the workspace of the API key, which agents can call as a tool.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The connector ID, a UUID. A `connector` tool of a `mistral_agent` takes it as `connector_id`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The name of the connector, unique in the workspace: up to 64 letters, digits, underscores and dashes.",
				Validators:          []validator.String{stringvalidator.RegexMatches(namePattern, "must be 1 to 64 letters, digits, underscores or dashes")},
			},
			"title": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "A human-readable title, up to 255 characters.",
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 255)},
			},
			"description": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "What the connector does.",
			},
			"server": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The URL of the connector server, such as `https://mcp.deepwiki.com/mcp`.",
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 2083)},
			},
			"protocol": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(client.ConnectorProtocolMCP),
				MarkdownDescription: "`mcp` (the default) for an MCP server, or `http`. Changing it replaces the connector.",
				Validators:          []validator.String{stringvalidator.OneOf(client.ConnectorProtocolMCP, client.ConnectorProtocolHTTP)},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			// Required, although the API defaults to private: a key with the
			// default connector access scope ("Shared connectors only",
			// https://docs.mistral.ai/admin/identity-access/api-keys) does not
			// see private connectors, so the next refresh would lose one.
			"visibility": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Who can use the connector: `private` (its creator only), `shared_workspace` (the members of the workspace) " +
					"or `shared_org` (the members of the organization).",
				Validators: []validator.String{stringvalidator.OneOf(visibilities...)},
			},
			"icon_url": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "The URL of the icon of the connector.",
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"system_prompt": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "A system prompt added when the model uses the tools of the connector. Changing it replaces an `http` connector.",
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplaceIf(replaceIfHTTP,
					"Changing it replaces an http connector, whose update takes no system prompt.",
					"Changing it replaces an `http` connector, whose update takes no system prompt.")},
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "When the connector was created, in RFC 3339 format.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"modified_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "When the connector was last modified, in RFC 3339 format.",
			},
		},
	}
}

// replaceIfHTTP replaces an HTTP connector whose system prompt changes: the
// create of an HTTP connector takes a system_prompt, its update
// (UpdateHTTPConnectorRequest) does not.
func replaceIfHTTP(ctx context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
	var protocol types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("protocol"), &protocol)...)
	resp.RequiresReplace = protocol.ValueString() == client.ConnectorProtocolHTTP
}

// Configure takes the client of the standard API key.
func (r *ConnectorResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerdata.Configure(req.ProviderData, providerdata.Workspace, &resp.Diagnostics)
}

// Create creates the connector with its visibility.
func (r *ConnectorResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan connectorModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	created, err := r.client.CreateConnector(ctx, plan.createRequest())
	if err != nil {
		resp.Diagnostics.AddError("Cannot create the connector", err.Error())
		return
	}
	// The create body carries the visibility, so this is a no-op unless the
	// API answers another one; the share calls then apply the planned one.
	r.applyVisibility(ctx, created, plan.Visibility.ValueString(), &resp.State, resp.Identity, &resp.Diagnostics)
}

// Read gets the connector by ID; a 404 removes it from the state.
func (r *ConnectorResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var id types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := r.client.GetConnector(ctx, id.ValueString())
	if client.IsNotFound(err) {
		// Set the identity before removing: during an import,
		// terraform-plugin-framework v1.19.0 fails a Read that removes the
		// resource without one, instead of Terraform's "Cannot import
		// non-existent remote object".
		if resp.Identity != nil {
			resp.Diagnostics.Append(resp.Identity.Set(ctx, connectorIdentity{ID: id})...)
		}
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot read the connector", err.Error())
		return
	}
	resp.Diagnostics.Append(setConnectorState(ctx, *got, &resp.State, resp.Identity)...)
}

// Update sends every field to the PATCH, then moves the visibility with the
// share calls, which the PATCH does not take.
func (r *ConnectorResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan connectorModel
	var id types.String
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		return
	}
	updated, err := r.client.UpdateConnector(ctx, id.ValueString(), plan.updateRequest())
	if err != nil {
		resp.Diagnostics.AddError("Cannot update the connector", err.Error())
		return
	}
	r.applyVisibility(ctx, updated, plan.Visibility.ValueString(), &resp.State, resp.Identity, &resp.Diagnostics)
}

// Delete deletes the connector; a 404 means it is already gone.
func (r *ConnectorResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var id types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteConnector(ctx, id.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Cannot delete the connector", err.Error())
	}
}

// ImportState takes the connector ID or the resource identity.
func (r *ConnectorResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughWithIdentity(ctx, path.Root("id"), path.Root("id"), req, resp)
}

// applyVisibility moves the connector c, as the create or the update
// answered it, to the visibility want, and writes the state. When a share
// call fails, the state keeps c, which the next refresh corrects, so that a
// created connector stays in the state.
func (r *ConnectorResource) applyVisibility(ctx context.Context, c *client.Connector, want string, state *tfsdk.State, identity *tfsdk.ResourceIdentity, diags *diag.Diagnostics) {
	if c.Visibility == want {
		diags.Append(setConnectorState(ctx, *c, state, identity)...)
		return
	}
	if err := r.moveVisibility(ctx, c.ID, c.Visibility, want); err != nil {
		diags.Append(setConnectorState(ctx, *c, state, identity)...)
		diags.AddError("Cannot change the visibility of the connector", err.Error())
		return
	}
	// The share calls answer a message, not the connector.
	got, err := r.client.GetConnector(ctx, c.ID)
	if err != nil {
		diags.Append(setConnectorState(ctx, *c, state, identity)...)
		diags.AddError("Cannot read the connector", err.Error())
		return
	}
	diags.Append(setConnectorState(ctx, *got, state, identity)...)
}

// moveVisibility takes the connector id from one visibility to another. A
// share call "transfers ownership of a private user-owned connector", so a
// shared connector goes back to private first: from shared_workspace to
// shared_org is an unshare from the workspace, then a share with the
// organization. An unshare removes the credentials of the scope the
// connector leaves, except its creator's.
func (r *ConnectorResource) moveVisibility(ctx context.Context, id, from, to string) error {
	if from != client.ConnectorPrivate {
		if err := r.client.UnshareConnector(ctx, id, from); err != nil {
			return err
		}
	}
	if to == client.ConnectorPrivate {
		return nil
	}
	return r.client.ShareConnector(ctx, id, to)
}

// createRequest is the body of the create: every field, null included.
func (m *connectorModel) createRequest() client.ConnectorRequest {
	return client.ConnectorRequest{
		Protocol:     m.Protocol.ValueString(),
		Name:         m.Name.ValueString(),
		Title:        m.Title.ValueStringPointer(),
		Description:  m.Description.ValueString(),
		Server:       m.Server.ValueString(),
		IconURL:      m.IconURL.ValueStringPointer(),
		SystemPrompt: m.SystemPrompt.ValueStringPointer(),
		Visibility:   m.Visibility.ValueString(),
	}
}

// updateRequest is the body of the update: every field the update schema of
// the protocol takes, null included, so that the update clears what the
// configuration removed.
func (m *connectorModel) updateRequest() client.ConnectorUpdate {
	return client.ConnectorUpdate{
		Protocol:     m.Protocol.ValueString(),
		Name:         m.Name.ValueString(),
		Title:        m.Title.ValueStringPointer(),
		Description:  m.Description.ValueString(),
		Server:       m.Server.ValueString(),
		IconURL:      m.IconURL.ValueStringPointer(),
		SystemPrompt: m.SystemPrompt.ValueStringPointer(),
	}
}

// setConnectorState writes the connector and its identity.
func setConnectorState(ctx context.Context, c client.Connector, state *tfsdk.State, identity *tfsdk.ResourceIdentity) diag.Diagnostics {
	m := connectorModel{
		ID:           types.StringValue(c.ID),
		Name:         types.StringValue(c.Name),
		Title:        nonEmpty(c.Title),
		Description:  types.StringValue(c.Description),
		Server:       nonEmpty(c.Server),
		Protocol:     types.StringValue(protocolOrDefault(c.Protocol)),
		Visibility:   types.StringValue(c.Visibility),
		IconURL:      nonEmpty(c.IconURL),
		SystemPrompt: nonEmpty(c.SystemPrompt),
		CreatedAt:    timestamp(c.CreatedAt),
		ModifiedAt:   timestamp(c.ModifiedAt),
	}
	diags := state.Set(ctx, &m)
	if identity != nil {
		diags.Append(identity.Set(ctx, connectorIdentity{ID: m.ID})...)
	}
	return diags
}
