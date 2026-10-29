// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package promptsandskills

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
)

// notesPath is the attribute that holds the notes of the latest version.
var notesPath = path.Root("notes")

// layout says which attributes of a type belong to its versions and which to
// the object itself, which decides the calls of an update: a change of the
// definition creates a version, a change of the notes alone updates the
// notes of the latest version, and a change of a parent attribute updates
// the object (the request bodies of the specification draw this line).
type layout struct {
	definition []path.Path
	parent     []path.Path
}

// changes are the parts of an object an update changes.
type changes struct {
	definition, notes, parent bool
}

// changesOf compares the plan with the state, attribute by attribute.
func (l layout) changesOf(ctx context.Context, plan tfsdk.Plan, state tfsdk.State) (changes, diag.Diagnostics) {
	var c changes
	var diags diag.Diagnostics
	c.definition = anyChanged(ctx, plan, state, l.definition, &diags)
	c.notes = anyChanged(ctx, plan, state, []path.Path{notesPath}, &diags)
	c.parent = anyChanged(ctx, plan, state, l.parent, &diags)
	return c, diags
}

// anyChanged reports whether the plan holds another value than the state at
// one of paths. A value unknown in the plan counts as a change.
func anyChanged(ctx context.Context, plan tfsdk.Plan, state tfsdk.State, paths []path.Path, diags *diag.Diagnostics) bool {
	for _, p := range paths {
		var planned, prior attr.Value
		diags.Append(plan.GetAttribute(ctx, p, &planned)...)
		diags.Append(state.GetAttribute(ctx, p, &prior)...)
		if diags.HasError() {
			return false
		}
		if !planned.Equal(prior) {
			return true
		}
	}
	return false
}

// versionPlan plans the computed version: unknown when the definition
// changes, since the update then creates a version, and the version of the
// state otherwise, so that changing a title or the sharing does not show the
// version as changing.
type versionPlan struct {
	definition []path.Path
}

// Description says when the version is planned as changing.
func (versionPlan) Description(context.Context) string {
	return "unknown when the definition changes, the prior version otherwise"
}

// MarkdownDescription is Description.
func (m versionPlan) MarkdownDescription(ctx context.Context) string { return m.Description(ctx) }

// PlanModifyInt64 keeps the prior version unless the definition changes. On
// a create, the state is null and the version stays unknown.
func (m versionPlan) PlanModifyInt64(ctx context.Context, req planmodifier.Int64Request, resp *planmodifier.Int64Response) {
	if req.StateValue.IsNull() {
		return
	}
	if !anyChanged(ctx, req.Plan, req.State, m.definition, &resp.Diagnostics) && !resp.Diagnostics.HasError() {
		resp.PlanValue = req.StateValue
	}
}

// versionedUpdate holds the calls of one update of a prompt or a skill, T
// being its client type, and makes them in the order apply documents.
type versionedUpdate[T any] struct {
	// noun names the object in the diagnostics: "prompt" or "skill".
	noun    string
	changes changes
	// version is the latest version, from the state: the one whose notes a
	// notes-only change updates.
	version      int64
	newVersion   func(context.Context) (*client.CreatedVersion, error)
	setNotes     func(context.Context) (*T, error)
	updateParent func(context.Context) (*T, error)
	get          func(context.Context) (*T, error)
	versionOf    func(*T) int64
}

// apply makes the writes of the update and returns the object to write in
// the state, or nil after an error diagnostic.
//
// The version comes first: it is the call that cannot be replayed (a new
// version) and the one most likely to be refused (the content the user
// wrote). The parent update comes second, and resends its whole body when an
// apply repeats it. When a call fails, the state keeps its prior value, and
// the refresh of the next plan reads what the calls before it changed: the
// plan then holds only what is left to do.
//
// A new version is followed by a read of the latest version, which the
// answer to POST .../versions does not hold.
func (u versionedUpdate[T]) apply(ctx context.Context, diags *diag.Diagnostics) *T {
	var created *client.CreatedVersion
	var got *T
	var err error
	switch {
	case u.changes.definition:
		if created, err = u.newVersion(ctx); err != nil {
			diags.AddError("Cannot create a version of the "+u.noun, err.Error())
			return nil
		}
	case u.changes.notes:
		if got, err = u.setNotes(ctx); err != nil {
			diags.AddError(fmt.Sprintf("Cannot update the notes of version %d of the %s", u.version, u.noun), err.Error())
			return nil
		}
	}
	if u.changes.parent {
		if got, err = u.updateParent(ctx); err != nil {
			diags.AddError("Cannot update the "+u.noun, err.Error())
			return nil
		}
	}
	if created != nil || got == nil {
		if got, err = u.get(ctx); err != nil {
			diags.AddError("Cannot read the "+u.noun, err.Error())
			return nil
		}
	}
	if created != nil && u.versionOf(got) != created.Version {
		diags.AddError("The "+u.noun+" did not get a new latest version", notLatest(u.noun, *created, u.versionOf(got)))
		return nil
	}
	return got
}

// notLatest explains why the version an update created is not the latest
// one, the version a read returns. Writing the state from that read would
// hold another definition than the plan.
func notLatest(noun string, created client.CreatedVersion, latest int64) string {
	if created.Deduplicated {
		return fmt.Sprintf("Mistral matched this definition to version %d, which it already holds, and the latest version stays %d, "+
			"with another definition. The API cannot make an earlier definition the latest one again: "+
			"change the definition, even slightly, so that Mistral creates a new version.", created.Version, latest)
	}
	return fmt.Sprintf("Mistral created version %d, but the latest version of the %s is now %d: "+
		"another client changed it during the apply. Apply again to write the configuration back.", created.Version, noun, latest)
}

// versionAttributeDescription describes the computed version of a type
// whose definition is held by the attributes named in what.
func versionAttributeDescription(noun, what string) string {
	return fmt.Sprintf("The latest version of the %s, the one Mistral returns when no version is asked for. "+
		"A change of %s creates a new version; any other change keeps it.", noun, what)
}
