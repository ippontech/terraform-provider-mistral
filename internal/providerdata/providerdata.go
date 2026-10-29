// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package providerdata holds what the provider's Configure hands to every
// resource and data source: the resolved provider configuration.
//
// It does not import internal/provider, so the service packages can depend on
// it without an import cycle.
package providerdata

import (
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
)

// Audience names the credential a type's endpoints accept (CLAUDE.md,
// "Credentials").
type Audience int

const (
	// Workspace is a standard API key (api_key), scoped to one workspace:
	// every endpoint outside /v1/admin.
	Workspace Audience = iota + 1
	// Admin is an Admin API key (admin_api_key), tied to one organization:
	// the /v1/admin endpoints, Enterprise plans only.
	Admin
)

func (a Audience) String() string {
	switch a {
	case Workspace:
		return "api_key"
	case Admin:
		return "admin_api_key"
	}
	return fmt.Sprintf("Audience(%d)", int(a))
}

// envVar is the environment variable the provider reads for a's credential.
func (a Audience) envVar() string {
	switch a {
	case Workspace:
		return "MISTRAL_API_KEY"
	case Admin:
		return "MISTRAL_ADMIN_API_KEY"
	}
	return ""
}

// ProviderData is the resolved provider configuration. The same value is set
// as ResourceData, DataSourceData and, later, the data of the other kinds.
type ProviderData struct {
	// ServerURL is the API base URL, without a trailing slash.
	ServerURL string
	// APIKey is the standard API key, empty when neither api_key nor
	// MISTRAL_API_KEY is set.
	APIKey string
	// AdminAPIKey is the Admin API key, empty when neither admin_api_key nor
	// MISTRAL_ADMIN_API_KEY is set.
	AdminAPIKey string
	// UserAgent is sent on every request.
	UserAgent string
}

// Credential returns the credential for a, or an error diagnostic naming the
// provider argument and environment variable to set when it is missing. A
// resource or data source calls it from its Configure, so a configuration
// that never uses a type of an audience does not need its credential.
func (pd *ProviderData) Credential(a Audience) (string, diag.Diagnostics) {
	var diags diag.Diagnostics
	var value string
	switch a {
	case Workspace:
		value = pd.APIKey
	case Admin:
		value = pd.AdminAPIKey
	}
	if value == "" {
		diags.AddError(
			"Missing Mistral credential",
			fmt.Sprintf("This resource or data source calls an endpoint that needs the %q provider argument, "+
				"or the %s environment variable, and neither is set.", a.String(), a.envVar()),
		)
	}
	return value, diags
}
