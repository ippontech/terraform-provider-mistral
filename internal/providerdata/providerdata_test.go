// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package providerdata

import (
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
)

func TestCredentialReturnsTheAudienceKey(t *testing.T) {
	pd := &ProviderData{APIKey: "k", AdminAPIKey: "a"}

	for a, want := range map[Audience]string{Workspace: "k", Admin: "a"} {
		got, diags := pd.Credential(a)
		if diags.HasError() || got != want {
			t.Errorf("Credential(%v) = %q, %v; want %q", a, got, diags, want)
		}
	}
}

func TestCredentialNamesWhatToSet(t *testing.T) {
	tests := map[Audience][]string{
		Workspace: {`"api_key"`, "MISTRAL_API_KEY"},
		Admin:     {`"admin_api_key"`, "MISTRAL_ADMIN_API_KEY"},
	}
	for a, wants := range tests {
		_, diags := (&ProviderData{}).Credential(a)
		if !diags.HasError() {
			t.Fatalf("Credential(%v) without a key returned no error", a)
		}
		detail := diags.Errors()[0].Detail()
		for _, want := range wants {
			if !strings.Contains(detail, want) {
				t.Errorf("Credential(%v) detail %q does not name %s", a, detail, want)
			}
		}
	}
}

// A provider that ignores the environment says so, rather than telling the
// reader to set a variable it would not read.
func TestCredentialSaysTheEnvironmentIsIgnored(t *testing.T) {
	_, diags := (&ProviderData{EnvironmentIgnored: true}).Credential(Admin)
	if !diags.HasError() {
		t.Fatal("Credential without a key returned no error")
	}
	detail := diags.Errors()[0].Detail()
	for _, want := range []string{`"admin_api_key"`, "ignores the environment", "MISTRAL_ADMIN_API_KEY is not read"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail %q does not contain %q", detail, want)
		}
	}
	if strings.Contains(detail, "neither is set") {
		t.Errorf("detail %q blames the environment variable", detail)
	}
}

func TestAudienceString(t *testing.T) {
	if got := Audience(0).String(); got != "Audience(0)" {
		t.Errorf("Audience(0).String() = %q", got)
	}
	if got := Audience(0).envVar(); got != "" {
		t.Errorf("Audience(0).envVar() = %q", got)
	}
}

func TestConfigure(t *testing.T) {
	c := client.New(client.Config{})
	pd := &ProviderData{APIKey: "k", Client: c}

	var diags diag.Diagnostics
	if got := Configure(nil, Workspace, &diags); got != nil || diags.HasError() {
		t.Errorf("Configure(nil) = %v, %v; want nil and no error before the provider is configured", got, diags)
	}
	if got := Configure(pd, Workspace, &diags); got != c || diags.HasError() {
		t.Errorf("Configure(Workspace) = %v, %v", got, diags)
	}
	if got := Configure(pd, Admin, &diags); got != nil || !diags.HasError() {
		t.Errorf("Configure(Admin) without an admin key = %v, %v", got, diags)
	}
	diags = nil
	if got := Configure("not provider data", Workspace, &diags); got != nil || !diags.HasError() {
		t.Errorf("Configure(wrong type) = %v, %v", got, diags)
	}
}
