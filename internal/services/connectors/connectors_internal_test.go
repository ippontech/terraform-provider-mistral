// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package connectors

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
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

// A failed PATCH says, after a narrowing only, that the visibility already
// moved; it names the access scope of the key only for a 404 after a move to
// private.
func TestUpdateFailure(t *testing.T) {
	t.Parallel()
	notFound := &client.Error{StatusCode: 404, Method: "PATCH", Path: "/v1/connectors/c", Message: "Connector not found"}
	unavailable := &client.Error{StatusCode: 503, Method: "PATCH", Path: "/v1/connectors/c"}
	const (
		moved = "The visibility of the connector is already %s, but its other arguments were not changed: %s"
		scope = "\n\nAn API key with the default connector access scope, \"Shared connectors only\", does not see private connectors: " +
			"manage a private connector with a key of the scope \"Private and shared connectors\" (https://docs.mistral.ai/admin/identity-access/api-keys)."
	)
	cases := map[string]struct {
		narrowed bool
		want     string
		err      error
		detail   string
	}{
		"no narrowing":            {false, client.ConnectorPrivate, notFound, notFound.Error()},
		"404 after private":       {true, client.ConnectorPrivate, notFound, fmt.Sprintf(moved, "private", notFound) + scope},
		"404 after the workspace": {true, client.ConnectorSharedWorkspace, notFound, fmt.Sprintf(moved, "shared_workspace", notFound)},
		"503 after private":       {true, client.ConnectorPrivate, unavailable, fmt.Sprintf(moved, "private", unavailable)},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := updateFailure(c.narrowed, c.want, c.err); got != c.detail {
				t.Errorf("updateFailure =\n%s\nwant\n%s", got, c.detail)
			}
		})
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
