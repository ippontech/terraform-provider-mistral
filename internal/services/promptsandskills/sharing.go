// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package promptsandskills

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// The sharing settings of a registry object: RegistrySharingScope and
// ShareRelation in the specification. Their unspecified values
// (sharing_scope_unspecified, share_relation_unspecified) are the protobuf
// zero values, never sent.
const (
	scopePrivate   = "private"
	scopeWorkspace = "workspace"
	relationReader = "reader"
	relationWriter = "writer"
)

// sharingAttributes returns sharing_scope and workspace_relation, the same
// for a prompt and a skill. noun names the object in the descriptions.
func sharingAttributes(noun string) map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"sharing_scope": schema.StringAttribute{
			Optional: true,
			Computed: true,
			// The specification gives no default. A protobuf API reads an
			// absent scope as unspecified, which we read as private; the
			// state mapping writes private for it too.
			Default: stringdefault.StaticString(scopePrivate),
			MarkdownDescription: fmt.Sprintf("Who can use the %s: `private` (the default) keeps it unshared, `workspace` shares it with every member of the workspace.",
				noun),
			Validators: []validator.String{stringvalidator.OneOf(scopePrivate, scopeWorkspace)},
		},
		"workspace_relation": schema.StringAttribute{
			Optional: true,
			Computed: true,
			MarkdownDescription: "The access every member of the workspace gets when `sharing_scope` is `workspace`: `writer` (the default) or `reader`. " +
				"It is refused with any other scope. The API does not return it, so the state keeps the configured value and an import leaves it unset.",
			Validators:    []validator.String{stringvalidator.OneOf(relationReader, relationWriter)},
			PlanModifiers: []planmodifier.String{relationDefault{}},
		},
	}
}

// relationDefault plans workspace_relation when the configuration leaves it
// out: writer with the workspace scope, as the specification documents
// ("Defaults to `writer`"), and null with any other scope, which the API
// refuses a relation for. A schema Default cannot depend on another
// attribute.
type relationDefault struct{}

// Description says what the modifier plans.
func (relationDefault) Description(context.Context) string {
	return "writer when sharing_scope is workspace, null otherwise"
}

// MarkdownDescription is Description.
func (m relationDefault) MarkdownDescription(ctx context.Context) string { return m.Description(ctx) }

// PlanModifyString plans the default relation for the planned scope.
func (relationDefault) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if !req.ConfigValue.IsNull() {
		return
	}
	var scope types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("sharing_scope"), &scope)...)
	switch {
	case scope.IsUnknown():
		// Terraform plans again at apply time, with the scope known.
		resp.PlanValue = types.StringUnknown()
	case scope.ValueString() == scopeWorkspace:
		resp.PlanValue = types.StringValue(relationWriter)
	default:
		resp.PlanValue = types.StringNull()
	}
}

// validateSharing refuses a workspace_relation without the workspace scope,
// which the API rejects ("Rejected unless sharing_scope is `workspace`"). A
// scope unknown at plan time is checked by the API at apply time.
func validateSharing(ctx context.Context, config tfsdk.Config, diags *diag.Diagnostics) {
	var scope, relation types.String
	diags.Append(config.GetAttribute(ctx, path.Root("sharing_scope"), &scope)...)
	diags.Append(config.GetAttribute(ctx, path.Root("workspace_relation"), &relation)...)
	if relation.IsNull() || scope.IsUnknown() || scope.ValueString() == scopeWorkspace {
		return
	}
	diags.AddAttributeError(path.Root("workspace_relation"), "Unexpected workspace_relation",
		`workspace_relation applies to sharing_scope = "workspace" only.`)
}

// relationRequest is the workspaceRelation to send: the planned relation
// with the workspace scope, and nothing with another scope, for which the
// API rejects a relation.
func relationRequest(scope, relation types.String) *string {
	if scope.ValueString() != scopeWorkspace {
		return nil
	}
	return relation.ValueStringPointer()
}

// narrowsSharing reports whether the planned sharing grants less access than
// the prior one, which decides the order of an update (versionedUpdate.apply):
// sharing_scope from workspace to private, or workspace_relation from writer
// to reader with the workspace scope.
//
// The second one hides the new definition from no one, since a reader reads
// it too: updating the object first only withdraws the write access a moment
// earlier, and a version another member creates meanwhile already fails the
// apply (notLatest). It counts as narrowing anyway, which costs nothing.
func narrowsSharing(plannedScope, plannedRelation, priorScope, priorRelation types.String) bool {
	return sharingAccess(plannedScope, plannedRelation) < sharingAccess(priorScope, priorRelation)
}

// sharingAccess ranks sharing settings by the access they grant: 0 private,
// 1 the workspace as reader, 2 the workspace as writer. A null relation with
// the workspace scope ranks as writer: it is the API's default ("Defaults to
// `writer`"), and the state holds it after an import, the API not returning
// the relation. A scope this provider does not know, which only a read can
// bring into the state, ranks above all: leaving it counts as narrowing, so
// the new definition never reaches an audience this provider cannot rank.
// Were that scope narrower, the earlier definition would only reach the
// planned audience a moment before the new one.
func sharingAccess(scope, relation types.String) int {
	switch scope.ValueString() {
	case scopePrivate:
		return 0
	case scopeWorkspace:
		if relation.ValueString() == relationReader {
			return 1
		}
		return 2
	default:
		return 3
	}
}

// scopeState maps the scope the API returns. An absent or unspecified scope
// reads as private, the schema default; any other value is kept as
// returned, so a scope this provider does not know shows as drift.
func scopeState(scope string) types.String {
	if scope == "" || scope == "sharing_scope_unspecified" {
		return types.StringValue(scopePrivate)
	}
	return types.StringValue(scope)
}

// relationState is workspace_relation after a read: the API does not return
// it, so the prior value is kept while the scope stays workspace, and it is
// null with any other scope.
func relationState(scope types.String, prior types.String) types.String {
	if scope.ValueString() != scopeWorkspace {
		return types.StringNull()
	}
	return prior
}
