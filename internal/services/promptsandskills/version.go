// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package promptsandskills

import (
	"context"
	"fmt"
	"strings"

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

// copyParent writes the parent attributes of the plan into state, which holds
// the prior state during an Update, and leaves the others as they are. Update
// calls it through versionedUpdate.recordParent once the update of the parent
// succeeded, so that the state an error leaves holds what the API then has:
// workspace_relation above all, which no answer holds and a refresh keeps
// from the state. The plan values are known at apply time: each parent
// attribute is configured, or planned from the configuration
// (relationDefault, the default of sharing_scope).
func (l layout) copyParent(ctx context.Context, plan tfsdk.Plan, state *tfsdk.State) diag.Diagnostics {
	var diags diag.Diagnostics
	for _, p := range l.parent {
		var planned attr.Value
		diags.Append(plan.GetAttribute(ctx, p, &planned)...)
		diags.Append(state.SetAttribute(ctx, p, planned)...)
	}
	return diags
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
	noun string
	// parentFields names in the diagnostics what updateParent sets, such as
	// "the sharing of the skill".
	parentFields string
	changes      changes
	// narrowsSharing is narrowsSharing of the plan and the state: the
	// update of the parent then comes first.
	narrowsSharing bool
	// version is the latest version, from the state: the one whose notes a
	// notes-only change updates.
	version      int64
	newVersion   func(context.Context) (*client.CreatedVersion, error)
	setNotes     func(context.Context) (*T, error)
	updateParent func(context.Context) (*T, error)
	// recordParent writes into the state of the Update what updateParent
	// applied (layout.copyParent). apply calls it as soon as updateParent
	// succeeds.
	recordParent func(context.Context) diag.Diagnostics
	get          func(context.Context) (*T, error)
	versionOf    func(*T) int64
}

// write is one of the writes of an update.
type write int

// The writes of an update. An update makes at most two: a new version or the
// notes, and the update of the parent.
const (
	writeVersion write = iota
	writeNotes
	writeParent
)

// updateProgress is what the writes of one apply returned and applied.
type updateProgress[T any] struct {
	// created is the answer to a new version, nil without one.
	created *client.CreatedVersion
	// got is the object the last write answered, nil when none did, and
	// then the object the read after the writes answered.
	got *T
	// applied names what the writes changed, in order, for the diagnostic of
	// a later failure.
	applied []string
}

// apply makes the writes of the update and returns the object to write in
// the state, or nil after an error diagnostic. The writes come in one of two
// orders (writes).
//
// By default the version, or the notes, comes first: a new version is the
// call that cannot be replayed and the one most likely to be refused (the
// content the user wrote), and the update of the parent, second, resends its
// whole body when an apply repeats it. A wider sharing then applies only once
// the new definition is in place, and a failed update of the parent leaves
// the object shared no wider than before.
//
// When the plan narrows the sharing (narrowsSharing), the update of the
// parent comes first, and when it fails nothing else is sent. In the default
// order the new definition would be the latest version, the one a read
// returns, while the members the plan excludes can still read it, and would
// stay so if the update of the parent failed: a PATCH is not replayed after a
// 5xx (internal/client/retry.go). This rests on our reading of the API: the
// sharing belongs to the object, whatever the version, and a read returns the
// latest version.
//
// A new version is followed by a read of the latest version, which the
// answer to POST .../versions does not hold, and the read must return that
// version (notLatest).
//
// When a call fails, the diagnostic names what the writes before it applied
// and the writes not attempted, and the state keeps its prior value (the
// framework starts Update from it), with one exception: a successful update
// of the parent is written into it at once (recordParent), whatever fails
// next. The refresh of the next plan reads the rest of what was applied, and
// the plan then holds only what is left to do. The parent cannot wait for the
// refresh: workspace_relation is in no answer, so a refresh keeps it from the
// state, and a prior relation left there would hide the one the API now has.
// A configuration set back to the prior relation would then plan no change.
func (u versionedUpdate[T]) apply(ctx context.Context, diags *diag.Diagnostics) *T {
	var p updateProgress[T]
	writes := u.writes()
	for i, w := range writes {
		if summary, err := u.send(ctx, w, &p); err != nil {
			diags.AddError(summary, err.Error()+u.progress(p.applied, writes[i+1:]))
			return nil
		}
		if w != writeParent {
			continue
		}
		if diags.Append(u.recordParent(ctx)...); diags.HasError() {
			return nil
		}
	}
	if p.created != nil || p.got == nil {
		got, err := u.get(ctx)
		if err != nil {
			diags.AddError("Cannot read the "+u.noun, err.Error()+u.progress(p.applied, nil))
			return nil
		}
		p.got = got
	}
	if p.created != nil && u.versionOf(p.got) != p.created.Version {
		diags.AddError("The "+u.noun+" did not get a new latest version",
			notLatest(u.noun, *p.created, u.versionOf(p.got))+u.progress(p.applied, nil))
		return nil
	}
	return p.got
}

// writes returns the writes the changes call for, in the order apply
// documents: the parent first when the sharing narrows, last otherwise.
func (u versionedUpdate[T]) writes() []write {
	var writes []write
	switch {
	case u.changes.definition:
		writes = append(writes, writeVersion)
	case u.changes.notes:
		writes = append(writes, writeNotes)
	}
	switch {
	case !u.changes.parent:
		return writes
	case u.narrowsSharing:
		return append([]write{writeParent}, writes...)
	default:
		return append(writes, writeParent)
	}
}

// send makes one write and records in p what it returned and applied. On
// failure, it returns the summary of the diagnostic with the error.
func (u versionedUpdate[T]) send(ctx context.Context, w write, p *updateProgress[T]) (string, error) {
	var err error
	switch w {
	case writeVersion:
		if p.created, err = u.newVersion(ctx); err != nil {
			return "Cannot create a version of the " + u.noun, err
		}
		// A deduplicated definition changes nothing: Mistral answers a
		// version it already holds.
		if !p.created.Deduplicated {
			p.applied = append(p.applied, fmt.Sprintf("version %d, with the planned definition", p.created.Version))
		}
	case writeNotes:
		if p.got, err = u.setNotes(ctx); err != nil {
			return fmt.Sprintf("Cannot update the notes of version %d of the %s", u.version, u.noun), err
		}
		p.applied = append(p.applied, u.label(w))
	default:
		if p.got, err = u.updateParent(ctx); err != nil {
			return "Cannot update the " + u.noun, err
		}
		p.applied = append(p.applied, u.label(w))
	}
	return "", nil
}

// label names what a write changes, for the diagnostics.
func (u versionedUpdate[T]) label(w write) string {
	switch w {
	case writeVersion:
		return "a new version with the planned definition"
	case writeNotes:
		return fmt.Sprintf("the notes of version %d", u.version)
	default:
		return u.parentFields
	}
}

// progress ends the diagnostic of a failed call: what the writes before it
// applied, which a refresh reads, and the writes after it, which were not
// attempted.
func (u versionedUpdate[T]) progress(applied []string, skipped []write) string {
	var b strings.Builder
	if len(applied) > 0 {
		b.WriteString("\n\nAlready applied: " + strings.Join(applied, "; ") + ".")
	}
	if len(skipped) > 0 {
		names := make([]string, 0, len(skipped))
		for _, w := range skipped {
			names = append(names, u.label(w))
		}
		b.WriteString("\n\nNot attempted: " + strings.Join(names, "; ") + ".")
	}
	return b.String()
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
