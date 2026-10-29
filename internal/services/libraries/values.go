// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package libraries

import (
	"context"
	"encoding/json"
	"regexp"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// noColon matches an ID that can be one part of a composite import ID, which
// joins its parts with colons.
var noColon = regexp.MustCompile(`^[^:]+$`)

// removeResource removes the resource of a Read from the state, after
// setting identity, its resource identity read from the state.
func removeResource(ctx context.Context, resp *resource.ReadResponse, identity any) {
	// Set the identity before removing: during an import, terraform-plugin-framework v1.19.0 fails a Read that removes the resource without one.
	if resp.Identity != nil {
		resp.Diagnostics.Append(resp.Identity.Set(ctx, identity)...)
	}
	resp.State.RemoveResource(ctx)
}

// timestamp writes a date of the API as RFC 3339 in UTC, null for a zero
// time.
func timestamp(t time.Time) types.String {
	if t.IsZero() {
		return types.StringNull()
	}
	return types.StringValue(t.UTC().Format(time.RFC3339))
}

// optionalTimestamp is timestamp for a date the API may leave null.
func optionalTimestamp(t *time.Time) types.String {
	if t == nil {
		return types.StringNull()
	}
	return timestamp(*t)
}

// nonEmpty reads an absent or empty string as null: the schema refuses an
// empty value, so "" and null never alternate in the state.
func nonEmpty(s *string) types.String {
	if s == nil || *s == "" {
		return types.StringNull()
	}
	return types.StringValue(*s)
}

// stringPointer returns the value of a known, non-null string, nil otherwise,
// for a request field that the API reads as null when absent.
func stringPointer(v types.String) *string {
	if v.IsNull() || v.IsUnknown() {
		return nil
	}
	s := v.ValueString()
	return &s
}

// attributesState writes each attribute as a string: the provider sends
// strings, and a value set outside Terraform as another JSON type (a number,
// a list) reads as its JSON text. No attribute reads as null, since the
// schema refuses an empty map.
func attributesState(attrs map[string]json.RawMessage) map[string]string {
	if len(attrs) == 0 {
		return nil
	}
	out := make(map[string]string, len(attrs))
	for k, raw := range attrs {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			out[k] = s
			continue
		}
		out[k] = string(raw)
	}
	return out
}

// instantState keeps prior when it names the same instant as got, so an
// expiry the API returns in another format (UTC, with fractional seconds)
// does not show as drift; otherwise it returns got in RFC 3339.
func instantState(got *time.Time, prior types.String) types.String {
	if got == nil {
		return types.StringNull()
	}
	if !prior.IsNull() && !prior.IsUnknown() {
		if p, err := time.Parse(time.RFC3339, prior.ValueString()); err == nil && p.Equal(*got) {
			return prior
		}
	}
	return types.StringValue(got.UTC().Format(time.RFC3339Nano))
}
