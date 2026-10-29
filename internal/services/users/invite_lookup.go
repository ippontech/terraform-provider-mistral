// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package users

import (
	"context"
	"slices"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
)

// inviteLookup is what findInvite knows of an invite.
type inviteLookup struct {
	// invite is the invite while Mistral lists it, nil once accepted.
	invite *client.Invite
	status string
}

// findInvite returns the invite id, or nil when Mistral has no trace of it.
// It is the one function to replace when Mistral adds a read by ID.
//
// It lists GET /v1/admin/users filtered by email, the narrowest filter the
// API offers, rather than GET /v1/admin/users-invite, which has no filter
// and does not say whether an invite expired. Accepting an invite consumes
// it: Mistral lists the member instead, so an invite gone from the list
// whose address belongs to a member is accepted, and stays in the state
// (CLAUDE.md, "State after status changes").
//
// After an import, email is empty: the walk then covers every member and
// invite of the organization, and an accepted invite cannot be recognized.
func (r *InviteResource) findInvite(ctx context.Context, id, email string) (*inviteLookup, error) {
	dir, err := r.client.ListUsers(ctx, email)
	if err != nil {
		return nil, err
	}
	for i := range dir.Invites {
		if inv := &dir.Invites[i]; inv.ID == id {
			return &inviteLookup{invite: inv, status: inviteStatus(*inv)}, nil
		}
	}
	if email != "" && findUser(dir, email) != nil {
		return &inviteLookup{status: statusAccepted}, nil
	}
	return nil, nil
}

// findCreatedInvite returns the invite a create just made, or nil when
// Mistral does not list it. The create answers with counts, not with the
// invite: this is the one function to replace when it returns the ID.
//
// Create ran only if the address had no pending invite, so the pending
// invite listed now is the one it made; should another client have invited
// the same address in between, the newest one is taken.
func (r *InviteResource) findCreatedInvite(ctx context.Context, email string) (*client.Invite, error) {
	dir, err := r.client.ListUsers(ctx, email)
	if err != nil {
		return nil, err
	}
	var newest *client.Invite
	for i := range dir.Invites {
		inv := &dir.Invites[i]
		if inv.Expired || !sameEmail(inv.Email, email) {
			continue
		}
		if newest == nil || inv.CreatedAt.After(newest.CreatedAt) {
			newest = inv
		}
	}
	return newest, nil
}

// inviteStatus is the status of an invite Mistral lists.
func inviteStatus(inv client.Invite) string {
	if inv.Expired {
		return statusExpired
	}
	return statusPending
}

// legacyOrganizationRoles names the letters of UserRole, which the API can
// return instead of role UUIDs. A and M are documented as Admin and Member
// (https://docs.mistral.ai/admin/admin-api/user-provisioning); B for the
// billing manager is our reading of the three roles of the Admin Panel.
var legacyOrganizationRoles = map[string]string{"A": "organization_admin", "M": "member", "B": "billing_manager"}

// roleNames turns the raw organization roles of an invite into the names
// role_names takes: the legacy letters by legacyOrganizationRoles, the role
// UUIDs through GET /v1/admin/roles, which is called only when a UUID is
// there. A role neither names is kept as the API returned it, so the plan
// that follows an import shows it rather than hiding it.
func (r *InviteResource) roleNames(ctx context.Context, raw []string) ([]string, error) {
	var byID map[string]string
	names := make([]string, 0, len(raw))
	for _, role := range raw {
		if name, ok := legacyOrganizationRoles[role]; ok {
			names = append(names, name)
			continue
		}
		if byID == nil {
			roles, err := r.client.ListRoles(ctx)
			if err != nil {
				return nil, err
			}
			byID = make(map[string]string, len(roles.OrganizationRoles))
			for _, ro := range roles.OrganizationRoles {
				byID[ro.ID] = ro.Name
			}
		}
		if name, ok := byID[role]; ok {
			role = name
		}
		names = append(names, role)
	}
	// role_names is a set: a role the API lists twice, as a letter and as
	// a UUID, must appear once.
	slices.Sort(names)
	return slices.Compact(names), nil
}
