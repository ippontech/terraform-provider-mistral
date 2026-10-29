// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package serviceaccounts

import (
	"testing"
	"time"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
)

// TestAccountState checks the mapping of the values the fake tests do not
// reach: an absent date and a role list in another order.
func TestAccountState(t *testing.T) {
	t.Parallel()
	got := accountState(client.ServiceAccount{ID: "sa-1", Description: new("")}, []string{})
	if !got.Description.IsNull() || got.RoleIDs != nil || !got.CreatedAt.IsNull() {
		t.Errorf("accountState = %+v, want a null description, role_ids and created_at", got)
	}
	if got := timestamp(time.Date(2026, 10, 29, 12, 0, 0, 0, time.FixedZone("CET", 3600))); got.ValueString() != "2026-10-29T11:00:00Z" {
		t.Errorf("timestamp = %s, want UTC", got)
	}
}

// TestSameSet checks that the order of the roles never plans a PUT.
func TestSameSet(t *testing.T) {
	t.Parallel()
	if !sameSet([]string{"b", "a"}, []string{"a", "b"}) || sameSet([]string{"a"}, []string{"a", "b"}) || !sameSet(nil, []string{}) {
		t.Error("sameSet")
	}
}
