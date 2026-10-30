// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package promptsandskills holds mistral_prompt and mistral_skill, the
// versioned prompts and skills of a workspace (/v2/prompts and /v2/skills).
// The specification calls both "registry objects": they share their routes,
// their version protocol and their sharing settings, which this package
// writes once. It has nothing to do with internal/provider/registry.go, the
// list of the provider's types.
package promptsandskills

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
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
)

// idIdentity is the resource identity of mistral_prompt and mistral_skill.
type idIdentity struct {
	ID types.String `tfsdk:"id"`
}

// removeGone removes a prompt or a skill from the state after setting its
// identity: during an import, terraform-plugin-framework v1.19.0 fails a Read
// that removes the resource without one, instead of Terraform's "Cannot
// import non-existent remote object".
func removeGone(ctx context.Context, resp *resource.ReadResponse, id types.String) {
	if resp.Identity != nil {
		resp.Diagnostics.Append(resp.Identity.Set(ctx, idIdentity{ID: id})...)
	}
	resp.State.RemoveResource(ctx)
}

// addCreateError reports a failed create. A 409 most likely means that the
// name is taken, since the API fetches an object by its name too
// (https://docs.mistral.ai/studio/conversations/chat-completion/prompt-registry):
// the diagnostic then points to an import block rather than adopting the
// existing object.
func addCreateError(diags *diag.Diagnostics, noun, name string, err error) {
	if client.IsConflict(err) {
		diags.AddAttributeError(path.Root("name"), fmt.Sprintf("A %s named %q already exists", noun, name),
			fmt.Sprintf("Import it with an import block to manage it with Terraform, or choose another name. The API answered: %v", err))
		return
	}
	diags.AddError("Cannot create the "+noun, err.Error())
}

// idIdentitySchema is the identity of a type imported by its ID alone.
func idIdentitySchema(desc string) identityschema.Schema {
	return identityschema.Schema{
		Attributes: map[string]identityschema.Attribute{
			"id": identityschema.StringAttribute{RequiredForImport: true, Description: desc},
		},
	}
}

// nameAttribute is the name of a prompt or a skill, which no update takes.
func nameAttribute(noun string) schema.StringAttribute {
	return schema.StringAttribute{
		Required: true,
		MarkdownDescription: fmt.Sprintf("The name of the %s. The API cannot rename a %s: changing it replaces the %s, "+
			"and the new one starts again from a first version.", noun, noun, noun),
		PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
		Validators:    []validator.String{stringvalidator.LengthAtLeast(1)},
	}
}

// notesAttribute is the notes of the latest version. definition names what
// a version holds, for the description.
func notesAttribute(definition string) schema.StringAttribute {
	return schema.StringAttribute{
		Optional: true,
		MarkdownDescription: fmt.Sprintf("Notes on the latest version. A change of the %s sends them with the new version; "+
			"a change of the notes alone updates the notes of the latest version and creates no version.", definition),
		Validators: []validator.String{stringvalidator.LengthAtLeast(1)},
	}
}

// createdAtAttribute is the creation time, which never changes.
func createdAtAttribute(noun string) schema.StringAttribute {
	return schema.StringAttribute{
		Computed:            true,
		MarkdownDescription: fmt.Sprintf("When the %s was created, in RFC 3339 format.", noun),
		PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
	}
}

// updatedAtAttribute is the time of the last change, unknown on every update.
func updatedAtAttribute(noun string) schema.StringAttribute {
	return schema.StringAttribute{
		Computed:            true,
		MarkdownDescription: fmt.Sprintf("When the %s was last updated, in RFC 3339 format.", noun),
	}
}

// nonEmpty maps a string the API returns to the state: the API omits an
// empty field, so "" and an absent field both read as null, and an optional
// argument refuses "" (LengthAtLeast(1)) so the two never fight.
func nonEmpty(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}

// timestamp writes a time in RFC 3339 format, null when the API left it out.
func timestamp(t time.Time) types.String {
	if t.IsZero() {
		return types.StringNull()
	}
	return types.StringValue(t.UTC().Format(time.RFC3339))
}
