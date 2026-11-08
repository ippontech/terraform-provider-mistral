// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package xpprovider exposes the Mistral provider to a Go program that runs it
// in its own process rather than as a plugin of Terraform or OpenTofu. An
// Upjet generated Crossplane provider is one: Upjet takes the provider as a
// terraform-plugin-framework provider.Provider
// (config.WithTerraformPluginFrameworkProvider) and serves it over protocol 6
// in process. The name follows the xpprovider packages that Upjet based
// providers import from their Terraform providers.
//
// It is the only package of this module meant to be imported, and its
// signature is a public Go API: change it only with a major version. Every
// other package is under internal/ and may change in any release.
package xpprovider

import (
	"github.com/hashicorp/terraform-plugin-framework/provider"

	mistral "github.com/ippontech/terraform-provider-mistral/internal/provider"
)

// New returns the Mistral provider as the plugin binary serves it, version
// being the version it reports, such as the version of this module the
// program requires. It takes no option: the options of internal/provider
// (a fake server URL, a short retry policy, ignoring the environment) are
// for tests. Like the plugin, the provider reads MISTRAL_API_KEY,
// MISTRAL_ADMIN_API_KEY and MISTRAL_SERVER_URL for what its configuration
// leaves empty.
func New(version string) provider.Provider {
	return mistral.New(version)()
}
