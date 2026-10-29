// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package libraries

import (
	"context"
	"fmt"
	"slices"
	"strings"

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
	_ resource.ResourceWithConfigure   = &LibraryShareResource{}
	_ resource.ResourceWithImportState = &LibraryShareResource{}
	_ resource.ResourceWithIdentity    = &LibraryShareResource{}
)

// shareWithTypes are the entities a library can be shared with (EntityType
// in the specification).
var shareWithTypes = []string{"User", "Workspace", "Org"}

// NewLibraryShareResource returns the mistral_library_share resource.
func NewLibraryShareResource() resource.Resource {
	return &LibraryShareResource{}
}

// LibraryShareResource manages one access grant on a library:
// /v1/libraries/{library_id}/share
// (https://docs.mistral.ai/api/endpoint/beta/libraries). A grant is
// identified by the entity it is given to, its type and its ID; its level is
// the only setting, which the PUT upsert changes in place. The grants have no
// read by entity, so Read lists them.
type LibraryShareResource struct {
	client *client.Client
}

// shareModel is the state of mistral_library_share. share_with_id is the
// share_with_uuid of the API, named after the provider's <object>_id rule.
type shareModel struct {
	ID            types.String `tfsdk:"id"`
	LibraryID     types.String `tfsdk:"library_id"`
	ShareWithType types.String `tfsdk:"share_with_type"`
	ShareWithID   types.String `tfsdk:"share_with_id"`
	Level         types.String `tfsdk:"level"`
}

// shareIdentity is the resource identity of mistral_library_share.
type shareIdentity struct {
	LibraryID     types.String `tfsdk:"library_id"`
	ShareWithType types.String `tfsdk:"share_with_type"`
	ShareWithID   types.String `tfsdk:"share_with_id"`
}

// shareKey is the identity of a grant as plain strings.
type shareKey struct {
	libraryID, shareWithType, shareWithID string
}

// importID is the composite ID of the grant, <library_id>:<share_with_type>:<share_with_id>.
func (k shareKey) importID() string {
	return k.libraryID + ":" + k.shareWithType + ":" + k.shareWithID
}

// Metadata names the resource mistral_library_share.
func (r *LibraryShareResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_library_share"
}

// IdentitySchema declares the library and the entity as the identity.
func (r *LibraryShareResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = identityschema.Schema{
		Attributes: map[string]identityschema.Attribute{
			"library_id":      identityschema.StringAttribute{RequiredForImport: true, Description: "The library ID."},
			"share_with_type": identityschema.StringAttribute{RequiredForImport: true, Description: "User, Workspace or Org."},
			"share_with_id":   identityschema.StringAttribute{RequiredForImport: true, Description: "The ID of the user, workspace or organization."},
		},
	}
}

// Schema describes mistral_library_share.
func (r *LibraryShareResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	idPart := []validator.String{stringvalidator.RegexMatches(noColon, "must not be empty or contain a colon")}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Gives a user, a workspace or the organization access to a library, as a viewer or an editor.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "`<library_id>:<share_with_type>:<share_with_id>`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"library_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the library. Changing it replaces the grant.",
				PlanModifiers:       replace,
				Validators:          idPart,
			},
			"share_with_type": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The kind of entity the library is shared with: `User`, `Workspace` or `Org`. Changing it replaces the grant.",
				PlanModifiers:       replace,
				Validators:          []validator.String{stringvalidator.OneOf(shareWithTypes...)},
			},
			"share_with_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the user, the workspace or the organization. Changing it replaces the grant.",
				PlanModifiers:       replace,
				Validators:          idPart,
			},
			"level": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The access level: `Viewer` can search the library, `Editor` can also change it.",
				Validators:          []validator.String{stringvalidator.OneOf("Viewer", "Editor")},
			},
		},
	}
}

// Configure takes the client of the standard API key.
func (r *LibraryShareResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerdata.Configure(req.ProviderData, providerdata.Workspace, &resp.Diagnostics)
}

// Create grants the access, after checking that the entity has none yet.
func (r *LibraryShareResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan shareModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	key := plan.key()

	// The PUT is an upsert: without this check, a create would take over,
	// and change the level of, a grant made outside Terraform.
	existing, err := r.find(ctx, key)
	if err != nil {
		resp.Diagnostics.AddError("Cannot create the library share", err.Error())
		return
	}
	if existing != nil {
		resp.Diagnostics.AddAttributeError(path.Root("share_with_id"), "Library share already exists",
			fmt.Sprintf("The library %q is already shared with the %s %q, as %s. Import the grant with an import block (id = %q) to manage it.",
				key.libraryID, key.shareWithType, key.shareWithID, existing.Role, key.importID()))
		return
	}
	r.put(ctx, plan, &resp.State, resp.Identity, &resp.Diagnostics)
}

// Read finds the grant in the list of the library, and removes it from the
// state when the grant or the library is gone.
func (r *LibraryShareResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state shareModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	found, err := r.find(ctx, state.key())
	if client.IsNotFound(err) {
		removeResource(ctx, resp, state.key().identity())
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot read the library share", err.Error())
		return
	}
	if found == nil {
		removeResource(ctx, resp, state.key().identity())
		return
	}
	resp.Diagnostics.Append(setShareState(ctx, state.key(), found.Role, state.Level, &resp.State, resp.Identity)...)
}

// Update changes the level of the grant, through the same upsert.
func (r *LibraryShareResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan shareModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.put(ctx, plan, &resp.State, resp.Identity, &resp.Diagnostics)
}

// Delete removes the grant; a grant or a library already gone is a success.
func (r *LibraryShareResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state shareModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	key := state.key()
	err := r.client.DeleteLibraryShare(ctx, key.libraryID, key.shareWithType, key.shareWithID)
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Cannot delete the library share", err.Error())
	}
}

// ImportState imports a grant by <library_id>:<share_with_type>:<share_with_id>
// or by its identity. Read then fills the level.
func (r *LibraryShareResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	var key shareKey
	if req.ID != "" {
		parts := strings.Split(req.ID, ":")
		if len(parts) != 3 || slices.Contains(parts, "") || !slices.Contains(shareWithTypes, parts[1]) {
			resp.Diagnostics.AddError("Invalid import ID",
				fmt.Sprintf("The ID must be <library_id>:<share_with_type>:<share_with_id>, with a share_with_type of %s, got %q.",
					strings.Join(shareWithTypes, ", "), req.ID))
			return
		}
		key = shareKey{libraryID: parts[0], shareWithType: parts[1], shareWithID: parts[2]}
	} else {
		var id shareIdentity
		resp.Diagnostics.Append(req.Identity.Get(ctx, &id)...)
		if resp.Diagnostics.HasError() {
			return
		}
		key = shareKey{libraryID: id.LibraryID.ValueString(), shareWithType: id.ShareWithType.ValueString(), shareWithID: id.ShareWithID.ValueString()}
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("library_id"), key.libraryID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("share_with_type"), key.shareWithType)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("share_with_id"), key.shareWithID)...)
}

// find returns the grant of the entity, nil when the library has none for
// it, and a 404 error when the library does not exist. The type and the ID
// are compared without case, since the specification types the answer's
// share_with_type as a free string and its UUID may come back in another
// case.
func (r *LibraryShareResource) find(ctx context.Context, key shareKey) (*client.LibraryShare, error) {
	shares, err := r.client.ListLibraryShares(ctx, key.libraryID)
	if err != nil {
		return nil, err
	}
	for _, s := range shares {
		if s.ShareWithUUID != nil && strings.EqualFold(*s.ShareWithUUID, key.shareWithID) && strings.EqualFold(s.ShareWithType, key.shareWithType) {
			return &s, nil
		}
	}
	return nil, nil
}

// put sends the grant at the level of the plan and writes the answer.
func (r *LibraryShareResource) put(ctx context.Context, plan shareModel, state *tfsdk.State, identity *tfsdk.ResourceIdentity, diags *diag.Diagnostics) {
	key := plan.key()
	got, err := r.client.PutLibraryShare(ctx, key.libraryID, client.LibraryShareRequest{
		Level:         plan.Level.ValueString(),
		ShareWithUUID: key.shareWithID,
		ShareWithType: key.shareWithType,
	})
	if err != nil {
		diags.AddError("Cannot share the library", err.Error())
		return
	}
	diags.Append(setShareState(ctx, key, got.Role, plan.Level, state, identity)...)
}

// identity is the resource identity of the grant.
func (k shareKey) identity() shareIdentity {
	return shareIdentity{LibraryID: types.StringValue(k.libraryID), ShareWithType: types.StringValue(k.shareWithType), ShareWithID: types.StringValue(k.shareWithID)}
}

// key returns the identity of the grant the model describes.
func (m shareModel) key() shareKey {
	return shareKey{libraryID: m.LibraryID.ValueString(), shareWithType: m.ShareWithType.ValueString(), shareWithID: m.ShareWithID.ValueString()}
}

// setShareState writes the grant and its identity. The identity keeps the
// values of the configuration or of the import ID, which find matched
// without case; the level keeps prior when the role the API answers differs
// from it by case only.
func setShareState(ctx context.Context, key shareKey, role string, prior types.String, state *tfsdk.State, identity *tfsdk.ResourceIdentity) diag.Diagnostics {
	level := types.StringValue(role)
	if !prior.IsNull() && !prior.IsUnknown() && strings.EqualFold(prior.ValueString(), role) {
		level = prior
	}
	m := shareModel{
		ID:            types.StringValue(key.importID()),
		LibraryID:     types.StringValue(key.libraryID),
		ShareWithType: types.StringValue(key.shareWithType),
		ShareWithID:   types.StringValue(key.shareWithID),
		Level:         level,
	}
	diags := state.Set(ctx, &m)
	if identity != nil {
		diags.Append(identity.Set(ctx, key.identity())...)
	}
	return diags
}
