// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

// The routes of the connectors API, and the fields of a connector most
// answers carry.
const (
	connectorsRoute         = "POST /v1/connectors"
	connectorRoute          = "GET /v1/connectors/{connector_id_or_name}"
	connectorUpdateRoute    = "PATCH /v1/connectors/{connector_id}"
	connectorDeleteRoute    = "DELETE /v1/connectors/{connector_id}"
	credentialsListRoute    = "GET /v1/connectors/{connector}/{scope}/credentials"
	credentialsCreateRoute  = "POST /v1/connectors/{connector}/{scope}/credentials"
	credentialsUpdateRoute  = "PATCH /v1/connectors/{connector}/{scope}/credentials"
	credentialsDeleteRoute  = "DELETE /v1/connectors/{connector}/{scope}/credentials/{name}"
	connectorResponseFields = `"id": "c-1", "name": "deepwiki", "description": "Wiki", "visibility": "private", "protocol": "mcp"`
)

// A create sends every field, null included, and decodes the connector.
func TestCreateConnectorSendsEveryField(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond(connectorsRoute, http.StatusCreated, json.RawMessage(`{`+connectorResponseFields+`,
		"server": "https://mcp.deepwiki.com/mcp", "title": null, "created_at": "2026-10-29T10:00:00Z", "modified_at": "2026-10-29T10:00:00Z"}`))

	c, err := newTestClient(srv).CreateConnector(context.Background(), ConnectorRequest{
		Protocol: ConnectorProtocolMCP, Name: "deepwiki", Description: "Wiki",
		Server: "https://mcp.deepwiki.com/mcp", Visibility: ConnectorPrivate,
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.ID != "c-1" || c.Title != nil || *c.Server != "https://mcp.deepwiki.com/mcp" || c.ModifiedAt.IsZero() {
		t.Errorf("connector = %+v", c)
	}
	srv.AssertJSONBodies(connectorsRoute, `{
		"protocol": "mcp", "name": "deepwiki", "title": null, "description": "Wiki",
		"server": "https://mcp.deepwiki.com/mcp", "icon_url": null, "system_prompt": null, "visibility": "private"
	}`)
	srv.AssertCredential("", sdktest.APIKey)
}

// A create is not replayed after a 5xx: the connector may exist.
func TestCreateConnectorIsSentOnceOnA5xx(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond(connectorsRoute, http.StatusCreated, map[string]any{"id": "c-1"})
	srv.FailNext(connectorsRoute, http.StatusInternalServerError, 1)

	if _, err := newTestClient(srv).CreateConnector(context.Background(), ConnectorRequest{Name: "a"}); StatusCode(err) != http.StatusInternalServerError {
		t.Errorf("err = %v, want the 500", err)
	}
	srv.AssertCalled(connectorsRoute, 1)
}

// The HTTP update schema has no system_prompt and refuses unknown fields:
// the body carries it for an MCP connector only, null included.
func TestUpdateConnectorSendsTheFieldsOfItsProtocol(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond(connectorUpdateRoute, http.StatusOK, json.RawMessage(`{`+connectorResponseFields+`}`))
	c := newTestClient(srv)
	ctx := context.Background()

	update := ConnectorUpdate{Protocol: ConnectorProtocolMCP, Name: "deepwiki", Title: new("Wiki"), Description: "Wiki", Server: "https://a.example"}
	if _, err := c.UpdateConnector(ctx, "c-1", update); err != nil {
		t.Fatal(err)
	}
	update.Protocol, update.SystemPrompt = ConnectorProtocolHTTP, new("ignored")
	if _, err := c.UpdateConnector(ctx, "c-1", update); err != nil {
		t.Fatal(err)
	}
	srv.FailNext(connectorUpdateRoute, http.StatusServiceUnavailable, 1)
	if _, err := c.UpdateConnector(ctx, "c-1", update); StatusCode(err) != http.StatusServiceUnavailable {
		t.Errorf("an update after a 503 must not be replayed: %v", err)
	}

	common := `"name": "deepwiki", "title": "Wiki", "description": "Wiki", "server": "https://a.example", "icon_url": null`
	srv.AssertJSONBodies(connectorUpdateRoute,
		`{"protocol": "mcp", `+common+`, "system_prompt": null}`,
		`{"protocol": "http", `+common+`}`,
		`{"protocol": "http", `+common+`}`)
	if got := srv.Requests(connectorUpdateRoute)[0].Path; got != "/v1/connectors/c-1" {
		t.Errorf("path = %s", got)
	}
}

// A read takes the ID or the name, and is retried after a 5xx.
func TestGetAndDeleteConnector(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond(connectorRoute, http.StatusOK, json.RawMessage(`{`+connectorResponseFields+`}`))
	srv.Respond(connectorDeleteRoute, http.StatusOK, map[string]any{"message": "Connector deleted"})
	c := newTestClient(srv)
	ctx := context.Background()

	srv.FailNext(connectorRoute, http.StatusBadGateway, 1)
	if got, err := c.GetConnector(ctx, "deepwiki"); err != nil || got.ID != "c-1" {
		t.Errorf("GetConnector = %+v, %v", got, err)
	}
	if err := c.DeleteConnector(ctx, "c-1"); err != nil {
		t.Error(err)
	}
	srv.AssertCalled(connectorRoute, 2)
	if got := srv.Requests(connectorRoute)[1].Path; got != "/v1/connectors/deepwiki" {
		t.Errorf("path = %s", got)
	}
}

// The share calls take no body; their PUT is retried like a read.
func TestShareAndUnshareConnector(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	answer := map[string]any{"message": "ok"}
	srv.Respond("PUT /v1/connectors/{id}/share", http.StatusOK, answer)
	srv.Respond("DELETE /v1/connectors/{id}/share", http.StatusOK, answer)
	srv.Respond("PUT /v1/connectors/{id}/organization/share", http.StatusOK, answer)
	srv.Respond("DELETE /v1/connectors/{id}/organization/share", http.StatusOK, answer)
	c := newTestClient(srv)
	ctx := context.Background()

	srv.FailNext("PUT /v1/connectors/{id}/share", http.StatusServiceUnavailable, 1)
	for _, err := range []error{
		c.ShareConnector(ctx, "c-1", ConnectorSharedWorkspace),
		c.UnshareConnector(ctx, "c-1", ConnectorSharedWorkspace),
		c.ShareConnector(ctx, "c-1", ConnectorSharedOrg),
		c.UnshareConnector(ctx, "c-1", ConnectorSharedOrg),
	} {
		if err != nil {
			t.Error(err)
		}
	}
	srv.AssertCalled("PUT /v1/connectors/{id}/share", 2)
	srv.AssertCalled("DELETE /v1/connectors/{id}/organization/share", 1)
	for _, r := range srv.Requests("") {
		if len(r.Body) != 0 {
			t.Errorf("%s %s sent a body: %s", r.Method, r.Path, r.Body)
		}
	}
}

// private and shared_global have no share call: the client refuses them
// without a request.
func TestShareConnectorRefusesAVisibilityWithoutShareCall(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	c := newTestClient(srv)
	if err := c.ShareConnector(context.Background(), "c-1", ConnectorPrivate); err == nil {
		t.Error("sharing to private must fail")
	}
	if err := c.UnshareConnector(context.Background(), "c-1", "shared_global"); err == nil {
		t.Error("unsharing shared_global must fail")
	}
	srv.AssertCalled("", 0)
}

// The list decodes the credentials, reads a null list as empty, and
// returns the 404 of a missing connector.
func TestListConnectorCredentials(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond(credentialsListRoute, http.StatusOK, json.RawMessage(`{"credentials": [
		{"name": "ci", "title": null, "authentication_type": "bearer", "scope": "workspace", "is_default": true, "status": null}
	], "connector_preset_credentials_for_auth": []}`))
	got, err := newTestClient(srv).ListConnectorCredentials(context.Background(), "c-1", CredentialsScopeWorkspace)
	if err != nil || len(got) != 1 || got[0].Name != "ci" || !got[0].IsDefault || got[0].AuthenticationType != "bearer" {
		t.Errorf("ListConnectorCredentials = %+v, %v", got, err)
	}
	if p := srv.Requests(credentialsListRoute)[0].Path; p != "/v1/connectors/c-1/workspace/credentials" {
		t.Errorf("path = %s", p)
	}

	empty := sdktest.NewServer(t)
	empty.Respond(credentialsListRoute, http.StatusOK, map[string]any{"credentials": nil})
	if got, err := newTestClient(empty).ListConnectorCredentials(context.Background(), "c-1", CredentialsScopeOrganization); err != nil || got == nil || len(got) != 0 {
		t.Errorf("an answer without credentials must be an empty list: %#v, %v", got, err)
	}
	missing := sdktest.NewServer(t)
	missing.Respond(credentialsListRoute, http.StatusNotFound, map[string]any{"detail": "Connector not found"})
	if _, err := newTestClient(missing).ListConnectorCredentials(context.Background(), "c-1", CredentialsScopeWorkspace); !IsNotFound(err) {
		t.Errorf("err = %v, want a 404", err)
	}
}

// A create of credentials is not replayed after a 5xx; their PATCH, a
// documented "create or update", is.
func TestConnectorCredentialsWrites(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	answer := map[string]any{"message": "ok"}
	srv.Respond(credentialsCreateRoute, http.StatusOK, answer)
	srv.Respond(credentialsUpdateRoute, http.StatusOK, answer)
	srv.Respond(credentialsDeleteRoute, http.StatusOK, answer)
	c := newTestClient(srv)
	ctx := context.Background()

	create := ConnectorCredentialsRequest{Name: "ci", Credentials: json.RawMessage(`{"bearer_token":"token"}`)}
	srv.FailNext(credentialsCreateRoute, http.StatusInternalServerError, 1)
	if err := c.CreateConnectorCredentials(ctx, "c-1", CredentialsScopeWorkspace, create); StatusCode(err) != http.StatusInternalServerError {
		t.Errorf("a create after a 500 must not be replayed: %v", err)
	}
	if err := c.CreateConnectorCredentials(ctx, "c-1", CredentialsScopeWorkspace, create); err != nil {
		t.Error(err)
	}
	srv.FailNext(credentialsUpdateRoute, http.StatusServiceUnavailable, 1)
	if err := c.UpdateConnectorCredentials(ctx, "c-1", CredentialsScopeOrganization, ConnectorCredentialsRequest{Name: "ci", Title: new("CI"), IsDefault: new(true)}); err != nil {
		t.Error(err)
	}
	if err := c.DeleteConnectorCredentials(ctx, "c-1", CredentialsScopeWorkspace, "ci"); err != nil {
		t.Error(err)
	}

	srv.AssertJSONBodies(credentialsCreateRoute,
		`{"name": "ci", "title": null, "credentials": {"bearer_token": "token"}}`,
		`{"name": "ci", "title": null, "credentials": {"bearer_token": "token"}}`)
	srv.AssertJSONBodies(credentialsUpdateRoute,
		`{"name": "ci", "title": "CI", "is_default": true}`,
		`{"name": "ci", "title": "CI", "is_default": true}`)
	if p := srv.Requests(credentialsUpdateRoute)[0].Path; p != "/v1/connectors/c-1/organization/credentials" {
		t.Errorf("path = %s", p)
	}
	if p := srv.Requests(credentialsDeleteRoute)[0].Path; p != "/v1/connectors/c-1/workspace/credentials/ci" {
		t.Errorf("path = %s", p)
	}
}

// The calls return the error of the API with its status.
func TestConnectorCallsReturnTheAPIError(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond(connectorRoute, http.StatusNotFound, map[string]any{"detail": "Connector not found"})
	srv.Respond(connectorUpdateRoute, http.StatusUnprocessableEntity, map[string]any{"detail": "Extra inputs are not permitted"})
	srv.Respond(connectorsRoute, http.StatusConflict, map[string]any{"detail": "A connector with this name already exists"})
	srv.Respond("PUT /v1/connectors/{id}/share", http.StatusForbidden, map[string]any{"detail": "Forbidden"})
	c := newTestClient(srv)
	ctx := context.Background()

	if _, err := c.GetConnector(ctx, "c-1"); !IsNotFound(err) {
		t.Errorf("GetConnector err = %v, want a 404", err)
	}
	if _, err := c.UpdateConnector(ctx, "c-1", ConnectorUpdate{}); StatusCode(err) != http.StatusUnprocessableEntity {
		t.Errorf("UpdateConnector err = %v, want a 422", err)
	}
	if _, err := c.CreateConnector(ctx, ConnectorRequest{}); !IsConflict(err) {
		t.Errorf("CreateConnector err = %v, want a 409", err)
	}
	if err := c.ShareConnector(ctx, "c-1", ConnectorSharedWorkspace); StatusCode(err) != http.StatusForbidden {
		t.Errorf("ShareConnector err = %v, want a 403", err)
	}
}
