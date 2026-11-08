// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package promptsandskills

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
)

// stubUpdate is a versionedUpdate whose calls record their names in calls
// and fail when named failing. Otherwise a new version answers created, and
// every other call an object at version latest.
func stubUpdate(c changes, created client.CreatedVersion, latest int64, failing string) (versionedUpdate[int64], *[]string) {
	var calls []string
	answer := func(name string) (*int64, error) {
		calls = append(calls, name)
		if name == failing {
			return nil, errors.New(name + " failed")
		}
		return &latest, nil
	}
	return versionedUpdate[int64]{
		noun: "prompt", parentFields: "the title, description and sharing of the prompt", changes: c, version: 1,
		newVersion: func(context.Context) (*client.CreatedVersion, error) {
			calls = append(calls, "newVersion")
			if failing == "newVersion" {
				return nil, errors.New("newVersion failed")
			}
			return &created, nil
		},
		setNotes:     func(context.Context) (*int64, error) { return answer("setNotes") },
		updateParent: func(context.Context) (*int64, error) { return answer("updateParent") },
		get:          func(context.Context) (*int64, error) { return answer("get") },
		versionOf:    func(v *int64) int64 { return *v },
	}, &calls
}

// The calls of an update, in order, in both orders, and the diagnostic of
// each failing one: its summary, then what the calls before it applied and
// the writes after it, which are not attempted.
func TestVersionedUpdateOrdersItsCalls(t *testing.T) {
	t.Parallel()
	all := changes{definition: true, notes: true, parent: true}
	notesAndParent := changes{notes: true, parent: true}
	created := client.CreatedVersion{Version: 2}
	const (
		appliedVersion = "\n\nAlready applied: version 2, with the planned definition."
		appliedParent  = "\n\nAlready applied: the title, description and sharing of the prompt."
	)
	cases := map[string]struct {
		changes    changes
		narrowing  bool
		created    client.CreatedVersion
		failing    string
		wantCalls  string
		wantError  string // the summary of the error, "" for none
		wantDetail string
	}{
		"version then prompt then read": {changes: all, created: created, wantCalls: "newVersion updateParent get"},
		"notes then prompt":             {changes: notesAndParent, wantCalls: "setNotes updateParent"},
		"prompt alone":                  {changes: changes{parent: true}, wantCalls: "updateParent"},
		"nothing but a read":            {changes: changes{}, wantCalls: "get"},
		"failed version": {
			changes: all, created: created, failing: "newVersion", wantCalls: "newVersion",
			wantError:  "Cannot create a version of the prompt",
			wantDetail: "newVersion failed\n\nNot attempted: the title, description and sharing of the prompt.",
		},
		"failed notes": {
			changes: changes{notes: true}, failing: "setNotes", wantCalls: "setNotes",
			wantError: "Cannot update the notes of version 1 of the prompt", wantDetail: "setNotes failed",
		},
		"failed prompt after its version": {
			changes: all, created: created, failing: "updateParent", wantCalls: "newVersion updateParent",
			wantError: "Cannot update the prompt", wantDetail: "updateParent failed" + appliedVersion,
		},
		"failed prompt after the notes": {
			changes: notesAndParent, failing: "updateParent", wantCalls: "setNotes updateParent",
			wantError: "Cannot update the prompt", wantDetail: "updateParent failed\n\nAlready applied: the notes of version 1.",
		},
		"failed read": {
			changes: changes{definition: true}, created: created, failing: "get", wantCalls: "newVersion get",
			wantError: "Cannot read the prompt", wantDetail: "get failed" + appliedVersion,
		},
		"a concurrent version": {
			changes: changes{definition: true}, created: client.CreatedVersion{Version: 1}, wantCalls: "newVersion get",
			wantError:  "The prompt did not get a new latest version",
			wantDetail: notLatest("prompt", client.CreatedVersion{Version: 1}, 2) + "\n\nAlready applied: version 1, with the planned definition.",
		},
		// A deduplicated definition changes nothing, so only the prompt
		// shows as applied.
		"a deduplicated version": {
			changes: changes{definition: true, parent: true}, created: client.CreatedVersion{Version: 1, Deduplicated: true},
			wantCalls: "newVersion updateParent get", wantError: "The prompt did not get a new latest version",
			wantDetail: notLatest("prompt", client.CreatedVersion{Version: 1, Deduplicated: true}, 2) + appliedParent,
		},
		"narrowing: prompt then version then read": {changes: all, narrowing: true, created: created, wantCalls: "updateParent newVersion get"},
		"narrowing: prompt then notes":             {changes: notesAndParent, narrowing: true, wantCalls: "updateParent setNotes"},
		"narrowing without a change of the prompt": {changes: changes{definition: true}, narrowing: true, created: created, wantCalls: "newVersion get"},
		"narrowing: failed prompt": {
			changes: all, narrowing: true, created: created, failing: "updateParent", wantCalls: "updateParent",
			wantError:  "Cannot update the prompt",
			wantDetail: "updateParent failed\n\nNot attempted: a new version with the planned definition.",
		},
		"narrowing: failed prompt before the notes": {
			changes: notesAndParent, narrowing: true, failing: "updateParent", wantCalls: "updateParent",
			wantError: "Cannot update the prompt", wantDetail: "updateParent failed\n\nNot attempted: the notes of version 1.",
		},
		"narrowing: failed version": {
			changes: all, narrowing: true, created: created, failing: "newVersion", wantCalls: "updateParent newVersion",
			wantError: "Cannot create a version of the prompt", wantDetail: "newVersion failed" + appliedParent,
		},
		"narrowing: failed notes": {
			changes: notesAndParent, narrowing: true, failing: "setNotes", wantCalls: "updateParent setNotes",
			wantError: "Cannot update the notes of version 1 of the prompt", wantDetail: "setNotes failed" + appliedParent,
		},
		"narrowing: failed read": {
			changes: all, narrowing: true, created: created, failing: "get", wantCalls: "updateParent newVersion get",
			wantError:  "Cannot read the prompt",
			wantDetail: "get failed\n\nAlready applied: the title, description and sharing of the prompt; version 2, with the planned definition.",
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			u, calls := stubUpdate(c.changes, c.created, 2, c.failing)
			u.narrowsSharing = c.narrowing
			var diags diag.Diagnostics
			got := u.apply(context.Background(), &diags)
			if strings.Join(*calls, " ") != c.wantCalls {
				t.Errorf("calls = %v, want %s", *calls, c.wantCalls)
			}
			if c.wantError == "" {
				if diags.HasError() || got == nil || *got != 2 {
					t.Errorf("apply = %v, %v", got, diags)
				}
				return
			}
			if got != nil || len(diags) != 1 || !diags.HasError() {
				t.Fatalf("apply = %v, %v, want the error %q", got, diags, c.wantError)
			}
			if s := diags[0].Summary(); s != c.wantError {
				t.Errorf("summary = %q, want %q", s, c.wantError)
			}
			if d := diags[0].Detail(); d != c.wantDetail {
				t.Errorf("detail = %q, want %q", d, c.wantDetail)
			}
		})
	}
}

// The planned sharing narrows the prior one when it grants less access:
// private below the workspace as reader, below the workspace as writer, below
// a scope this provider does not know.
func TestNarrowsSharing(t *testing.T) {
	t.Parallel()
	null := types.StringNull()
	private, workspace := types.StringValue(scopePrivate), types.StringValue(scopeWorkspace)
	reader, writer := types.StringValue(relationReader), types.StringValue(relationWriter)
	unknownScope := types.StringValue("organization")
	type sharing struct{ scope, relation types.String }
	cases := map[string]struct {
		planned, prior sharing
		want           bool
	}{
		"workspace to private":              {sharing{private, null}, sharing{workspace, writer}, true},
		"workspace readers to private":      {sharing{private, null}, sharing{workspace, reader}, true},
		"writer to reader":                  {sharing{workspace, reader}, sharing{workspace, writer}, true},
		"imported relation to reader":       {sharing{workspace, reader}, sharing{workspace, null}, true},
		"an unknown scope to the workspace": {sharing{workspace, writer}, sharing{unknownScope, null}, true},
		"an unknown scope to private":       {sharing{private, null}, sharing{unknownScope, null}, true},
		"private to workspace":              {sharing{workspace, writer}, sharing{private, null}, false},
		"private to workspace readers":      {sharing{workspace, reader}, sharing{private, null}, false},
		"reader to writer":                  {sharing{workspace, writer}, sharing{workspace, reader}, false},
		"imported relation to writer":       {sharing{workspace, writer}, sharing{workspace, null}, false},
		"private kept":                      {sharing{private, null}, sharing{private, null}, false},
		"workspace readers kept":            {sharing{workspace, reader}, sharing{workspace, reader}, false},
		"workspace writers kept":            {sharing{workspace, writer}, sharing{workspace, writer}, false},
	}
	for name, c := range cases {
		if got := narrowsSharing(c.planned.scope, c.planned.relation, c.prior.scope, c.prior.relation); got != c.want {
			t.Errorf("%s: narrowsSharing = %v, want %v", name, got, c.want)
		}
	}
}

// The diagnostic of a version that is not the latest names its cause.
func TestNotLatestExplainsBothCauses(t *testing.T) {
	t.Parallel()
	if got := notLatest("skill", client.CreatedVersion{Version: 1, Deduplicated: true}, 3); !strings.Contains(got, "matched this definition to version 1") || !strings.Contains(got, "stays 3") {
		t.Errorf("deduplicated: %s", got)
	}
	if got := notLatest("skill", client.CreatedVersion{Version: 3}, 4); !strings.Contains(got, "latest version of the skill is now 4") {
		t.Errorf("concurrent: %s", got)
	}
}

// An asset keeps its prior attribute and text when the API returns the same
// bytes in another form, and reads as returned otherwise.
func TestAssetState(t *testing.T) {
	t.Parallel()
	text := func(s string) *assetModel {
		return &assetModel{Content: types.StringValue(s), ContentBase64: types.StringNull()}
	}
	raw := func(s string) *assetModel {
		return &assetModel{Content: types.StringNull(), ContentBase64: types.StringValue(s)}
	}
	cases := map[string]struct {
		asset              client.SkillAsset
		prior              *assetModel
		wantText, wantBase string // "-" for null
	}{
		"text kept":                    {client.SkillAsset{TextContent: new("hi")}, text("hi"), "hi", "-"},
		"text answered as bytes":       {client.SkillAsset{RawContent: new("aGk=")}, text("hi"), "hi", "-"},
		"bytes answered as text":       {client.SkillAsset{TextContent: new("hi")}, raw("aGk="), "-", "aGk="},
		"another alphabet":             {client.SkillAsset{RawContent: new("-_8")}, raw("+/8="), "-", "+/8="},
		"changed bytes":                {client.SkillAsset{RawContent: new("AAA=")}, raw("+/8="), "-", "AAA="},
		"changed text":                 {client.SkillAsset{TextContent: new("new")}, text("old"), "new", "-"},
		"no prior":                     {client.SkillAsset{RawContent: new("-_8")}, nil, "-", "-_8"},
		"neither field":                {client.SkillAsset{}, nil, "", "-"},
		"neither field, prior bytes":   {client.SkillAsset{}, raw(""), "-", ""},
		"bytes that are not base64":    {client.SkillAsset{RawContent: new("!!")}, text("!!"), "-", "!!"},
		"a prior that is not base64":   {client.SkillAsset{RawContent: new("aGk=")}, raw("!!"), "-", "aGk="},
		"executable from the response": {client.SkillAsset{TextContent: new("x"), IsExecutable: true}, text("x"), "x", "-"},
	}
	for name, c := range cases {
		got := assetState(c.asset, c.prior)
		if s := orDash(got.Content); s != c.wantText {
			t.Errorf("%s: content = %s, want %s", name, s, c.wantText)
		}
		if s := orDash(got.ContentBase64); s != c.wantBase {
			t.Errorf("%s: content_base64 = %s, want %s", name, s, c.wantBase)
		}
		if got.Executable.ValueBool() != c.asset.IsExecutable {
			t.Errorf("%s: executable = %s", name, got.Executable)
		}
	}
}

// orDash is the value of s, or "-" when it is null.
func orDash(s types.String) string {
	if s.IsNull() {
		return "-"
	}
	return s.ValueString()
}

// The mapping of the scope and the relation, and the relation a request
// sends.
func TestSharingState(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{"": "private", "sharing_scope_unspecified": "private", "workspace": "workspace", "organization": "organization"} {
		if got := scopeState(in).ValueString(); got != want {
			t.Errorf("scopeState(%q) = %s, want %s", in, got, want)
		}
	}
	if got := relationState(types.StringValue(scopePrivate), types.StringValue(relationReader)); !got.IsNull() {
		t.Errorf("a private object has no relation: %s", got)
	}
	if got := relationRequest(types.StringValue(scopeWorkspace), types.StringValue(relationReader)); got == nil || *got != relationReader {
		t.Errorf("relationRequest = %v", got)
	}
}

// content_base64 accepts each alphabet a protobuf API reads, and an unknown
// value until the apply.
func TestBase64Validator(t *testing.T) {
	t.Parallel()
	v := base64String{}
	ctx := context.Background()
	for value, wantErr := range map[string]bool{"iVBORw0KGgo=": false, "-_8": false, "": false, "not base64!": true} {
		resp := &validator.StringResponse{}
		v.ValidateString(ctx, validator.StringRequest{Path: path.Root("p"), ConfigValue: types.StringValue(value)}, resp)
		if resp.Diagnostics.HasError() != wantErr {
			t.Errorf("%q: error = %v, want %v", value, resp.Diagnostics, wantErr)
		}
	}
	resp := &validator.StringResponse{}
	v.ValidateString(ctx, validator.StringRequest{ConfigValue: types.StringUnknown()}, resp)
	if resp.Diagnostics.HasError() {
		t.Error("an unknown value is checked at apply time")
	}
}

// The descriptions of the plan modifiers and validators, which the
// framework shows in its logs and diagnostics.
func TestModifierDescriptions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	type described interface {
		Description(context.Context) string
		MarkdownDescription(context.Context) string
	}
	for _, d := range []described{relationDefault{}, versionPlan{}, base64String{}} {
		if d.Description(ctx) == "" || d.MarkdownDescription(ctx) != d.Description(ctx) {
			t.Errorf("%T: descriptions %q and %q", d, d.Description(ctx), d.MarkdownDescription(ctx))
		}
	}
}

// Times are written in UTC, in RFC 3339 format, and a zero time as null.
func TestTimestamp(t *testing.T) {
	t.Parallel()
	if !timestamp(time.Time{}).IsNull() {
		t.Error("a zero time must be null")
	}
	paris := time.FixedZone("CET", 3600)
	if got := timestamp(time.Date(2026, 10, 29, 11, 0, 0, 0, paris)).ValueString(); got != "2026-10-29T10:00:00Z" {
		t.Errorf("timestamp = %s, want UTC", got)
	}
}
