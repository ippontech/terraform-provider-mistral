// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package apikeys holds mistral_api_key: the workspace API keys an
// organization admin creates for a user, through the Admin API.
package apikeys

import (
	"context"
	"fmt"
	"time"

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
	_ resource.ResourceWithConfigure   = &APIKeyResource{}
	_ resource.ResourceWithImportState = &APIKeyResource{}
	_ resource.ResourceWithIdentity    = &APIKeyResource{}
)

// NewAPIKeyResource returns the mistral_api_key resource.
func NewAPIKeyResource() resource.Resource {
	return &APIKeyResource{}
}

// APIKeyResource manages a workspace API key: /v1/admin/api-keys
// (https://docs.mistral.ai/admin/identity-access/api-keys). The API creates,
// lists and deletes keys, and updates none: every argument replaces the key.
// It has no read by ID, so Read goes through findAPIKey.
type APIKeyResource struct {
	client *client.Client
}

// apiKeyModel is the state of mistral_api_key.
type apiKeyModel struct {
	ID          types.String `tfsdk:"id"`
	WorkspaceID types.String `tfsdk:"workspace_id"`
	UserID      types.String `tfsdk:"user_id"`
	Name        types.String `tfsdk:"name"`
	// Expiration is the date the configuration asks for, never read back:
	// an expiration policy can shorten it (ExpirationDate), and reading
	// that date into Expiration would plan a replacement on every run.
	Expiration     types.String `tfsdk:"expiration"`
	ExpirationDate types.String `tfsdk:"expiration_date"`
	Key            types.String `tfsdk:"key"`
	HiddenKey      types.String `tfsdk:"hidden_key"`
	CreatedAt      types.String `tfsdk:"created_at"`
}

// apiKeyIdentity is the resource identity of mistral_api_key.
type apiKeyIdentity struct {
	ID types.String `tfsdk:"id"`
}

// Metadata names the resource mistral_api_key.
func (r *APIKeyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_api_key"
}

// IdentitySchema declares the identity of a key: its ID.
func (r *APIKeyResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = identityschema.Schema{
		Attributes: map[string]identityschema.Attribute{
			"id": identityschema.StringAttribute{RequiredForImport: true, Description: "The API key ID."},
		},
	}
}

// Schema declares the arguments and attributes of mistral_api_key.
func (r *APIKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a workspace API key created for a user, with the Admin API key. The API cannot update a key: " +
			"changing any argument replaces it, and destroying the resource deletes it.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The API key ID, a UUID.",
				PlanModifiers:       keep,
			},
			"workspace_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the workspace the key acts in, whose quota, limits and resources it uses. Changing it replaces the key.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"user_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the user the key is created for. Changing it replaces the key, except after an import, where the first apply records it in place.",
				PlanModifiers:       []planmodifier.String{replaceUnlessImported()},
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"name": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "The name of the key, of at most 255 characters. Changing it replaces the key.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{stringvalidator.UTF8LengthBetween(1, 255)},
			},
			"expiration": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "The date the key should expire, in RFC 3339 date format (`2027-06-30`). When null, the key never expires, " +
					"unless an expiration policy applies. The state keeps the configured date: `expiration_date` gives the date Mistral set. " +
					"Changing it replaces the key, except after an import, where the first apply records it in place, " +
					"and except when it is added to a key whose `expiration_date` is already on or before it, which records it in place too.",
				PlanModifiers: []planmodifier.String{replaceExpiration()},
				Validators:    []validator.String{date{}},
			},
			"expiration_date": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "The date the key expires, as Mistral set it: `expiration`, or an earlier date when an expiration policy " +
					"applies to the workspace. Null when the key never expires.",
				PlanModifiers: keep,
			},
			"key": schema.StringAttribute{
				Computed:            true,
				Sensitive:           true,
				MarkdownDescription: "The secret of the key, which only the create returns. The state holds it; it is null after an import.",
				PlanModifiers:       keep,
			},
			"hidden_key": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The masked secret, as the Admin Panel shows it.",
				PlanModifiers:       keep,
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "When the key was created, in RFC 3339 format.",
				PlanModifiers:       keep,
			},
		},
	}
}

// replaceUnlessImported replaces the key when user_id changes, except on the
// first apply after an import (replaceExpiration does the same for
// expiration). Neither is read back, user_id because the list does not
// return it and expiration because the API may shorten it, so both are null
// after an import: that apply records the configured values in place
// (Update) instead of replacing a key that applications use.
//
// A null value alone does not mark an import: a key created without an
// expiration has none either, and recording one in place would leave the key
// without an expiry at Mistral. The import marker is a null user_id in the
// state (imported).
func replaceUnlessImported() planmodifier.String {
	return stringplanmodifier.RequiresReplaceIf(
		func(ctx context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
			resp.RequiresReplace = !imported(ctx, req.State, &resp.Diagnostics)
		},
		"Changing the value replaces the key, except on the first apply after an import, which records it in place.",
		"Changing the value replaces the key, except on the first apply after an import, which records it in place.",
	)
}

// replaceExpiration is replaceUnlessImported for expiration, which also
// records in place an expiration added to a key that already expires on or
// before that date, as a policy may have set it: the key meets the new date,
// and a new key would expire no earlier, so a replacement would only cut off
// the applications that use it. An expiration added to a key that never
// expires, or that expires later, still replaces the key, and so does any
// change of a configured expiration: moving it later is how a key is renewed.
func replaceExpiration() planmodifier.String {
	return stringplanmodifier.RequiresReplaceIf(
		func(ctx context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
			if imported(ctx, req.State, &resp.Diagnostics) {
				return
			}
			// A change from null adds a date. One unknown at plan time reads
			// as "", which expiresBy cannot compare: the key is replaced.
			added := req.StateValue.IsNull()
			resp.RequiresReplace = !added || !expiresBy(ctx, req.State, req.PlanValue.ValueString(), &resp.Diagnostics)
		},
		"Changing the value replaces the key, except on the first apply after an import, and except for a date added to a key "+
			"that already expires on or before it; both record the date in place.",
		"Changing the value replaces the key, except on the first apply after an import, and except for a date added to a key "+
			"that already expires on or before it; both record the date in place.",
	)
}

// imported reports whether state is that of a key imported and not applied
// since: user_id is required, so every create stores it, and only an import
// leaves it null until the first apply records it.
func imported(ctx context.Context, state tfsdk.State, diags *diag.Diagnostics) bool {
	var userID types.String
	diags.Append(state.GetAttribute(ctx, path.Root("user_id"), &userID)...)
	return userID.IsNull()
}

// expiresBy reports whether the key of state expires on or before planned,
// a YYYY-MM-DD date. The client reads expiration_date as such a date, the
// format of the specification; a null date (a key that never expires) or
// another format reports false, which replaces the key: the safe side.
func expiresBy(ctx context.Context, state tfsdk.State, planned string, diags *diag.Diagnostics) bool {
	var current types.String
	diags.Append(state.GetAttribute(ctx, path.Root("expiration_date"), &current)...)
	expiry, err := time.Parse(time.DateOnly, current.ValueString())
	if err != nil {
		return false
	}
	asked, err := time.Parse(time.DateOnly, planned)
	return err == nil && !expiry.After(asked)
}

// Configure takes the client of the provider, and fails without the Admin API
// key.
func (r *APIKeyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerdata.Configure(req.ProviderData, providerdata.Admin, &resp.Diagnostics)
}

// Create creates the key and stores its secret, which no later call returns.
func (r *APIKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan apiKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	k, err := r.client.CreateAPIKey(ctx, client.CreateAPIKeyRequest{
		Name:        plan.Name.ValueStringPointer(),
		Expiration:  plan.Expiration.ValueStringPointer(),
		WorkspaceID: plan.WorkspaceID.ValueString(),
		UserID:      plan.UserID.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Cannot create the API key", err.Error())
		return
	}
	state := apiKeyState(*k, plan)
	// The create is the only answer with the secret.
	state.Key = types.StringPointerValue(k.Key)
	resp.Diagnostics.Append(writeAPIKey(ctx, state, &resp.State, resp.Identity)...)
}

// Read refreshes the key through findAPIKey, and removes it from the state
// when the organization no longer has it.
func (r *APIKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state apiKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	k, err := findAPIKey(ctx, r.client, state.ID.ValueString(), state.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Cannot read the API key", err.Error())
		return
	}
	if k == nil {
		// Set the identity before removing: during an import, terraform-plugin-framework v1.19.0 fails a Read that removes the resource without one.
		if resp.Identity != nil {
			resp.Diagnostics.Append(resp.Identity.Set(ctx, apiKeyIdentity{ID: state.ID})...)
		}
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(writeAPIKey(ctx, apiKeyState(*k, state), &resp.State, resp.Identity)...)
}

// Update only records user_id and expiration on the first apply after an
// import, and an expiration added to a key that already expires by then:
// every other change replaces the key (replaceUnlessImported,
// replaceExpiration), and the API has no update to call.
func (r *APIKeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state apiKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	state.UserID, state.Expiration = plan.UserID, plan.Expiration
	resp.Diagnostics.Append(writeAPIKey(ctx, state, &resp.State, resp.Identity)...)
}

// Delete deletes the key at Mistral: the applications that use it lose
// access.
func (r *APIKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var id types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteAPIKey(ctx, id.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Cannot delete the API key", err.Error())
	}
}

// ImportState imports a key by its ID or its identity. Read fills in the rest,
// without the secret.
func (r *APIKeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughWithIdentity(ctx, path.Root("id"), path.Root("id"), req, resp)
}

// findAPIKey returns the API key id, or nil when the organization has no key
// by that ID. There is no read by ID (GET /v1/admin/api-keys/{key_id} does
// not exist), so it lists the keys whose name contains name, the narrowest
// filter of GET /v1/admin/api-keys, then every key, since a key imported or
// renamed outside Terraform escapes that filter. It is the one function to
// replace when Mistral adds a read by ID.
func findAPIKey(ctx context.Context, cl *client.Client, id, name string) (*client.APIKey, error) {
	byID := func(k client.APIKey) bool { return k.ID == id }
	if name != "" {
		k, err := cl.FindAPIKey(ctx, name, byID)
		if err != nil || k != nil {
			return k, err
		}
	}
	return cl.FindAPIKey(ctx, "", byID)
}

// apiKeyState maps k to the state. prior is the plan or state the call
// started from: the secret, the user and the configured expiration are never
// read back, so they keep their prior value, and so does the workspace when
// an answer leaves it out, as the create does.
func apiKeyState(k client.APIKey, prior apiKeyModel) apiKeyModel {
	m := prior
	m.ID = types.StringValue(k.ID)
	m.Name = nonEmpty(k.Name)
	m.ExpirationDate = types.StringPointerValue(k.ExpirationDate)
	m.HiddenKey = types.StringValue(k.HiddenKey)
	if k.WorkspaceID != nil {
		m.WorkspaceID = types.StringValue(*k.WorkspaceID)
	}
	if k.CreatedAt != nil {
		m.CreatedAt = types.StringValue(k.CreatedAt.UTC().Format(time.RFC3339))
	}
	return m
}

// writeAPIKey sets m as the state, and its ID as the resource identity.
func writeAPIKey(ctx context.Context, m apiKeyModel, state *tfsdk.State, identity *tfsdk.ResourceIdentity) diag.Diagnostics {
	diags := state.Set(ctx, &m)
	if identity != nil {
		diags.Append(identity.Set(ctx, apiKeyIdentity{ID: m.ID})...)
	}
	return diags
}

// nonEmpty reads an optional name: a key created without one may answer
// null or "", and both are null in the state.
func nonEmpty(s *string) types.String {
	if s == nil || *s == "" {
		return types.StringNull()
	}
	return types.StringValue(*s)
}

// date validates an RFC 3339 full date (YYYY-MM-DD), the format of
// expiration in the specification.
type date struct{}

// Description says what the validator accepts.
func (date) Description(context.Context) string {
	return "value must be a date in the YYYY-MM-DD format"
}

// MarkdownDescription is Description.
func (v date) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }

// ValidateString fails on a value that is not a YYYY-MM-DD date.
func (date) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if _, err := time.Parse(time.DateOnly, req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid date",
			fmt.Sprintf("The value must be a date in the YYYY-MM-DD format, such as 2027-06-30: %v", err))
	}
}
