// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package libraries

import (
	"context"
	"fmt"
	"mime"
	"path/filepath"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
)

// documentModel is the state of mistral_library_document.
type documentModel struct {
	ID              types.String      `tfsdk:"id"`
	DocumentID      types.String      `tfsdk:"document_id"`
	LibraryID       types.String      `tfsdk:"library_id"`
	Source          types.String      `tfsdk:"source"`
	SourceHash      types.String      `tfsdk:"source_hash"`
	Name            types.String      `tfsdk:"name"`
	Attributes      map[string]string `tfsdk:"attributes"`
	ExpiresAt       types.String      `tfsdk:"expires_at"`
	MimeType        types.String      `tfsdk:"mime_type"`
	Extension       types.String      `tfsdk:"extension"`
	Size            types.Int64       `tfsdk:"size"`
	NumberOfPages   types.Int64       `tfsdk:"number_of_pages"`
	ProcessStatus   types.String      `tfsdk:"process_status"`
	CreatedAt       types.String      `tfsdk:"created_at"`
	LastProcessedAt types.String      `tfsdk:"last_processed_at"`
	Timeouts        timeouts.Value    `tfsdk:"timeouts"`
}

// documentState maps a document of the API to the state. prior gives the
// library ID as configured or imported, the attributes the API does not hold
// (source, source_hash, timeouts), and the text of the expiry when the API
// returns the same instant.
func documentState(doc client.Document, prior documentModel) documentModel {
	return documentModel{
		ID:              types.StringValue(prior.LibraryID.ValueString() + ":" + doc.ID),
		DocumentID:      types.StringValue(doc.ID),
		LibraryID:       prior.LibraryID,
		Source:          prior.Source,
		SourceHash:      prior.SourceHash,
		Name:            types.StringValue(doc.Name),
		Attributes:      attributesState(doc.Attributes),
		ExpiresAt:       instantState(doc.ExpiresAt, prior.ExpiresAt),
		MimeType:        nonEmpty(doc.MimeType),
		Extension:       nonEmpty(doc.Extension),
		Size:            types.Int64PointerValue(doc.Size),
		NumberOfPages:   types.Int64PointerValue(doc.NumberOfPages),
		ProcessStatus:   types.StringValue(doc.ProcessStatus),
		CreatedAt:       timestamp(doc.CreatedAt),
		LastProcessedAt: optionalTimestamp(doc.LastProcessedAt),
		Timeouts:        prior.Timeouts,
	}
}

// updateRequest is the PATCH body of the model: every field, null included,
// so that removing the attributes or the expiry from the configuration
// clears them.
func (m documentModel) updateRequest() client.UpdateDocumentRequest {
	return client.UpdateDocumentRequest{
		Name:       m.Name.ValueString(),
		Attributes: m.Attributes,
		ExpiresAt:  stringPointer(m.ExpiresAt),
	}
}

// expired reports whether the document is past its expiry. The API deletes
// such a document ("If set, the document will be automatically deleted after
// this date", Document in the specification) but may return it until its
// deletion runs; Read treats it as gone either way.
func expired(doc client.Document, now time.Time) bool {
	return doc.ExpiresAt != nil && !doc.ExpiresAt.After(now)
}

// contentType is the media type of the file part, from the extension of the
// file, as the curl example of the libraries guide sets it
// (type=application/pdf); application/octet-stream when the extension is
// unknown.
func contentType(source string) string {
	if t := mime.TypeByExtension(filepath.Ext(source)); t != "" {
		return t
	}
	return "application/octet-stream"
}

// replaceUnlessImported replaces the document when source or source_hash
// changes, the way an upload of a new file would, except on the first apply
// after an import: an import cannot recover the file, so both are null in its
// state, and that apply records them in place. The import marker is a null
// source in the state, which is required and so set by every create; a null
// source_hash is not one, since a document uploaded without a hash has none
// either. Adding a hash to such a document replaces it: Terraform never read
// the file, so it cannot tell whether the content changed since the upload,
// and recording the hash in place would keep the old content for good.
// Removing source_hash only records the change: it stops following the
// content, and has nothing to upload.
func replaceUnlessImported(ctx context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
	var source types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("source"), &source)...)
	resp.RequiresReplace = !source.IsNull() && !req.PlanValue.IsNull()
}

// nameFromSource plans the file name of source for a name the configuration
// leaves out: the upload names the document after its file. Planning it,
// rather than leaving it to the API, lets removing name from the
// configuration rename the document back.
type nameFromSource struct{}

// Description says what the modifier does.
func (nameFromSource) Description(context.Context) string {
	return "Defaults to the file name of source."
}

// MarkdownDescription is the Description.
func (m nameFromSource) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

// PlanModifyString plans filepath.Base(source) for a null name, and an
// unknown name while source is unknown.
func (nameFromSource) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if !req.ConfigValue.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var source types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("source"), &source)...)
	if source.IsNull() || source.IsUnknown() {
		resp.PlanValue = types.StringUnknown()
		return
	}
	resp.PlanValue = types.StringValue(filepath.Base(source.ValueString()))
}

// rfc3339 validates that a string is an RFC 3339 date-time.
type rfc3339 struct{}

// Description says what the validator checks.
func (rfc3339) Description(context.Context) string {
	return "value must be an RFC 3339 date-time, such as 2027-01-01T00:00:00Z"
}

// MarkdownDescription is the Description.
func (v rfc3339) MarkdownDescription(ctx context.Context) string { return v.Description(ctx) }

// ValidateString checks a known value.
func (rfc3339) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if _, err := time.Parse(time.RFC3339, req.ConfigValue.ValueString()); err != nil {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid date-time",
			fmt.Sprintf("The value must be an RFC 3339 date-time, such as 2027-01-01T00:00:00Z: %v", err))
	}
}
