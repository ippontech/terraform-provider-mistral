// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package libraries holds mistral_library, mistral_library_share,
// mistral_library_document and the mistral_library data source: the
// libraries of documents that the document_library tool of an agent searches
// (https://docs.mistral.ai/studio/search/libraries).
package libraries

import (
	"context"

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
	_ resource.ResourceWithConfigure   = &LibraryResource{}
	_ resource.ResourceWithImportState = &LibraryResource{}
	_ resource.ResourceWithIdentity    = &LibraryResource{}
)

// defaultOwnerType is the owner the API gives a library created with an API
// key and no owner_type (CreateLibraryRequest in the specification).
const defaultOwnerType = "Workspace"

// NewLibraryResource returns the mistral_library resource.
func NewLibraryResource() resource.Resource {
	return &LibraryResource{}
}

// LibraryResource manages a library: /v1/libraries
// (https://docs.mistral.ai/api/endpoint/beta/libraries). The deprecated PUT
// of a library is not used: updates go through its PATCH.
type LibraryResource struct {
	client *client.Client
}

// libraryModel is the state of mistral_library, and of the mistral_library
// data source, which has the same attributes.
type libraryModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	OwnerType   types.String `tfsdk:"owner_type"`
	OwnerID     types.String `tfsdk:"owner_id"`
	NbDocuments types.Int64  `tfsdk:"nb_documents"`
	TotalSize   types.Int64  `tfsdk:"total_size"`
	CreatedAt   types.String `tfsdk:"created_at"`
	UpdatedAt   types.String `tfsdk:"updated_at"`
}

// libraryIdentity is the resource identity of mistral_library.
type libraryIdentity struct {
	ID types.String `tfsdk:"id"`
}

// Metadata names the resource mistral_library.
func (r *LibraryResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_library"
}

// IdentitySchema declares the library ID as the identity.
func (r *LibraryResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = identityschema.Schema{
		Attributes: map[string]identityschema.Attribute{
			"id": identityschema.StringAttribute{RequiredForImport: true, Description: "The library ID."},
		},
	}
}

// Schema describes mistral_library. nb_documents, total_size and updated_at
// have no plan modifier: they follow the documents of the library, which
// other resources of the same apply add and delete, so an update plans them
// unknown instead of a value the API may no longer return.
func (r *LibraryResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a library of the workspace of the API key: a knowledge base of documents that the `document_library` tool of an agent searches.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The library ID, a UUID such as `0197f425-5e85-7353-b8e7-e8b974b9c613`. Give it to the `library_ids` of a `document_library` tool.",
				PlanModifiers:       keep,
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The name of the library.",
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"description": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "The description of the library.",
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"owner_type": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Default:  stringdefault.StaticString(defaultOwnerType),
				MarkdownDescription: "Who owns the library: `Workspace` (the default), shared with the workspace of the API key, or `User`, private to its owner. " +
					"Only an API key with the \"Private and shared connectors\" connector access scope can create a `User` library. Changing it replaces the library and deletes its documents.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:    []validator.String{stringvalidator.OneOf("User", "Workspace")},
			},
			"owner_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The ID of the workspace or the user that owns the library.",
				PlanModifiers:       keep,
			},
			"nb_documents": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "The number of documents of the library, as of the last refresh.",
			},
			"total_size": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "The total size of the documents of the library, as of the last refresh.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "When the library was created, in RFC 3339 format.",
				PlanModifiers:       keep,
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "When the library was last updated, in RFC 3339 format.",
			},
		},
	}
}

// Configure takes the client of the standard API key.
func (r *LibraryResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerdata.Configure(req.ProviderData, providerdata.Workspace, &resp.Diagnostics)
}

// Create creates the library. owner_type is always sent, so the library
// gets the owner the plan shows.
func (r *LibraryResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan libraryModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	lib, err := r.client.CreateLibrary(ctx, client.CreateLibraryRequest{
		Name:        plan.Name.ValueString(),
		Description: stringPointer(plan.Description),
		OwnerType:   plan.OwnerType.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Cannot create the library", err.Error())
		return
	}
	resp.Diagnostics.Append(setLibraryState(ctx, *lib, &resp.State, resp.Identity)...)
}

// Read refreshes the library, and removes it from the state when the API no
// longer has it.
func (r *LibraryResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var id types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		return
	}
	lib, err := r.client.GetLibrary(ctx, id.ValueString())
	if client.IsNotFound(err) {
		removeResource(ctx, resp, libraryIdentity{ID: id})
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot read the library", err.Error())
		return
	}
	resp.Diagnostics.Append(setLibraryState(ctx, *lib, &resp.State, resp.Identity)...)
}

// Update sends the name and the description, a null description included,
// so that removing it from the configuration clears it.
func (r *LibraryResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan libraryModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	lib, err := r.client.UpdateLibrary(ctx, plan.ID.ValueString(), client.UpdateLibraryRequest{
		Name:        plan.Name.ValueString(),
		Description: stringPointer(plan.Description),
	})
	if err != nil {
		resp.Diagnostics.AddError("Cannot update the library", err.Error())
		return
	}
	resp.Diagnostics.Append(setLibraryState(ctx, *lib, &resp.State, resp.Identity)...)
}

// Delete deletes the library, which deletes its documents with it.
func (r *LibraryResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var id types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteLibrary(ctx, id.ValueString()); err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Cannot delete the library", err.Error())
	}
}

// ImportState imports a library by its ID or by its identity.
func (r *LibraryResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughWithIdentity(ctx, path.Root("id"), path.Root("id"), req, resp)
}

// setLibraryState writes the library and, when the call has one, its
// identity.
func setLibraryState(ctx context.Context, lib client.Library, state *tfsdk.State, identity *tfsdk.ResourceIdentity) diag.Diagnostics {
	m := libraryState(lib)
	diags := state.Set(ctx, &m)
	if identity != nil {
		diags.Append(identity.Set(ctx, libraryIdentity{ID: m.ID})...)
	}
	return diags
}

// libraryState maps a library of the API to the state. An empty owner_type,
// which the specification does not allow, reads as the default.
func libraryState(lib client.Library) libraryModel {
	ownerType := lib.OwnerType
	if ownerType == "" {
		ownerType = defaultOwnerType
	}
	return libraryModel{
		ID:          types.StringValue(lib.ID),
		Name:        types.StringValue(lib.Name),
		Description: nonEmpty(lib.Description),
		OwnerType:   types.StringValue(ownerType),
		OwnerID:     nonEmpty(lib.OwnerID),
		NbDocuments: types.Int64Value(lib.NbDocuments),
		TotalSize:   types.Int64Value(lib.TotalSize),
		CreatedAt:   timestamp(lib.CreatedAt),
		UpdatedAt:   timestamp(lib.UpdatedAt),
	}
}
