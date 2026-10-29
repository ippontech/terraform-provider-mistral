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

// The objects of the test organization that the acceptance tests use but
// cannot create, one environment variable each. No call of the provider
// reads the workspace of a standard key, and the keys alone cannot tell which
// users a test may add to a workspace or a group. testacc.yml passes them
// from the variables of the acctest environment; Fixture reads them.
const (
	// EnvWorkspaceID is the ID of the workspace of MISTRAL_API_KEY.
	EnvWorkspaceID = "MISTRAL_ACC_WORKSPACE_ID"
	// EnvAdminUserID is the ID of an organization admin, whom the tests
	// make the Workspace Admin of the workspaces they create.
	EnvAdminUserID = "MISTRAL_ACC_ADMIN_USER_ID"
	// EnvMemberUserID is the ID of another user of the organization, a
	// member of the workspace of MISTRAL_API_KEY but not the owner of the
	// keys: the tests add it to workspaces and groups, give it an API key
	// and share a library with it.
	EnvMemberUserID = "MISTRAL_ACC_MEMBER_USER_ID"
)

// Fixture returns the value of name, one of the Env variables, and skips t
// when it is not set. It skips t unless TF_ACC is set first, so a unit test
// run gives the usual reason.
func Fixture(t *testing.T, name string) string {
	t.Helper()
	SkipUnlessAcc(t)
	v := os.Getenv(name)
	if v == "" {
		t.Skipf("%s is not set: %s needs this object of the test organization", name, t.Name())
	}
	return v
}

// RandomName returns "tf-acc-<prefix>-<8 hex characters>", so concurrent
// runs and leftovers of a failed run do not collide.
func RandomName(prefix string) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "tf-acc-" + prefix + "-" + hex.EncodeToString(b)
}
