// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// Connector protocols: the discriminator of the create and update bodies and
// of the Connector response (https://docs.mistral.ai/api/endpoint/beta/connectors).
const (
	ConnectorProtocolMCP  = "mcp"
	ConnectorProtocolHTTP = "http"
)

// Connector visibilities (PublicResourceVisibility in the specification). A
// response may also answer shared_global, for the connectors Mistral
// publishes to everyone.
const (
	ConnectorPrivate         = "private"
	ConnectorSharedWorkspace = "shared_workspace"
	ConnectorSharedOrg       = "shared_org"
)

// Connector is an MCP or HTTP connector (MCPConnector and HTTPConnector in
// the specification, which share every field the provider reads).
type Connector struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Title        *string   `json:"title"`
	Description  string    `json:"description"`
	Server       *string   `json:"server"`
	IconURL      *string   `json:"icon_url"`
	SystemPrompt *string   `json:"system_prompt"`
	Visibility   string    `json:"visibility"`
	Protocol     string    `json:"protocol"`
	CreatedAt    time.Time `json:"created_at"`
	ModifiedAt   time.Time `json:"modified_at"`
}

// ConnectorRequest is the body of a create (CreateConnectorRequest, or
// CreateHTTPConnectorRequest when Protocol is http). Every field is sent,
// null included.
type ConnectorRequest struct {
	Protocol     string  `json:"protocol"`
	Name         string  `json:"name"`
	Title        *string `json:"title"`
	Description  string  `json:"description"`
	Server       string  `json:"server"`
	IconURL      *string `json:"icon_url"`
	SystemPrompt *string `json:"system_prompt"`
	Visibility   string  `json:"visibility"`
}

// ConnectorUpdate is the body of an update (ConnectorMCPPublicUpdate, or
// UpdateHTTPConnectorRequest when Protocol is http). Every field it holds is
// sent, null included, so that an update clears what the configuration
// removed. It has no visibility: the share calls change it.
type ConnectorUpdate struct {
	Protocol    string
	Name        string
	Title       *string
	Description string
	Server      string
	IconURL     *string
	// SystemPrompt is sent for an MCP connector only: both update schemas
	// refuse unknown fields (additionalProperties: false), and the HTTP one
	// has no system_prompt.
	SystemPrompt *string
}

// connectorUpdateFields are the fields both update schemas accept.
type connectorUpdateFields struct {
	Protocol    string  `json:"protocol"`
	Name        string  `json:"name"`
	Title       *string `json:"title"`
	Description string  `json:"description"`
	Server      string  `json:"server"`
	IconURL     *string `json:"icon_url"`
}

// MarshalJSON encodes the fields of the update schema of u.Protocol, and
// only those.
func (u ConnectorUpdate) MarshalJSON() ([]byte, error) {
	common := connectorUpdateFields{
		Protocol: u.Protocol, Name: u.Name, Title: u.Title,
		Description: u.Description, Server: u.Server, IconURL: u.IconURL,
	}
	if u.Protocol != ConnectorProtocolMCP {
		return json.Marshal(common)
	}
	return json.Marshal(struct {
		connectorUpdateFields
		SystemPrompt *string `json:"system_prompt"`
	}{common, u.SystemPrompt})
}

// Consumer scopes of connector credentials: the {consumer_scope} path
// segment. The list answers the scope as a ConsumerType instead, where the
// organization is "org".
const (
	CredentialsScopeWorkspace    = "workspace"
	CredentialsScopeOrganization = "organization"
	CredentialsScopeUser         = "user"
)

// ConnectorCredentials is a named set of credentials of a connector, as the
// list returns it (AuthenticationConfiguration in the specification). The
// API never returns the secret values.
type ConnectorCredentials struct {
	Name               string  `json:"name"`
	Title              *string `json:"title"`
	AuthenticationType string  `json:"authentication_type"`
	IsDefault          bool    `json:"is_default"`
}

// ConnectorCredentialsRequest is the body of a create and of an update of
// credentials (CredentialsCreateOrUpdate).
type ConnectorCredentialsRequest struct {
	Name  string  `json:"name"`
	Title *string `json:"title"`
	// IsDefault is left out when nil: the specification defines what an
	// omitted is_default does on a create and on an update.
	IsDefault *bool `json:"is_default,omitempty"`
	// Credentials is the ConnectionCredentialsInput object (headers,
	// bearer_token or oauth). An update leaves it out unless the secret
	// changes.
	Credentials json.RawMessage `json:"credentials,omitempty"`
}

// CreateConnector creates a connector: POST /v1/connectors. It is not
// idempotent.
func (cl *Client) CreateConnector(ctx context.Context, req ConnectorRequest) (*Connector, error) {
	var out Connector
	if err := cl.do(ctx, call{method: http.MethodPost, path: "/v1/connectors", body: req, audience: workspaceKey}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetConnector returns the connector idOrName, its ID or its name, which is
// unique in a workspace: GET /v1/connectors/{connector_id_or_name}.
func (cl *Client) GetConnector(ctx context.Context, idOrName string) (*Connector, error) {
	var out Connector
	if err := cl.do(ctx, call{method: http.MethodGet, path: escapePath("/v1/connectors", idOrName), audience: workspaceKey}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateConnector updates the connector id with req: PATCH
// /v1/connectors/{connector_id}, which takes the ID, not the name. It is not
// documented as idempotent, so it is not replayed after a 5xx.
func (cl *Client) UpdateConnector(ctx context.Context, id string, req ConnectorUpdate) (*Connector, error) {
	var out Connector
	if err := cl.do(ctx, call{method: http.MethodPatch, path: escapePath("/v1/connectors", id), body: req, audience: workspaceKey}, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteConnector deletes the connector id: DELETE /v1/connectors/{connector_id}.
func (cl *Client) DeleteConnector(ctx context.Context, id string) error {
	return cl.do(ctx, call{method: http.MethodDelete, path: escapePath("/v1/connectors", id), audience: workspaceKey}, nil)
}

// ShareConnector shares the private connector id with the workspace of the
// key (visibility shared_workspace, PUT /v1/connectors/{connector_id}/share)
// or with its organization (shared_org, PUT .../organization/share). The PUT
// takes no body and is retried like a read.
func (cl *Client) ShareConnector(ctx context.Context, id, visibility string) error {
	p, err := sharePath(id, visibility)
	if err != nil {
		return err
	}
	return cl.do(ctx, call{method: http.MethodPut, path: p, audience: workspaceKey}, nil)
}

// UnshareConnector makes the connector id, shared with visibility
// (shared_workspace or shared_org), private again: DELETE on the path of
// ShareConnector. The API then removes the credentials of the scope it was
// shared with, except the creator's.
func (cl *Client) UnshareConnector(ctx context.Context, id, visibility string) error {
	p, err := sharePath(id, visibility)
	if err != nil {
		return err
	}
	return cl.do(ctx, call{method: http.MethodDelete, path: p, audience: workspaceKey}, nil)
}

// sharePath is the share path of a shared visibility. Private and
// shared_global have none.
func sharePath(id, visibility string) (string, error) {
	switch visibility {
	case ConnectorSharedWorkspace:
		return escapePath("/v1/connectors", id, "share"), nil
	case ConnectorSharedOrg:
		return escapePath("/v1/connectors", id, "organization", "share"), nil
	}
	return "", fmt.Errorf("mistral: no share call for the connector visibility %q", visibility)
}

// ListConnectorCredentials returns every set of credentials of the
// connector for the consumer scope: GET
// /v1/connectors/{connector_id_or_name}/{workspace,organization,user}/credentials,
// which is not paginated and has no read by name.
func (cl *Client) ListConnectorCredentials(ctx context.Context, connector, scope string) ([]ConnectorCredentials, error) {
	var out struct {
		Credentials []ConnectorCredentials `json:"credentials"`
	}
	if err := cl.do(ctx, call{method: http.MethodGet, path: credentialsPath(connector, scope), audience: workspaceKey}, &out); err != nil {
		return nil, err
	}
	if out.Credentials == nil {
		out.Credentials = []ConnectorCredentials{}
	}
	return out.Credentials, nil
}

// CreateConnectorCredentials creates a set of credentials: POST
// /v1/connectors/{connector_id_or_name}/{consumer_scope}/credentials. It is
// not idempotent.
func (cl *Client) CreateConnectorCredentials(ctx context.Context, connector, scope string, req ConnectorCredentialsRequest) error {
	return cl.do(ctx, call{method: http.MethodPost, path: credentialsPath(connector, scope), body: req, audience: workspaceKey}, nil)
}

// UpdateConnectorCredentials updates the set of credentials req.Name: PATCH
// on the path of CreateConnectorCredentials, which the specification
// documents as "Create or update consumer credentials", an upsert: it is
// retried like a read.
func (cl *Client) UpdateConnectorCredentials(ctx context.Context, connector, scope string, req ConnectorCredentialsRequest) error {
	return cl.do(ctx, call{method: http.MethodPatch, path: credentialsPath(connector, scope), body: req, audience: workspaceKey, idempotent: true}, nil)
}

// DeleteConnectorCredentials deletes the set of credentials name: DELETE
// /v1/connectors/{connector_id_or_name}/{consumer_scope}/credentials/{credentials_name}.
func (cl *Client) DeleteConnectorCredentials(ctx context.Context, connector, scope, name string) error {
	return cl.do(ctx, call{method: http.MethodDelete, path: escapePath(credentialsPath(connector, scope), name), audience: workspaceKey}, nil)
}

// credentialsPath is the credentials collection of a connector for a
// consumer scope.
func credentialsPath(connector, scope string) string {
	return escapePath("/v1/connectors", connector, scope, "credentials")
}
