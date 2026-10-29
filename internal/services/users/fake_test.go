// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package users_test

import (
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

const (
	routeListUsers    = "GET /v1/admin/users"
	routeGetUser      = "GET /v1/admin/users/{user_id}"
	routeInvite       = "POST /v1/admin/users-invite"
	routeDeleteInvite = "DELETE /v1/admin/users-invite/{invite_uuid}"
	routeListRoles    = "GET /v1/admin/roles"
)

// fakeRoleIDs are the UUIDs the fake gives the organization roles. They are
// made up: the specification lists three static role UUIDs without saying
// which role each one is.
var fakeRoleIDs = map[string]string{
	"member":             "00000000-0000-4000-8000-0000000000a1",
	"billing_manager":    "00000000-0000-4000-8000-0000000000a2",
	"organization_admin": "00000000-0000-4000-8000-0000000000a3",
}

// fake serves the users, invites and roles of the Admin API
// (https://docs.mistral.ai/api/endpoint/beta/admin/users) over an in-memory
// store. Besides the transport, it holds what the provider depends on:
//   - GET /v1/admin/users pages members and invites together, from page 1,
//     page_size up to 100, with the total of both; the list of a member has
//     no first and last name, which only the read by ID returns;
//   - its email filter is a case-insensitive substring match, the loosest
//     reading of "Email address to filter users and invitations", so that
//     the provider's own comparison of the addresses is exercised;
//   - a create answers counts, not the invite (OrganizationInvitesCreateOUT),
//     and refuses the address of a member in already_members;
//   - an invite returns its roles as role UUIDs (raw_roles), not as the
//     names the create took;
//   - accepting an invite (accept) removes it from the list and adds the
//     member, as the Invited Users and Active Users of the Admin Panel show
//     (https://docs.mistral.ai/admin/identity-access/user-management).
type fake struct {
	*sdktest.Server
	// t reports a broken client from a handler, with Errorf only: handlers
	// run on server goroutines.
	t       testing.TB
	users   *sdktest.Collection
	invites *sdktest.Collection

	mu    sync.Mutex
	clock time.Time
	// invalid holds the addresses a create answers in invalid_emails.
	invalid map[string]bool
	// unlisted makes a create succeed without storing the invite, for the
	// test of an invite Mistral does not list.
	unlisted bool
	// afterInvite runs once a create has answered, for a test that fails
	// the listing that follows it.
	afterInvite func()
}

// newFake starts a fake with no member, no invite, and the three
// organization roles of fakeRoleIDs.
func newFake(t *testing.T) *fake {
	t.Helper()
	f := &fake{
		Server:  sdktest.NewServer(t),
		t:       t,
		users:   sdktest.NewCollection("uuid"),
		invites: sdktest.NewCollection("uuid"),
		clock:   time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC),
		invalid: map[string]bool{},
	}
	f.Handle(routeListUsers, f.listUsers)
	f.Handle(routeGetUser, f.getUser)
	f.Handle(routeInvite, f.invite)
	f.Handle(routeDeleteInvite, f.deleteInvite)
	f.Handle(routeListRoles, f.listRoles)
	return f
}

// now returns the time of the next write: one minute after the previous one.
func (f *fake) now() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clock = f.clock.Add(time.Minute)
	return f.clock.Format(time.RFC3339)
}

// at is the time of the n-th write of the fake, from 1.
func at(n int) string {
	return time.Date(2026, 10, 29, 10, n, 0, 0, time.UTC).Format(time.RFC3339)
}

// listEntry is a member or an invite, in the order the list pages them.
type listEntry struct {
	member bool
	object map[string]any
}

// listUsers answers one page of the members, then the invites, whose
// address contains the email filter.
func (f *fake) listUsers(w http.ResponseWriter, r *http.Request) {
	page, size := sdktest.QueryInt(r, "page", 1), sdktest.QueryInt(r, "page_size", 25)
	if page < 1 || size < 1 || size > 100 {
		sdktest.WriteValidationError(w, []string{"query", "page_size"}, "Input should be between 1 and 100")
		return
	}
	filter := strings.ToLower(r.URL.Query().Get("email"))
	matches := func(o map[string]any) bool {
		email, _ := o["email"].(string)
		return strings.Contains(strings.ToLower(email), filter)
	}
	var entries []listEntry
	for _, u := range f.users.List(matches) {
		delete(u, "first_name")
		delete(u, "last_name")
		entries = append(entries, listEntry{member: true, object: u})
	}
	for _, inv := range f.invites.List(matches) {
		entries = append(entries, listEntry{object: inv})
	}
	members, invites := []any{}, []any{}
	for _, e := range sdktest.PageOf(entries, page, size, 1) {
		if e.member {
			members = append(members, e.object)
		} else {
			invites = append(invites, e.object)
		}
	}
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{
		"members": members, "invites": invites, "total": len(entries), "page": page, "page_size": size,
	})
}

// getUser answers a member with every field of the read by ID, or 404.
func (f *fake) getUser(w http.ResponseWriter, r *http.Request) {
	u, ok := f.users.Get(r.PathValue("user_id"))
	if !ok {
		sdktest.WriteError(w, http.StatusNotFound, "User not found")
		return
	}
	sdktest.WriteJSON(w, http.StatusOK, u)
}

// invite creates one invite. The provider sends one address: a list is a
// bug of the client.
func (f *fake) invite(w http.ResponseWriter, r *http.Request) {
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	email, _ := body["email"].(string)
	if strings.ContainsAny(email, ",\n") {
		f.t.Errorf("fake: the provider sent several addresses in one invite: %q", email)
		sdktest.WriteError(w, http.StatusBadRequest, "fake: the provider must send one address")
		return
	}
	result := map[string]any{"invalid_emails": []string{}, "already_members": []string{}, "invited_members_count": 0}
	switch {
	case f.invalid[email]:
		result["invalid_emails"] = []string{email}
	case f.memberID(email) != "":
		result["already_members"] = []string{email}
	default:
		result["invited_members_count"] = 1
		if !f.unlisted {
			f.invites.Create(newInvite(email, roleIDs(body["role_names"]), body["workspace_uuids"], f.now()))
		}
	}
	sdktest.WriteJSON(w, http.StatusOK, result)
	if f.afterInvite != nil {
		f.afterInvite()
	}
}

// deleteInvite revokes an invite, pending or expired, and answers 404 for
// an ID the fake does not list, an accepted invite included.
func (f *fake) deleteInvite(w http.ResponseWriter, r *http.Request) {
	if !f.invites.Delete(r.PathValue("invite_uuid")) {
		sdktest.WriteError(w, http.StatusNotFound, "Invite not found")
		return
	}
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{"message": "Invite deleted"})
}

// listRoles answers the organization roles of fakeRoleIDs, named as the
// role_names of the requests, and no workspace role.
func (f *fake) listRoles(w http.ResponseWriter, _ *http.Request) {
	var org []any
	for _, name := range []string{"member", "billing_manager", "organization_admin"} {
		org = append(org, map[string]any{"uuid": fakeRoleIDs[name], "name": name, "description": name, "is_custom_role": false})
	}
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{"organization_roles": org, "workspace_roles": []any{}})
}

// newInvite is an invite as the list returns it (OrganizationInviteOUT); an
// absent or null workspace_uuids reads [], its default in the specification.
func newInvite(email string, rawRoles []string, workspaceIDs any, createdAt string) map[string]any {
	if workspaceIDs == nil {
		workspaceIDs = []any{}
	}
	return map[string]any{
		"email": email, "raw_roles": rawRoles, "raw_role": rawRoles[0], "expired": false,
		"created_at": createdAt, "workspace_uuids": workspaceIDs,
	}
}

// roleIDs turns the role_names of a create into the role UUIDs an invite
// returns.
func roleIDs(names any) []string {
	var ids []string
	list, _ := names.([]any)
	for _, n := range list {
		ids = append(ids, fakeRoleIDs[n.(string)])
	}
	if len(ids) == 0 {
		ids = []string{fakeRoleIDs["member"]}
	}
	return ids
}

// memberID returns the ID of the member with the address email, or "".
func (f *fake) memberID(email string) string {
	found := f.users.List(func(u map[string]any) bool { return strings.EqualFold(u["email"].(string), email) })
	if len(found) == 0 {
		return ""
	}
	return found[0]["uuid"].(string)
}

// seedUser stores a member created outside Terraform, with the fields of
// the read by ID (AdminUserOUT) that extra overrides, and returns its ID.
func (f *fake) seedUser(email string, extra map[string]any) string {
	u := map[string]any{
		"email": email, "name": "Someone", "first_name": "Some", "last_name": "One", "oid_id": nil,
		"raw_roles": []string{fakeRoleIDs["member"]}, "raw_role": fakeRoleIDs["member"],
		"workspaces": []any{}, "subscriptions": []any{}, "created_at": f.now(),
	}
	maps.Copy(u, extra)
	return f.users.Create(u)["uuid"].(string)
}

// seedInvite stores an invite sent outside Terraform and returns its ID.
func (f *fake) seedInvite(email string, rawRoles []string, expired bool) string {
	inv := newInvite(email, rawRoles, nil, f.now())
	inv["expired"] = expired
	return f.invites.Create(inv)["uuid"].(string)
}

// accept turns the invites of email into a member: Mistral no longer lists
// an invite once accepted.
func (f *fake) accept(email string) string {
	for _, inv := range f.invites.List(func(inv map[string]any) bool { return inv["email"] == email }) {
		f.invites.Delete(inv["uuid"].(string))
	}
	return f.seedUser(email, nil)
}

// expire marks the invite id expired.
func (f *fake) expire(id string) {
	f.invites.Patch(id, map[string]any{"expired": true})
}

// assertListQueries fails the test unless every request to the list carried
// one of the query strings of allowed, and each of them was sent.
func (f *fake) assertListQueries(t *testing.T, allowed ...string) {
	t.Helper()
	var sent []string
	for _, r := range f.Requests(routeListUsers) {
		q := r.Query.Encode()
		if !slices.Contains(allowed, q) {
			t.Errorf("unexpected query %q on %s", q, routeListUsers)
		}
		sent = append(sent, q)
	}
	for _, q := range allowed {
		if !slices.Contains(sent, q) {
			t.Errorf("no request to %s with the query %q; sent: %v", routeListUsers, q, sent)
		}
	}
}

// emails returns n addresses that all contain suffix, as the substring
// filter of the fake matches them.
func emails(n int, suffix string) []string {
	out := make([]string, 0, n)
	for i := range n {
		out = append(out, fmt.Sprintf("%d.%s", i, suffix))
	}
	return out
}
