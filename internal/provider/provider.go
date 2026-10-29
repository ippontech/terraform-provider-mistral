// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package provider is the Mistral AI provider: its configuration and the
// registry of its resources and data sources.
package provider

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-mistral/internal/providerdata"
)

const (
	providerTypeName = "mistral"

	// DefaultServerURL is the API host every endpoint of the provider lives
	// on. The regional hosts (api.eu.mistral.ai, api.us.mistral.ai) serve
	// stateless inference only, none of the provider's objects.
	DefaultServerURL = "https://api.mistral.ai"

	envAPIKey      = "MISTRAL_API_KEY"       //nolint:gosec // G101: the name of the variable, not a credential
	envAdminAPIKey = "MISTRAL_ADMIN_API_KEY" //nolint:gosec // G101: the name of the variable, not a credential
	envServerURL   = "MISTRAL_SERVER_URL"
)

var _ provider.Provider = &MistralProvider{}

// MistralProvider is the provider implementation.
type MistralProvider struct {
	// version is the provider version on release, "dev" for a local build
	// and "test" in tests.
	version string

	// serverURL, when set by WithServerURL, wins over the server_url
	// argument and MISTRAL_SERVER_URL. Empty in every binary users run.
	serverURL string
}

// Option changes how New builds the provider. Options are for tests only:
// main.go calls New without any.
type Option func(*MistralProvider)

// WithServerURL points the provider at serverURL whatever its configuration
// says, so a test runs it against an httptest fake without t.Setenv and can
// call t.Parallel (internal/providertest wraps it). Unlike server_url, it
// accepts a plain-HTTP URL on any host.
func WithServerURL(serverURL string) Option {
	return func(p *MistralProvider) {
		p.serverURL = serverURL
	}
}

// MistralProviderModel is the provider configuration.
type MistralProviderModel struct {
	APIKey      types.String `tfsdk:"api_key"`
	AdminAPIKey types.String `tfsdk:"admin_api_key"`
	ServerURL   types.String `tfsdk:"server_url"`
}

func (p *MistralProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = providerTypeName
	resp.Version = p.version
}

func (p *MistralProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages Mistral AI objects: agents, libraries, connectors, prompts, skills and service accounts " +
			"in a workspace, and the organization through the Admin API.",
		Attributes: map[string]schema.Attribute{
			"api_key": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				Description: "A standard Mistral API key, scoped to the workspace it was created in, for every resource " +
					"and data source outside the Admin API. Prefer the " + envAPIKey + " environment variable: a value " +
					"set here, even from a sensitive variable, can be kept in a saved plan.",
			},
			"admin_api_key": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				Description: "An Admin API key, tied to one organization, for the Admin API resources and data sources " +
					"(workspaces, API keys, invites, user groups, spend limit). The Admin API is available on Enterprise " +
					"plans only, and the key is created in the Admin Panel. Prefer the " + envAdminAPIKey + " " +
					"environment variable: a value set here, even from a sensitive variable, can be kept in a saved plan.",
			},
			"server_url": schema.StringAttribute{
				Optional: true,
				Description: "The API base URL, `" + DefaultServerURL + "` by default. It must use HTTPS, except on a " +
					"loopback address. Can also be set with the " + envServerURL + " environment variable.",
			},
		},
	}
}

func (p *MistralProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var data MistralProviderModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// A value unknown at plan time would be read as unset, and the plan's
	// reads would go out with the environment's credential or none.
	for name, v := range map[string]types.String{
		"api_key":       data.APIKey,
		"admin_api_key": data.AdminAPIKey,
		"server_url":    data.ServerURL,
	} {
		if v.IsUnknown() {
			resp.Diagnostics.AddAttributeError(path.Root(name), "Unknown provider argument",
				fmt.Sprintf("The provider argument %q depends on a value Terraform only knows after apply. "+
					"Set it from a value known at plan time, or apply the configuration that produces it first.", name))
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}

	serverURL := p.serverURL
	if serverURL == "" {
		var err error
		serverURL, err = validateServerURL(resolve(data.ServerURL, envServerURL, DefaultServerURL))
		if err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("server_url"), "Invalid server URL", err.Error())
			return
		}
	}

	pd := &providerdata.ProviderData{
		ServerURL:   strings.TrimRight(serverURL, "/"),
		APIKey:      resolve(data.APIKey, envAPIKey, ""),
		AdminAPIKey: resolve(data.AdminAPIKey, envAdminAPIKey, ""),
		UserAgent:   "terraform-provider-mistral/" + p.version,
	}
	resp.ResourceData = pd
	resp.DataSourceData = pd
}

// resolve returns the configured value, else the environment variable, else
// fallback.
func resolve(configured types.String, envVar, fallback string) string {
	if v := configured.ValueString(); v != "" {
		return v
	}
	if v := os.Getenv(envVar); v != "" {
		return v
	}
	return fallback
}

// validateServerURL accepts an absolute HTTPS URL, or plain HTTP on a
// loopback address for a local mock server.
func validateServerURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("%q is not an absolute URL", raw)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", fmt.Errorf("%q must not have a query or a fragment", raw)
	}
	switch u.Scheme {
	case "https":
		return raw, nil
	case "http":
		if isLoopback(u.Hostname()) {
			return raw, nil
		}
		return "", fmt.Errorf("%q uses plain HTTP on a non-loopback host: the API key would travel unencrypted", raw)
	default:
		return "", fmt.Errorf("%q must use https", raw)
	}
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (p *MistralProvider) Resources(_ context.Context) []func() resource.Resource {
	return factories(resourceRegistrations())
}

func (p *MistralProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return factories(dataSourceRegistrations())
}

// New returns the provider factory providerserver.Serve takes.
func New(version string, opts ...Option) func() provider.Provider {
	return func() provider.Provider {
		p := &MistralProvider{version: version}
		for _, opt := range opts {
			opt(p)
		}
		return p
	}
}
