// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package libraries

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/identityschema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
	"github.com/ippontech/terraform-provider-mistral/internal/providerdata"
)

var (
	_ resource.ResourceWithConfigure   = &LibraryDocumentResource{}
	_ resource.ResourceWithImportState = &LibraryDocumentResource{}
	_ resource.ResourceWithIdentity    = &LibraryDocumentResource{}
	_ resource.ResourceWithModifyPlan  = &LibraryDocumentResource{}
)

// defaultCreateTimeout bounds a create: the upload and the processing of the
// document. The guide's 9-page PDF was processed in 8 seconds
// (https://docs.mistral.ai/studio/search/libraries); 20 minutes leave room
// for the OCR of a long scanned document and for a queue
// (waiting_for_capacity), and still fail a stuck document within one CI job.
const defaultCreateTimeout = 20 * time.Minute

// taintedNote ends the error of a create that failed after the upload,
// whose document stays in the state.
const taintedNote = "Terraform marks the document tainted: the next apply deletes it and uploads the file again."

// NewLibraryDocumentResource returns the mistral_library_document resource.
func NewLibraryDocumentResource() resource.Resource {
	return &LibraryDocumentResource{}
}

// LibraryDocumentResource manages a document of a library, uploaded from a
// local file: /v1/libraries/{library_id}/documents
// (https://docs.mistral.ai/api/endpoint/beta/libraries). Like
// aws_s3_object, it holds the path of the file and an optional hash of it,
// since the API cannot change the content of a document: a new file replaces
// the document, and only the metadata (name, attributes, expiry) updates in
// place. The create waits until the library has processed the document.
type LibraryDocumentResource struct {
	client *client.Client
}

// documentIdentity is the resource identity of mistral_library_document.
type documentIdentity struct {
	LibraryID  types.String `tfsdk:"library_id"`
	DocumentID types.String `tfsdk:"document_id"`
}

// Metadata names the resource mistral_library_document.
func (r *LibraryDocumentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_library_document"
}

// IdentitySchema declares the library and the document IDs as the identity.
func (r *LibraryDocumentResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = identityschema.Schema{
		Attributes: map[string]identityschema.Attribute{
			"library_id":  identityschema.StringAttribute{RequiredForImport: true, Description: "The library ID."},
			"document_id": identityschema.StringAttribute{RequiredForImport: true, Description: "The document ID."},
		},
	}
}

// Schema describes mistral_library_document. The content attributes (source,
// source_hash) replace the document, except right after an import, when the
// state has no file yet; the attributes the content fixes (size, type, pages)
// keep their state on an update.
func (r *LibraryDocumentResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	keepInt := []planmodifier.Int64{int64planmodifier.UseStateForUnknown()}
	replaceFile := []planmodifier.String{stringplanmodifier.RequiresReplaceIf(replaceUnlessImported,
		"Changing it replaces the document, unless the state has no value yet (after an import).",
		"Changing it replaces the document, unless the state has no value yet (after an import).")}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Uploads a local file to a library as a document, and waits until the library has processed it.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "`<library_id>:<document_id>`.",
				PlanModifiers:       keep,
			},
			"document_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The document ID, a UUID.",
				PlanModifiers:       keep,
			},
			"library_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the library. Changing it replaces the document.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{stringvalidator.RegexMatches(noColon, "must not be empty or contain a colon")},
			},
			"source": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "The path of the local file to upload, such as `\"${path.module}/docs/pricing.pdf\"`. " +
					"Changing it replaces the document, since the API cannot change the content of a document.",
				PlanModifiers: replaceFile,
				Validators:    []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"source_hash": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "A hash of the file, such as `filesha256(\"${path.module}/docs/pricing.pdf\")`. " +
					"Set it so that a change of the content of the file replaces the document: Terraform does not read the file otherwise.",
				PlanModifiers: replaceFile,
				Validators:    []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"name": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "The name of the document. Defaults to the file name of `source`.",
				PlanModifiers:       []planmodifier.String{nameFromSource{}},
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"attributes": schema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Free-form string attributes of the document. A value set outside Terraform as another JSON type reads as its JSON text.",
				Validators:          []validator.Map{mapvalidator.SizeAtLeast(1)},
			},
			"expires_at": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "When the API deletes the document, in RFC 3339 format, such as `2027-01-01T00:00:00Z`. " +
					"Once it has passed, the document reads as gone, and a plan that would upload it again fails until `expires_at` moves.",
				Validators: []validator.String{rfc3339{}},
			},
			"mime_type": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The media type of the document, such as `application/pdf`.",
				PlanModifiers:       keep,
			},
			"extension": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The extension of the document, such as `pdf`.",
				PlanModifiers:       keep,
			},
			"size": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "The size of the document, as the API reports it. It is not compared with the local file: set `source_hash` to follow the content.",
				PlanModifiers:       keepInt,
			},
			"number_of_pages": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "The number of pages the processing found, when the format has pages.",
				PlanModifiers:       keepInt,
			},
			"process_status": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The processing status: `done` once the document can be searched.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "When the document was uploaded, in RFC 3339 format.",
				PlanModifiers:       keep,
			},
			"last_processed_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "When the library last processed the document, in RFC 3339 format.",
			},
		},
		Blocks: map[string]schema.Block{
			"timeouts": timeouts.Block(ctx, timeouts.Opts{
				Create: true,
				CreateDescription: "How long a create may take, the upload and the processing included, as a duration such as `\"30m\"`. " +
					"Defaults to 20 minutes.",
			}),
		},
	}
}

// Configure takes the client of the standard API key.
func (r *LibraryDocumentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerdata.Configure(req.ProviderData, providerdata.Workspace, &resp.Diagnostics)
}

// ModifyPlan refuses an expiry in the past when it would take effect: on a
// create, or when expires_at changes. The API would delete the document
// right after its upload, and the next plan would upload it again.
func (r *LibraryDocumentResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var planned, prior types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("expires_at"), &planned)...)
	if !req.State.Raw.IsNull() {
		resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("expires_at"), &prior)...)
	}
	if resp.Diagnostics.HasError() || planned.IsNull() || planned.IsUnknown() || planned.Equal(prior) {
		return
	}
	// A malformed value is the validator's error.
	if t, err := time.Parse(time.RFC3339, planned.ValueString()); err == nil && !t.After(time.Now()) {
		resp.Diagnostics.AddAttributeError(path.Root("expires_at"), "Expiry in the past",
			fmt.Sprintf("The API deletes a document once its expires_at has passed, and %s has: the document would be deleted right after its upload. "+
				"Move expires_at to a later date, or remove the document from the configuration.", planned.ValueString()))
	}
}

// Create uploads the file, writes the document to the state, waits until the
// library has processed it, then sets the metadata the upload cannot carry.
// The whole create is bounded by timeouts.create.
func (r *LibraryDocumentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan documentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	timeout, diags := plan.Timeouts.Create(ctx, defaultCreateTimeout)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	uploaded := r.upload(ctx, plan, &resp.Diagnostics)
	if uploaded == nil {
		return
	}
	// Written before the wait: when the processing fails or runs out of time,
	// Terraform keeps the document in the state as tainted, and the next apply
	// deletes it and uploads the file again.
	resp.Diagnostics.Append(setDocumentState(ctx, *uploaded, plan, &resp.State, resp.Identity)...)
	if resp.Diagnostics.HasError() || !r.waitProcessed(ctx, plan, *uploaded, timeout, &resp.Diagnostics) {
		return
	}
	doc, err := r.finish(ctx, plan, *uploaded)
	if err != nil {
		resp.Diagnostics.AddError("Cannot complete the library document", err.Error()+". "+taintedNote)
		return
	}
	resp.Diagnostics.Append(setDocumentState(ctx, *doc, plan, &resp.State, resp.Identity)...)
}

// Read refreshes the document, and removes it from the state when the API no
// longer has it or when it has expired.
func (r *LibraryDocumentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state documentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	doc, err := r.client.GetDocument(ctx, state.LibraryID.ValueString(), state.DocumentID.ValueString())
	gone := documentIdentity{LibraryID: state.LibraryID, DocumentID: state.DocumentID}
	if client.IsNotFound(err) {
		removeResource(ctx, resp, gone)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot read the library document", err.Error())
		return
	}
	if expired(*doc, time.Now()) {
		removeResource(ctx, resp, gone)
		return
	}
	resp.Diagnostics.Append(setDocumentState(ctx, *doc, state, &resp.State, resp.Identity)...)
}

// Update sets the metadata when it changed. Otherwise only source,
// source_hash or timeouts changed, which the API does not hold (the first
// apply after an import records the file this way), and Update reads the
// document again.
func (r *LibraryDocumentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state documentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	libraryID, documentID := state.LibraryID.ValueString(), state.DocumentID.ValueString()
	var doc *client.Document
	var err error
	if metadataChanged(plan, state) {
		doc, err = r.client.UpdateDocument(ctx, libraryID, documentID, plan.updateRequest())
	} else {
		doc, err = r.client.GetDocument(ctx, libraryID, documentID)
	}
	if err != nil {
		resp.Diagnostics.AddError("Cannot update the library document", err.Error())
		return
	}
	resp.Diagnostics.Append(setDocumentState(ctx, *doc, plan, &resp.State, resp.Identity)...)
}

// Delete deletes the document; a document or a library already gone is a
// success.
func (r *LibraryDocumentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state documentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteDocument(ctx, state.LibraryID.ValueString(), state.DocumentID.ValueString())
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Cannot delete the library document", err.Error())
	}
}

// ImportState imports a document by <library_id>:<document_id> or by its
// identity. The API does not return the file: source and source_hash stay
// null until the next apply records them in place.
func (r *LibraryDocumentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	var libraryID, documentID string
	if req.ID != "" {
		var ok bool
		libraryID, documentID, ok = strings.Cut(req.ID, ":")
		if !ok || libraryID == "" || documentID == "" || strings.Contains(documentID, ":") {
			resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("The ID must be <library_id>:<document_id>, got %q.", req.ID))
			return
		}
	} else {
		var id documentIdentity
		resp.Diagnostics.Append(req.Identity.Get(ctx, &id)...)
		if resp.Diagnostics.HasError() {
			return
		}
		libraryID, documentID = id.LibraryID.ValueString(), id.DocumentID.ValueString()
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("library_id"), libraryID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("document_id"), documentID)...)
}

// upload sends the file of the plan to the library, and returns the new
// document, or nil after adding a diagnostic.
func (r *LibraryDocumentResource) upload(ctx context.Context, plan documentModel, diags *diag.Diagnostics) *client.Document {
	libraryID, source := plan.LibraryID.ValueString(), plan.Source.ValueString()
	// Checked first, for an error on source rather than on the request.
	if info, err := os.Stat(source); err != nil || info.IsDir() {
		reason := "it is a directory"
		if err != nil {
			reason = err.Error()
		}
		diags.AddAttributeError(path.Root("source"), "Cannot read the source file", fmt.Sprintf("Cannot upload %q: %s.", source, reason))
		return nil
	}
	doc, created, err := r.client.UploadDocument(ctx, libraryID, client.DocumentFile{
		Filename:    filepath.Base(source),
		ContentType: contentType(source),
		Open:        func() (io.ReadCloser, error) { return os.Open(source) }, //nolint:gosec // G304: the configuration names the file to upload
	})
	if err != nil {
		diags.AddError("Cannot upload the library document", err.Error())
		return nil
	}
	if !created {
		// The API answers 200 with the document of the library that has the
		// same content. Managing it would take over a document uploaded
		// outside Terraform, or one that another resource already manages.
		diags.AddAttributeError(path.Root("source"), "Library document already exists",
			fmt.Sprintf("The library %q already holds a document with the content of %q: %q (document %s). "+
				"Import it with an import block (id = %q) to manage it, or remove the other document.",
				libraryID, source, doc.Name, doc.ID, libraryID+":"+doc.ID))
		return nil
	}
	return doc
}

// waitProcessed waits until the library has processed the uploaded document,
// and reports whether it succeeded, after adding a diagnostic otherwise.
func (r *LibraryDocumentResource) waitProcessed(ctx context.Context, plan documentModel, uploaded client.Document, timeout time.Duration, diags *diag.Diagnostics) bool {
	status, err := waitForProcessing(ctx, r.client, plan.LibraryID.ValueString(), uploaded.ID, uploaded.ProcessStatus)
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		diags.AddError("Library document still processing",
			fmt.Sprintf("The library was still processing %q (document %s, status %q) when timeouts.create (%s) ran out. %s "+
				"Raise timeouts.create for a long document.", uploaded.Name, uploaded.ID, status, timeout, taintedNote))
	case err != nil:
		diags.AddError("Cannot read the processing status of the library document", err.Error()+". "+taintedNote)
	case outcomeOf(status) == failed:
		diags.AddError("Library document processing failed",
			fmt.Sprintf("The library could not process %q (document %s): its status is %q. %s", uploaded.Name, uploaded.ID, status, taintedNote))
	default:
		return true
	}
	return false
}

// finish returns the processed document with the metadata of the plan: the
// upload carries only the file, so a name other than the file name, the
// attributes and the expiry take a PATCH. Without them, it reads the
// document again for what the processing filled in.
func (r *LibraryDocumentResource) finish(ctx context.Context, plan documentModel, uploaded client.Document) (*client.Document, error) {
	libraryID := plan.LibraryID.ValueString()
	if uploaded.Name == plan.Name.ValueString() && plan.Attributes == nil && plan.ExpiresAt.IsNull() {
		return r.client.GetDocument(ctx, libraryID, uploaded.ID)
	}
	return r.client.UpdateDocument(ctx, libraryID, uploaded.ID, plan.updateRequest())
}

// metadataChanged reports whether the plan changes what the PATCH of a
// document sets.
func metadataChanged(plan, state documentModel) bool {
	return !plan.Name.Equal(state.Name) || !maps.Equal(plan.Attributes, state.Attributes) || !plan.ExpiresAt.Equal(state.ExpiresAt)
}

// setDocumentState writes the document and its identity. prior is the plan
// or the state the call started from: it gives what the API does not return
// (source, source_hash, timeouts) and the text of the expiry.
func setDocumentState(ctx context.Context, doc client.Document, prior documentModel, state *tfsdk.State, identity *tfsdk.ResourceIdentity) diag.Diagnostics {
	m := documentState(doc, prior)
	diags := state.Set(ctx, &m)
	if identity != nil {
		diags.Append(identity.Set(ctx, documentIdentity{LibraryID: m.LibraryID, DocumentID: m.DocumentID})...)
	}
	return diags
}
