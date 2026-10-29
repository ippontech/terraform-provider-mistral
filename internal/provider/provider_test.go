// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
	"github.com/ippontech/terraform-provider-mistral/internal/providerdata"
)

// clearEnv unsets every variable the provider reads, so a test only sees the
// environment it sets itself. t.Setenv runs first for the cleanup it
// registers, which restores the original value at the end of the test.
func clearEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{envAPIKey, envAdminAPIKey, envServerURL} {
		t.Setenv(k, "")
		if err := os.Unsetenv(k); err != nil {
			t.Fatalf("unset %s: %v", k, err)
		}
	}
}

// configure runs Configure against a configuration holding attrs, every other
// attribute null, on a provider built by New with opts.
func configure(t *testing.T, attrs map[string]tftypes.Value, opts ...Option) *provider.ConfigureResponse {
	t.Helper()
	ctx := context.Background()
	p := New("test", opts...)()

	schemaResp := &provider.SchemaResponse{}
	p.Schema(ctx, provider.SchemaRequest{}, schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("provider schema: %v", schemaResp.Diagnostics)
	}
	objType, ok := schemaResp.Schema.Type().TerraformType(ctx).(tftypes.Object)
	if !ok {
		t.Fatal("provider schema is not an object type")
	}
	values := make(map[string]tftypes.Value, len(objType.AttributeTypes))
	for name, attrType := range objType.AttributeTypes {
		if v, found := attrs[name]; found {
			values[name] = v
			continue
		}
		values[name] = tftypes.NewValue(attrType, nil)
	}

	resp := &provider.ConfigureResponse{}
	p.Configure(ctx, provider.ConfigureRequest{
		Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: tftypes.NewValue(objType, values)},
	}, resp)
	return resp
}

func providerData(t *testing.T, resp *provider.ConfigureResponse) *providerdata.ProviderData {
	t.Helper()
	if resp.Diagnostics.HasError() {
		t.Fatalf("Configure returned errors: %v", resp.Diagnostics)
	}
	pd, ok := resp.ResourceData.(*providerdata.ProviderData)
	if !ok {
		t.Fatalf("ResourceData is %T, want *providerdata.ProviderData", resp.ResourceData)
	}
	if resp.DataSourceData != resp.ResourceData {
		t.Error("DataSourceData and ResourceData must be the same ProviderData")
	}
	return pd
}

func str(v string) tftypes.Value { return tftypes.NewValue(tftypes.String, v) }

func TestConfigureWithoutCredentialSucceeds(t *testing.T) {
	clearEnv(t)

	pd := providerData(t, configure(t, nil))

	if pd.APIKey != "" || pd.AdminAPIKey != "" {
		t.Errorf("credentials = %q, %q; want both empty", pd.APIKey, pd.AdminAPIKey)
	}
	if pd.ServerURL != DefaultServerURL {
		t.Errorf("ServerURL = %q, want %q", pd.ServerURL, DefaultServerURL)
	}
	if pd.UserAgent != "terraform-provider-mistral/test" {
		t.Errorf("UserAgent = %q", pd.UserAgent)
	}
	if pd.Client == nil {
		t.Error("Configure built no client")
	}
}

func TestConfiguredClientCallsTheServerWithTheKeys(t *testing.T) {
	clearEnv(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer k" {
			t.Errorf("Authorization = %q", got)
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	pd := providerData(t, configure(t, map[string]tftypes.Value{"api_key": str("k")},
		WithServerURL(srv.URL), WithRetryPolicy(client.RetryPolicy{})))

	if _, err := pd.Client.ListModels(context.Background()); client.StatusCode(err) != http.StatusInternalServerError {
		t.Errorf("err = %v, want the fake's 500", err)
	}
}

func TestConfigureReadsEnvironment(t *testing.T) {
	clearEnv(t)
	t.Setenv(envAPIKey, "env-key")
	t.Setenv(envAdminAPIKey, "env-admin")
	t.Setenv(envServerURL, "https://mistral.example.com/")

	pd := providerData(t, configure(t, nil))

	if pd.APIKey != "env-key" || pd.AdminAPIKey != "env-admin" {
		t.Errorf("credentials = %q, %q", pd.APIKey, pd.AdminAPIKey)
	}
	if pd.ServerURL != "https://mistral.example.com" {
		t.Errorf("ServerURL = %q, want the trailing slash removed", pd.ServerURL)
	}
}

func TestConfigurationWinsOverEnvironment(t *testing.T) {
	clearEnv(t)
	t.Setenv(envAPIKey, "env-key")
	t.Setenv(envAdminAPIKey, "env-admin")
	t.Setenv(envServerURL, "https://env.example.com")

	pd := providerData(t, configure(t, map[string]tftypes.Value{
		"api_key":       str("config-key"),
		"admin_api_key": str("config-admin"),
		"server_url":    str("https://config.example.com"),
	}))

	if pd.APIKey != "config-key" || pd.AdminAPIKey != "config-admin" || pd.ServerURL != "https://config.example.com" {
		t.Errorf("got %+v, want the configured values", *pd)
	}
}

func TestConfigureRejectsUnknownArguments(t *testing.T) {
	for _, name := range []string{"api_key", "admin_api_key", "server_url"} {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			resp := configure(t, map[string]tftypes.Value{
				name: tftypes.NewValue(tftypes.String, tftypes.UnknownValue),
			})
			if !resp.Diagnostics.HasError() {
				t.Fatal("Configure accepted an unknown value")
			}
			if got := resp.Diagnostics.Errors()[0].Summary(); got != "Unknown provider argument" {
				t.Errorf("summary = %q", got)
			}
			if resp.ResourceData != nil {
				t.Error("Configure set ResourceData despite the error")
			}
		})
	}
}

func TestConfigureValidatesServerURL(t *testing.T) {
	tests := map[string]struct {
		url     string
		wantErr string
	}{
		"https":                {url: "https://api.mistral.ai"},
		"http on localhost":    {url: "http://localhost:8080"},
		"http on 127.0.0.1":    {url: "http://127.0.0.1:8080"},
		"http on ::1":          {url: "http://[::1]:8080"},
		"http elsewhere":       {url: "http://api.mistral.ai", wantErr: "plain HTTP"},
		"relative":             {url: "api.mistral.ai", wantErr: "not an absolute URL"},
		"other scheme":         {url: "ftp://api.mistral.ai", wantErr: "must use https"},
		"query":                {url: "https://api.mistral.ai?x=1", wantErr: "query or a fragment"},
		"unparseable":          {url: "https://%zz", wantErr: "not an absolute URL"},
		"https with base path": {url: "https://gateway.example.com/mistral"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			clearEnv(t)
			resp := configure(t, map[string]tftypes.Value{"server_url": str(tc.url)})
			if tc.wantErr == "" {
				providerData(t, resp)
				return
			}
			if !resp.Diagnostics.HasError() {
				t.Fatalf("Configure accepted %q", tc.url)
			}
			if got := resp.Diagnostics.Errors()[0].Detail(); !strings.Contains(got, tc.wantErr) {
				t.Errorf("detail = %q, want it to contain %q", got, tc.wantErr)
			}
		})
	}
}

func TestWithServerURLWinsAndSkipsValidation(t *testing.T) {
	clearEnv(t)
	t.Setenv(envServerURL, "https://env.example.com")

	pd := providerData(t, configure(t, map[string]tftypes.Value{
		"server_url": str("https://config.example.com"),
	}, WithServerURL("http://fake.test:1234/")))

	if pd.ServerURL != "http://fake.test:1234" {
		t.Errorf("ServerURL = %q, want the WithServerURL value", pd.ServerURL)
	}
}

func TestMetadata(t *testing.T) {
	resp := &provider.MetadataResponse{}
	New("1.2.3")().Metadata(context.Background(), provider.MetadataRequest{}, resp)
	if resp.TypeName != "mistral" || resp.Version != "1.2.3" {
		t.Errorf("Metadata = %q, %q", resp.TypeName, resp.Version)
	}
}
