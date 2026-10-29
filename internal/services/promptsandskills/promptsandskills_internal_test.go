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
		noun: "prompt", changes: c, version: 1,
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

// The calls of an update, in order, and the diagnostic of each failing one.
func TestVersionedUpdateOrdersItsCalls(t *testing.T) {
	t.Parallel()
	all := changes{definition: true, notes: true, parent: true}
	cases := map[string]struct {
		changes   changes
		created   client.CreatedVersion
		failing   string
		wantCalls string
		wantError string
	}{
		"version then prompt then read": {all, client.CreatedVersion{Version: 2}, "", "newVersion updateParent get", ""},
		"notes then prompt":             {changes{notes: true, parent: true}, client.CreatedVersion{}, "", "setNotes updateParent", ""},
		"prompt alone":                  {changes{parent: true}, client.CreatedVersion{}, "", "updateParent", ""},
		"nothing but a read":            {changes{}, client.CreatedVersion{}, "", "get", ""},
		"failed version":                {all, client.CreatedVersion{Version: 2}, "newVersion", "newVersion", "Cannot create a version of the prompt"},
		"failed notes":                  {changes{notes: true}, client.CreatedVersion{}, "setNotes", "setNotes", "Cannot update the notes of version 1 of the prompt"},
		"failed read":                   {changes{definition: true}, client.CreatedVersion{Version: 2}, "get", "newVersion get", "Cannot read the prompt"},
		"a concurrent version":          {changes{definition: true}, client.CreatedVersion{Version: 1}, "", "newVersion get", "did not get a new latest version"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			u, calls := stubUpdate(c.changes, c.created, 2, c.failing)
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
			if got != nil || !diags.HasError() || !strings.Contains(diags.Errors()[0].Summary(), c.wantError) {
				t.Errorf("apply = %v, %v, want the error %q", got, diags, c.wantError)
			}
		})
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
