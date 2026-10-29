// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package connectors

import (
	"context"
	"testing"
	"time"
)

// A date reads in UTC, and a missing one as null.
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

// The validator gives the same description in plain text and Markdown.
func TestCredentialsDocumentDescribesItself(t *testing.T) {
	t.Parallel()
	v := credentialsDocument{}
	if v.MarkdownDescription(context.Background()) != v.Description(context.Background()) || v.Description(context.Background()) == "" {
		t.Errorf("descriptions = %q, %q", v.Description(context.Background()), v.MarkdownDescription(context.Background()))
	}
}
