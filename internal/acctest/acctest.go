// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package acctest holds the helpers of the acceptance tests, which run the
// provider against the live API with TF_ACC=1 and create real objects.
package acctest

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"

	"github.com/ippontech/terraform-provider-mistral/internal/provider"
)

// ProtoV6ProviderFactories runs the provider as users do: the credentials
// come from MISTRAL_API_KEY and MISTRAL_ADMIN_API_KEY.
var ProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"mistral": providerserver.NewProtocol6WithError(provider.New("test")()),
}

// SkipUnlessAcc skips t unless TF_ACC is set. A test that prepares live
// fixtures before resource.Test calls it first: resource.Test checks TF_ACC
// only when it runs.
func SkipUnlessAcc(t *testing.T) {
	t.Helper()
	if os.Getenv("TF_ACC") == "" {
		t.Skip("acceptance test: set TF_ACC=1 to run it against the live API")
	}
}

// PreCheck skips t unless TF_ACC is set, and fails it without a standard key.
func PreCheck(t *testing.T) {
	t.Helper()
	SkipUnlessAcc(t)
	if os.Getenv("MISTRAL_API_KEY") == "" {
		t.Fatal("MISTRAL_API_KEY must be set for acceptance tests")
	}
}

// PreCheckAdmin skips t unless TF_ACC is set, and skips it without an Admin
// API key: the Admin API needs an Enterprise plan and access granted by
// Mistral, which a test organization may not have.
func PreCheckAdmin(t *testing.T) {
	t.Helper()
	SkipUnlessAcc(t)
	if os.Getenv("MISTRAL_ADMIN_API_KEY") == "" {
		t.Skip("MISTRAL_ADMIN_API_KEY is not set: Admin API acceptance tests are skipped")
	}
}

// RandomName returns "tf-acc-<prefix>-<8 hex characters>", so concurrent
// runs and leftovers of a failed run do not collide.
func RandomName(prefix string) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "tf-acc-" + prefix + "-" + hex.EncodeToString(b)
}
