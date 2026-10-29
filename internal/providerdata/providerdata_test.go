// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package providerdata

import (
	"strings"
	"testing"
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

func TestAudienceString(t *testing.T) {
	if got := Audience(0).String(); got != "Audience(0)" {
		t.Errorf("Audience(0).String() = %q", got)
	}
	if got := Audience(0).envVar(); got != "" {
		t.Errorf("Audience(0).envVar() = %q", got)
	}
}
