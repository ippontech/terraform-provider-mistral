// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package connectors_test

import (
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

// The routes of the connectors API the provider calls
// (https://docs.mistral.ai/api/endpoint/beta/connectors).
const (
	routeCreate            = "POST /v1/connectors"
	routeGet               = "GET /v1/connectors/{connector_id_or_name}"
	routeUpdate            = "PATCH /v1/connectors/{connector_id}"
	routeDelete            = "DELETE /v1/connectors/{connector_id}"
	routeShare             = "PUT /v1/connectors/{connector_id}/share"
	routeUnshare           = "DELETE /v1/connectors/{connector_id}/share"
	routeShareOrg          = "PUT /v1/connectors/{connector_id}/organization/share"
	routeUnshareOrg        = "DELETE /v1/connectors/{connector_id}/organization/share"
	routeListCredentials   = "GET /v1/connectors/{connector_id_or_name}/{consumer_scope}/credentials"
	routeCreateCredentials = "POST /v1/connectors/{connector_id_or_name}/{consumer_scope}/credentials"
	routeUpdateCredentials = "PATCH /v1/connectors/{connector_id_or_name}/{consumer_scope}/credentials"
	routeDeleteCredentials = "DELETE /v1/connectors/{connector_id_or_name}/{consumer_scope}/credentials/{credentials_name}"
)

// fake serves the connectors API over an in-memory store. Besides the
// transport, it holds what the provider depends on:
//   - the defaults of a create (protocol mcp, visibility private) and the
//     update schemas, which refuse unknown fields;
//   - the share calls, which take a private connector and make it shared,
//     and the unshare calls, which make it private again and remove the
//     credentials of the scope it leaves;
//   - credentials per connector and scope, which the list returns without
//     their secrets, with the is_default rules of CredentialsCreateOrUpdate.
type fake struct {
	*sdktest.Server
	connectors  *sdktest.Collection
	credentials *sdktest.Collection

	mu    sync.Mutex
	clock time.Time
	// createAnswersPrivate makes a create ignore the visibility of its body,
	// for the test of the share calls that then follow a create.
	createAnswersPrivate bool
}

// newFake starts a fake with no connector.
func newFake(t *testing.T) *fake {
	t.Helper()
	f := &fake{
		Server:      sdktest.NewServer(t),
		connectors:  sdktest.NewCollection("id"),
		credentials: sdktest.NewCollection("key"),
		clock:       time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC),
	}
	f.Handle(routeCreate, f.create)
	f.Handle(routeGet, f.get)
	f.Handle(routeUpdate, f.update)
	f.Handle(routeDelete, f.delete)
	f.Handle(routeShare, f.share(sharedWorkspace))
	f.Handle(routeUnshare, f.unshare(sharedWorkspace))
	f.Handle(routeShareOrg, f.share(sharedOrg))
	f.Handle(routeUnshareOrg, f.unshare(sharedOrg))
	f.Handle(routeListCredentials, f.listCredentials)
	f.Handle(routeCreateCredentials, f.createCredentials)
	f.Handle(routeUpdateCredentials, f.updateCredentials)
	f.Handle(routeDeleteCredentials, f.deleteCredentials)
	return f
}

// Visibilities of a connector.
const (
	private         = "private"
	sharedWorkspace = "shared_workspace"
	sharedOrg       = "shared_org"
)

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

// message is the MessageResponse of the writes that return no object.
func message(text string) map[string]any {
	return map[string]any{"message": text}
}

// create stores a connector with the defaults of CreateConnectorRequest.
func (f *fake) create(w http.ResponseWriter, r *http.Request) {
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	for _, field := range []string{"name", "description", "server"} {
		if _, ok := body[field].(string); !ok {
			sdktest.WriteValidationError(w, []string{"body", field}, "Field required")
			return
		}
	}
	if body["protocol"] == nil {
		body["protocol"] = "mcp"
	}
	f.mu.Lock()
	answersPrivate := f.createAnswersPrivate
	f.mu.Unlock()
	if body["visibility"] == nil || answersPrivate {
		body["visibility"] = private
	}
	now := f.now()
	body["created_at"], body["modified_at"] = now, now
	body["owner_type"] = ownerType(body["visibility"].(string))
	sdktest.WriteJSON(w, http.StatusCreated, f.connectors.Create(body))
}

// ownerType is the ConsumerType that owns a connector of a visibility: a
// share "transfers ownership" to the workspace or the organization.
func ownerType(visibility string) string {
	switch visibility {
	case sharedWorkspace:
		return "workspace"
	case sharedOrg:
		return "org"
	}
	return "user"
}

// lookup finds a connector by ID or by name, which is unique in a
// workspace: the paths named connector_id_or_name take either.
func (f *fake) lookup(idOrName string) (map[string]any, bool) {
	if c, ok := f.connectors.Get(idOrName); ok {
		return c, true
	}
	byName := f.connectors.List(func(c map[string]any) bool { return c["name"] == idOrName })
	if len(byName) == 0 {
		return nil, false
	}
	return byName[0], true
}

// get answers GET /v1/connectors/{connector_id_or_name}.
func (f *fake) get(w http.ResponseWriter, r *http.Request) {
	c, ok := f.lookup(r.PathValue("connector_id_or_name"))
	if !ok {
		sdktest.WriteError(w, http.StatusNotFound, "Connector not found")
		return
	}
	sdktest.WriteJSON(w, http.StatusOK, c)
}

// updateFields are the fields of ConnectorMCPPublicUpdate and
// UpdateHTTPConnectorRequest, which refuse any other one
// (additionalProperties: false): the HTTP update has no system_prompt.
var updateFields = map[string][]string{
	"mcp":  {"protocol", "name", "title", "description", "icon_url", "server", "auth_methods", "system_prompt"},
	"http": {"protocol", "name", "title", "description", "icon_url", "server", "auth_methods"},
}

// update applies the fields of the body, null included, after checking them
// against the update schema of the connector's protocol.
func (f *fake) update(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("connector_id")
	c, ok := f.connectors.Get(id)
	if !ok {
		sdktest.WriteError(w, http.StatusNotFound, "Connector not found")
		return
	}
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	allowed := updateFields[c["protocol"].(string)]
	for field := range body {
		if !slices.Contains(allowed, field) {
			sdktest.WriteValidationError(w, []string{"body", field}, "Extra inputs are not permitted")
			return
		}
	}
	body["modified_at"] = f.now()
	updated, _ := f.connectors.Patch(id, body)
	sdktest.WriteJSON(w, http.StatusOK, updated)
}

// delete removes a connector and its credentials.
func (f *fake) delete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("connector_id")
	if !f.connectors.Delete(id) {
		sdktest.WriteError(w, http.StatusNotFound, "Connector not found")
		return
	}
	f.removeCredentials(id, "")
	sdktest.WriteJSON(w, http.StatusOK, message("Connector deleted"))
}

// share answers a share call to visibility. It "transfers ownership of a
// private user-owned connector", so it refuses a connector that is not
// private.
func (f *fake) share(visibility string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("connector_id")
		c, ok := f.connectors.Get(id)
		if !ok {
			sdktest.WriteError(w, http.StatusNotFound, "Connector not found")
			return
		}
		if c["visibility"] != private {
			sdktest.WriteError(w, http.StatusBadRequest, "Only a private connector can be shared")
			return
		}
		f.connectors.Patch(id, map[string]any{"visibility": visibility, "owner_type": ownerType(visibility)})
		sdktest.WriteJSON(w, http.StatusOK, message("Connector shared"))
	}
}

// unshare answers an unshare call from visibility: the connector goes back
// to private, and "[workspace or organization]-scoped connections and other
// members' connections are removed".
func (f *fake) unshare(visibility string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("connector_id")
		c, ok := f.connectors.Get(id)
		if !ok {
			sdktest.WriteError(w, http.StatusNotFound, "Connector not found")
			return
		}
		if c["visibility"] != visibility {
			sdktest.WriteError(w, http.StatusBadRequest, "The connector is not shared this way")
			return
		}
		f.connectors.Patch(id, map[string]any{"visibility": private, "owner_type": "user"})
		scope := map[string]string{sharedWorkspace: "workspace", sharedOrg: "organization"}[visibility]
		f.removeCredentials(id, scope)
		sdktest.WriteJSON(w, http.StatusOK, message("Connector unshared"))
	}
}

// removeCredentials deletes the credentials of a connector for a scope, or
// for every scope when scope is "".
func (f *fake) removeCredentials(connectorID, scope string) {
	for _, c := range f.credentials.List(func(c map[string]any) bool {
		return c["connector_id"] == connectorID && (scope == "" || c["consumer_scope"] == scope)
	}) {
		f.credentials.Delete(c["key"].(string))
	}
}

// credentialsTarget resolves the connector and the consumer scope of a
// credentials path, or answers the error the API would.
func (f *fake) credentialsTarget(w http.ResponseWriter, r *http.Request) (connectorID, scope string, ok bool) {
	scope = r.PathValue("consumer_scope")
	if !slices.Contains([]string{"user", "workspace", "organization"}, scope) {
		sdktest.WriteValidationError(w, []string{"path", "consumer_scope"}, "Input should be 'user', 'workspace' or 'organization'")
		return "", "", false
	}
	c, found := f.lookup(r.PathValue("connector_id_or_name"))
	if !found {
		sdktest.WriteError(w, http.StatusNotFound, "Connector not found")
		return "", "", false
	}
	return c["id"].(string), scope, true
}

// credentialsOf returns the credentials of a connector for a scope.
func (f *fake) credentialsOf(connectorID, scope string) []map[string]any {
	return f.credentials.List(func(c map[string]any) bool {
		return c["connector_id"] == connectorID && c["consumer_scope"] == scope
	})
}

// listCredentials answers CredentialsResponse: every set of credentials of
// the scope, without the secrets, and the scope as a ConsumerType.
func (f *fake) listCredentials(w http.ResponseWriter, r *http.Request) {
	connectorID, scope, ok := f.credentialsTarget(w, r)
	if !ok {
		return
	}
	out := []any{}
	for _, c := range f.credentialsOf(connectorID, scope) {
		out = append(out, map[string]any{
			"name": c["name"], "title": c["title"], "authentication_type": c["authentication_type"],
			"scope": strings.Replace(scope, "organization", "org", 1), "status": nil, "is_default": c["is_default"],
		})
	}
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{"credentials": out, "connector_preset_credentials_for_auth": []any{}})
}

// createCredentials stores a set of credentials with the rules of a create
// of is_default: the first credentials of an authentication method become
// its default (false is refused), and true promotes them.
func (f *fake) createCredentials(w http.ResponseWriter, r *http.Request) {
	connectorID, scope, ok := f.credentialsTarget(w, r)
	if !ok {
		return
	}
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	f.storeNew(w, connectorID, scope, body)
}

// storeNew stores credentials that do not exist yet.
func (f *fake) storeNew(w http.ResponseWriter, connectorID, scope string, body map[string]any) {
	name, _ := body["name"].(string)
	if name == "" {
		sdktest.WriteValidationError(w, []string{"body", "name"}, "Field required")
		return
	}
	authType := authenticationType(body["credentials"])
	hasDefault := slices.ContainsFunc(f.credentialsOf(connectorID, scope), func(c map[string]any) bool {
		return c["authentication_type"] == authType && c["is_default"] == true
	})
	isDefault, set := body["is_default"].(bool)
	switch {
	case !hasDefault && set && !isDefault:
		sdktest.WriteError(w, http.StatusBadRequest, "A default credential must exist for this auth method")
		return
	case !hasDefault:
		isDefault = true
	case isDefault:
		f.demote(connectorID, scope, authType)
	}
	f.credentials.Put(map[string]any{
		"key": connectorID + ":" + scope + ":" + name, "connector_id": connectorID, "consumer_scope": scope,
		"name": name, "title": body["title"], "authentication_type": authType, "is_default": isDefault,
	})
	sdktest.WriteJSON(w, http.StatusOK, message("Credentials created"))
}

// authenticationType is the method credentials are for, read from the
// fields they set: bearer_token for bearer, oauth for oauth2, none otherwise.
// This is our reading: the specification does not say how the API tells.
func authenticationType(credentials any) string {
	c, _ := credentials.(map[string]any)
	switch {
	case c["bearer_token"] != nil:
		return "bearer"
	case c["oauth"] != nil:
		return "oauth2"
	}
	return "none"
}

// demote makes every credentials of the method non-default, before another
// becomes its default.
func (f *fake) demote(connectorID, scope, authType string) {
	for _, c := range f.credentialsOf(connectorID, scope) {
		if c["authentication_type"] == authType {
			f.credentials.Patch(c["key"].(string), map[string]any{"is_default": false})
		}
	}
}

// updateCredentials creates or updates credentials ("Create or update
// consumer credentials"). On an update, an omitted field is left unchanged,
// true promotes the credentials, and false is refused for the default.
func (f *fake) updateCredentials(w http.ResponseWriter, r *http.Request) {
	connectorID, scope, ok := f.credentialsTarget(w, r)
	if !ok {
		return
	}
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	name, _ := body["name"].(string)
	key := connectorID + ":" + scope + ":" + name
	current, exists := f.credentials.Get(key)
	if !exists {
		f.storeNew(w, connectorID, scope, body)
		return
	}
	patch := map[string]any{}
	if title, ok := body["title"]; ok {
		patch["title"] = title
	}
	if credentials, ok := body["credentials"]; ok {
		patch["authentication_type"] = authenticationType(credentials)
	}
	switch isDefault, set := body["is_default"].(bool); {
	case set && isDefault:
		f.demote(connectorID, scope, current["authentication_type"].(string))
		patch["is_default"] = true
	case set && current["is_default"] == true:
		sdktest.WriteError(w, http.StatusBadRequest, "Promote another credential before demoting the default one")
		return
	}
	f.credentials.Patch(key, patch)
	sdktest.WriteJSON(w, http.StatusOK, message("Credentials updated"))
}

// deleteCredentials removes a set of credentials.
func (f *fake) deleteCredentials(w http.ResponseWriter, r *http.Request) {
	connectorID, scope, ok := f.credentialsTarget(w, r)
	if !ok {
		return
	}
	if !f.credentials.Delete(connectorID + ":" + scope + ":" + r.PathValue("credentials_name")) {
		sdktest.WriteError(w, http.StatusNotFound, "Credentials not found")
		return
	}
	sdktest.WriteJSON(w, http.StatusOK, message("Credentials deleted"))
}

// seed stores a connector built outside Terraform, such as in AI Studio,
// and returns its ID.
func (f *fake) seed(connector map[string]any) string {
	c := sdktest.DeepCopy(connector)
	now := f.now()
	c["created_at"], c["modified_at"] = now, now
	if c["protocol"] == nil {
		c["protocol"] = "mcp"
	}
	return f.connectors.Create(c)["id"].(string)
}

// seedCredentials stores credentials created outside Terraform.
func (f *fake) seedCredentials(connectorID, scope, name string, isDefault bool) {
	f.credentials.Put(map[string]any{
		"key": connectorID + ":" + scope + ":" + name, "connector_id": connectorID, "consumer_scope": scope,
		"name": name, "title": nil, "authentication_type": "bearer", "is_default": isDefault,
	})
}
