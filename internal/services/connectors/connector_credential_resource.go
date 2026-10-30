// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package connectors

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/identityschema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
	"github.com/ippontech/terraform-provider-mistral/internal/providerdata"
)

var (
	_ resource.ResourceWithConfigure   = &ConnectorCredentialResource{}
	_ resource.ResourceWithImportState = &ConnectorCredentialResource{}
	_ resource.ResourceWithIdentity    = &ConnectorCredentialResource{}
)

// NewConnectorCredentialResource returns the mistral_connector_credential
// resource.
func NewConnectorCredentialResource() resource.Resource {
	return &ConnectorCredentialResource{}
}

// ConnectorCredentialResource manages a named set of credentials of a
// connector for a consumer scope:
// /v1/connectors/{connector_id_or_name}/{consumer_scope}/credentials, which
// has no read by name, so Read lists the credentials of the scope. The
// secret values are write-only: the API never returns them, and the state
// never holds them.
type ConnectorCredentialResource struct {
	client *client.Client
}

// connectorCredentialModel is the state of mistral_connector_credential.
// CredentialsWO is always null in the state.
type connectorCredentialModel struct {
	ID                   types.String `tfsdk:"id"`
	ConnectorID          types.String `tfsdk:"connector_id"`
	Scope                types.String `tfsdk:"scope"`
	Name                 types.String `tfsdk:"name"`
	Title                types.String `tfsdk:"title"`
	IsDefault            types.Bool   `tfsdk:"is_default"`
	CredentialsWO        types.String `tfsdk:"credentials_wo"`
	CredentialsWOVersion types.Int64  `tfsdk:"credentials_wo_version"`
	AuthenticationType   types.String `tfsdk:"authentication_type"`
}

// connectorCredentialIdentity is the resource identity of
// mistral_connector_credential.
type connectorCredentialIdentity struct {
	ConnectorID types.String `tfsdk:"connector_id"`
	Scope       types.String `tfsdk:"scope"`
	Name        types.String `tfsdk:"name"`
}

// credentialScopes are the consumer scopes the resource manages. The user
// scope is left out: it binds the credentials to the person who owns the
// API key, which a configuration shared by a team rarely wants.
var credentialScopes = []string{client.CredentialsScopeWorkspace, client.CredentialsScopeOrganization}

// noColon is a connector ID: the import ID joins it to the scope and the
// name with colons.
var noColon = regexp.MustCompile(`^[^:]+$`)

// Metadata names the type mistral_connector_credential.
func (r *ConnectorCredentialResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_connector_credential"
}

// IdentitySchema declares the three parts of the import ID.
func (r *ConnectorCredentialResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = identityschema.Schema{
		Attributes: map[string]identityschema.Attribute{
			"connector_id": identityschema.StringAttribute{RequiredForImport: true, Description: "The connector ID."},
			"scope":        identityschema.StringAttribute{RequiredForImport: true, Description: "The consumer scope: `workspace` or `organization`."},
			"name":         identityschema.StringAttribute{RequiredForImport: true, Description: "The name of the credentials."},
		},
	}
}

// Schema describes the credentials. The connector, the scope and the name
// locate them, so changing one replaces them.
func (r *ConnectorCredentialResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a named set of credentials that a connector uses for a workspace or an organization. The secret values are write-only: the state never holds them.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "`<connector_id>:<scope>:<name>`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"connector_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the connector, such as `mistral_connector.this.id`. Changing it replaces the credentials.",
				PlanModifiers:       replace,
				Validators:          []validator.String{stringvalidator.RegexMatches(noColon, "must not be empty or contain a colon")},
			},
			"scope": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Who the credentials serve: `workspace` (the members of the workspace of the API key) or `organization`. " +
					"Changing it replaces the credentials.",
				PlanModifiers: replace,
				Validators:    []validator.String{stringvalidator.OneOf(credentialScopes...)},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The name of the credentials, unique per connector and scope: up to 64 letters, digits, underscores and dashes. Changing it replaces the credentials.",
				PlanModifiers:       replace,
				Validators:          []validator.String{stringvalidator.RegexMatches(namePattern, "must be 1 to 64 letters, digits, underscores or dashes")},
			},
			"title": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "A human-readable title, up to 255 characters.",
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 255)},
			},
			"is_default": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "Whether these are the default credentials of their authentication method. When unset, the API decides: " +
					"the first credentials of a method become its default. `true` makes them the default; `false` is refused for the current default.",
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"credentials_wo": schema.StringAttribute{
				Required:  true,
				WriteOnly: true,
				Sensitive: true,
				MarkdownDescription: "The secret values, as a JSON object written with `jsonencode`: `bearer_token` (a string), `headers` (an object of strings), " +
					"or `oauth` (OAuth2 client credentials). Write-only: Terraform sends it on create, and on update when `credentials_wo_version` changes.",
				Validators: []validator.String{credentialsDocument{}},
			},
			"credentials_wo_version": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Change it to send `credentials_wo` again, after a rotation of the secret.",
			},
			"authentication_type": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The authentication method the credentials are for, as the API reports it: `oauth2`, `bearer`, `none`, `github_app` or `slack_app`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

// Configure takes the client of the standard API key.
func (r *ConnectorCredentialResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerdata.Configure(req.ProviderData, providerdata.Workspace, &resp.Diagnostics)
}

// Create checks that the credentials do not exist, then creates them with
// the POST. The write-only secret comes from the configuration: the plan
// holds it as null.
func (r *ConnectorCredentialResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan, config connectorCredentialModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	connectorID, scope, name := plan.ConnectorID.ValueString(), plan.Scope.ValueString(), plan.Name.ValueString()

	// Without this check, a create would collide with credentials that exist
	// outside Terraform, or take them over if the API answered the POST as
	// the "create or update" PATCH does.
	existing, err := r.find(ctx, connectorID, scope, name)
	if err != nil {
		resp.Diagnostics.AddError("Cannot create the connector credentials", err.Error())
		return
	}
	if existing != nil {
		resp.Diagnostics.AddAttributeError(path.Root("name"), "Connector credentials already exist",
			fmt.Sprintf("The connector %q already has credentials %q for the %s scope. Import them with an import block (id = %q) to manage them, "+
				"or choose another name.", connectorID, name, scope, credentialID(connectorID, scope, name)))
		return
	}
	body := plan.request(config)
	body.Credentials = json.RawMessage(config.CredentialsWO.ValueString())
	if err := r.client.CreateConnectorCredentials(ctx, connectorID, scope, body); err != nil {
		resp.Diagnostics.AddError("Cannot create the connector credentials", err.Error())
		return
	}
	r.readBack(ctx, plan, &resp.State, resp.Identity, &resp.Diagnostics)
}

// Read lists the credentials of the scope and finds these by name. A
// connector deleted outside Terraform answers 404, which removes the
// credentials too.
func (r *ConnectorCredentialResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state connectorCredentialModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	found, err := r.find(ctx, state.ConnectorID.ValueString(), state.Scope.ValueString(), state.Name.ValueString())
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Cannot read the connector credentials", err.Error())
		return
	}
	// A 404 means the connector is gone, and its credentials with it.
	if found == nil {
		// Set the identity before removing: during an import,
		// terraform-plugin-framework v1.19.0 fails a Read that removes the
		// resource without one, instead of Terraform's "Cannot import
		// non-existent remote object".
		if resp.Identity != nil {
			resp.Diagnostics.Append(resp.Identity.Set(ctx, connectorCredentialIdentity{
				ConnectorID: state.ConnectorID, Scope: state.Scope, Name: state.Name,
			})...)
		}
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(setCredentialState(ctx, state, *found, &resp.State, resp.Identity)...)
}

// Update sends the title and is_default with the PATCH, and the secret only
// when credentials_wo_version changed: Terraform cannot compare a
// write-only value. That an omitted credentials keeps the secret is our
// reading: the specification says so of an omitted is_default only.
func (r *ConnectorCredentialResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state, config connectorCredentialModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body := plan.request(config)
	if !plan.CredentialsWOVersion.Equal(state.CredentialsWOVersion) {
		body.Credentials = json.RawMessage(config.CredentialsWO.ValueString())
	}
	if err := r.client.UpdateConnectorCredentials(ctx, plan.ConnectorID.ValueString(), plan.Scope.ValueString(), body); err != nil {
		resp.Diagnostics.AddError("Cannot update the connector credentials", err.Error())
		return
	}
	r.readBack(ctx, plan, &resp.State, resp.Identity, &resp.Diagnostics)
}

// Delete deletes the credentials; a 404 means they, or their connector, are
// already gone.
func (r *ConnectorCredentialResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state connectorCredentialModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteConnectorCredentials(ctx, state.ConnectorID.ValueString(), state.Scope.ValueString(), state.Name.ValueString())
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Cannot delete the connector credentials", err.Error())
	}
}

// ImportState reads <connector_id>:<scope>:<name>, or the resource
// identity. The secret cannot be imported, and Update sends credentials_wo
// only when credentials_wo_version differs from the state, where an import
// leaves it null: the first apply after an import sends the secret only when
// the configuration sets credentials_wo_version. Without it, nothing sends
// credentials_wo until it is set.
func (r *ConnectorCredentialResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	var connectorID, scope, name string
	if req.ID != "" {
		parts := strings.Split(req.ID, ":")
		if len(parts) != 3 || slices.Contains(parts, "") {
			resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("The ID must be <connector_id>:<scope>:<name>, got %q.", req.ID))
			return
		}
		connectorID, scope, name = parts[0], parts[1], parts[2]
	} else {
		var id connectorCredentialIdentity
		resp.Diagnostics.Append(req.Identity.Get(ctx, &id)...)
		if resp.Diagnostics.HasError() {
			return
		}
		connectorID, scope, name = id.ConnectorID.ValueString(), id.Scope.ValueString(), id.Name.ValueString()
	}
	if !slices.Contains(credentialScopes, scope) {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("The scope must be workspace or organization, got %q.", scope))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("connector_id"), connectorID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("scope"), scope)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), name)...)
}

// find returns the credentials name of the connector for the scope, nil
// when the scope has none by that name, and a 404 error when the connector
// does not exist. The API has no read by name: replace this function, and
// nothing else, when Mistral adds one.
func (r *ConnectorCredentialResource) find(ctx context.Context, connectorID, scope, name string) (*client.ConnectorCredentials, error) {
	all, err := r.client.ListConnectorCredentials(ctx, connectorID, scope)
	if err != nil {
		return nil, err
	}
	for _, c := range all {
		if c.Name == name {
			return &c, nil
		}
	}
	return nil, nil
}

// readBack writes the credentials after a create or an update: both answer
// a message only, so the list gives what the API stored.
func (r *ConnectorCredentialResource) readBack(ctx context.Context, plan connectorCredentialModel, state *tfsdk.State, identity *tfsdk.ResourceIdentity, diags *diag.Diagnostics) {
	found, err := r.find(ctx, plan.ConnectorID.ValueString(), plan.Scope.ValueString(), plan.Name.ValueString())
	if err != nil {
		diags.AddError("Cannot read the connector credentials", err.Error())
		return
	}
	if found == nil {
		diags.AddError("Cannot read the connector credentials",
			fmt.Sprintf("The API accepted the credentials %q, but does not list them for the %s scope of the connector %q.",
				plan.Name.ValueString(), plan.Scope.ValueString(), plan.ConnectorID.ValueString()))
		return
	}
	diags.Append(setCredentialState(ctx, plan, *found, state, identity)...)
}

// request is the body of a create or an update without the secret. config
// gives is_default: when the configuration leaves it out, the body does too,
// and the API applies the rules of the specification instead of the value
// the state last read.
func (m *connectorCredentialModel) request(config connectorCredentialModel) client.ConnectorCredentialsRequest {
	return client.ConnectorCredentialsRequest{
		Name:      m.Name.ValueString(),
		Title:     m.Title.ValueStringPointer(),
		IsDefault: config.IsDefault.ValueBoolPointer(),
	}
}

// setCredentialState writes the credentials found in the list and their
// identity. prior gives what the list does not return: the connector, the
// scope and credentials_wo_version.
func setCredentialState(ctx context.Context, prior connectorCredentialModel, found client.ConnectorCredentials, state *tfsdk.State, identity *tfsdk.ResourceIdentity) diag.Diagnostics {
	connectorID, scope := prior.ConnectorID.ValueString(), prior.Scope.ValueString()
	m := connectorCredentialModel{
		ID:                   types.StringValue(credentialID(connectorID, scope, found.Name)),
		ConnectorID:          prior.ConnectorID,
		Scope:                prior.Scope,
		Name:                 types.StringValue(found.Name),
		Title:                nonEmpty(found.Title),
		IsDefault:            types.BoolValue(found.IsDefault),
		CredentialsWO:        types.StringNull(),
		CredentialsWOVersion: prior.CredentialsWOVersion,
		AuthenticationType:   nonEmpty(&found.AuthenticationType),
	}
	diags := state.Set(ctx, &m)
	if identity != nil {
		diags.Append(identity.Set(ctx, connectorCredentialIdentity{ConnectorID: m.ConnectorID, Scope: m.Scope, Name: m.Name})...)
	}
	return diags
}

// credentialID is the ID and the import ID of credentials.
func credentialID(connectorID, scope, name string) string {
	return connectorID + ":" + scope + ":" + name
}

// credentialsDocument validates credentials_wo against
// ConnectionCredentialsInput: a JSON object of headers, bearer_token and
// oauth, which refuses any other field (additionalProperties: false).
type credentialsDocument struct{}

// Description says what the validator accepts.
func (credentialsDocument) Description(context.Context) string {
	return "value must be a JSON object of headers, bearer_token or oauth"
}

// MarkdownDescription is Description.
func (v credentialsDocument) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

// ValidateString checks the document; a value unknown at plan time, such as
// an ephemeral one, is checked at apply time.
func (credentialsDocument) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal([]byte(req.ConfigValue.ValueString()), &doc); err != nil || doc == nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid credentials",
			"The credentials must be a JSON object, such as jsonencode({ bearer_token = var.token }).")
		return
	}
	for _, field := range slices.Sorted(maps.Keys(doc)) {
		if problem := credentialsFieldProblem(field, doc[field]); problem != "" {
			resp.Diagnostics.AddAttributeError(req.Path, "Invalid credentials", problem)
		}
	}
}

// credentialsFieldProblem says what is wrong with one field of the
// credentials, "" when nothing is. Each field may be null.
func credentialsFieldProblem(field string, raw json.RawMessage) string {
	var target any
	var want string
	switch field {
	case "headers":
		target, want = &map[string]string{}, "an object of strings"
	case "bearer_token":
		target, want = new(string), "a string"
	case "oauth":
		target, want = &map[string]json.RawMessage{}, "an object"
	default:
		return fmt.Sprintf("The field %q is not one of headers, bearer_token or oauth.", field)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Sprintf("The field %q must be %s.", field, want)
	}
	return ""
}
