// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package users

import (
	"strings"
	"testing"
	"time"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
)

// refusedByAPI reads every refusal the create answers with a 200. The race
// it guards against (an address that becomes a member between the check
// and the create) cannot be staged through Terraform, hence a unit test.
func TestRefusedByAPI(t *testing.T) {
	t.Parallel()
	zero, one := int64(0), int64(1)
	cases := map[string]struct {
		result client.InviteResult
		want   string
	}{
		"invited":                 {client.InviteResult{InvitedCount: &one}, ""},
		"invited, no count":       {client.InviteResult{}, ""},
		"invalid address":         {client.InviteResult{InvalidEmails: []string{"grace@example"}, InvitedCount: &zero}, "Mistral refused grace@example as invalid."},
		"already a member":        {client.InviteResult{AlreadyMembers: []string{"grace@example.com"}, InvitedCount: &zero}, "the address belongs to a member"},
		"no one, and no reason":   {client.InviteResult{InvitedCount: &zero}, "Mistral invited no one"},
		"invalid before a member": {client.InviteResult{InvalidEmails: []string{"a"}, AlreadyMembers: []string{"b"}}, "Mistral refused a as invalid."},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			diags := refusedByAPI("grace@example.com", c.result)
			switch {
			case c.want == "" && diags.HasError():
				t.Errorf("unexpected error: %v", diags)
			case c.want != "" && (len(diags) != 1 || !strings.Contains(diags[0].Detail(), c.want)):
				t.Errorf("diagnostics = %v, want one containing %q", diags, c.want)
			}
		})
	}
}

// timestamp writes null for the zero time and RFC 3339 in UTC otherwise.
func TestTimestamp(t *testing.T) {
	t.Parallel()
	if got := timestamp(time.Time{}); !got.IsNull() {
		t.Errorf("timestamp of the zero time = %v, want null", got)
	}
	if got := timestamp(time.Date(2026, 10, 29, 12, 0, 0, 0, time.FixedZone("CET", 3600))).ValueString(); got != "2026-10-29T11:00:00Z" {
		t.Errorf("timestamp = %s, want UTC", got)
	}
}
