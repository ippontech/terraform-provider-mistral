// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package connectors_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-mistral/internal/providertest"
	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

const credentialAddr = "mistral_connector_credential.ci"

// credentialConfig is the minimal connector and its workspace credentials
// "ci", whose other arguments are body.
func credentialConfig(body string) string {
	return connectorConfig(minimalConnector) + `
resource "mistral_connector_credential" "ci" {
  connector_id = mistral_connector.this.id
  scope        = "workspace"
  name         = "ci"
` + body + `
}
`
}

// Dummy secrets: only the fake sees them, and no state may hold them.
const (
	firstToken  = "fake-first-token"
	secondToken = "fake-second-token"
)

// bearer is a credentials_wo argument with a bearer token, and its version.
func bearer(token string, version int) string {
	return fmt.Sprintf("  credentials_wo         = jsonencode({ bearer_token = %q })\n  credentials_wo_version = %d\n", token, version)
}

// noSecretInState fails when an attribute of the state holds one of the
// dummy secrets.
func noSecretInState(s *terraform.State) error {
	for addr, rs := range s.RootModule().Resources {
		for k, v := range rs.Primary.Attributes {
			if strings.Contains(v, firstToken) || strings.Contains(v, secondToken) {
				return fmt.Errorf("%s.%s holds a secret: the state must never hold one", addr, k)
			}
		}
	}
	return nil
}

// credentialValue checks one attribute of the credentials.
func credentialValue(attr string, want knownvalue.Check) statecheck.StateCheck {
	return statecheck.ExpectKnownValue(credentialAddr, tfjsonpath.New(attr), want)
}

// Create, a new title without the secret, a rotation of the secret with an
// empty title, then both imports; no step leaves a secret in the state.
func TestConnectorCredentialResourceLifecycle(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	var connectorID string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config:           credentialConfig(`  title = "CI token"` + "\n" + bearer(firstToken, 1)),
				ConfigPlanChecks: providertest.ExpectAction(credentialAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					credentialValue("id", knownvalue.StringRegexp(regexp.MustCompile(`^[0-9a-f-]{36}:workspace:ci$`))),
					credentialValue("title", knownvalue.StringExact("CI token")),
					credentialValue("is_default", knownvalue.Bool(true)),
					credentialValue("authentication_type", knownvalue.StringExact("bearer")),
					credentialValue("credentials_wo", knownvalue.Null()),
					credentialValue("credentials_wo_version", knownvalue.Int64Exact(1)),
				},
				Check: func(s *terraform.State) error {
					connectorID = s.RootModule().Resources[connectorAddr].Primary.ID
					return noSecretInState(s)
				},
			},
			{
				// A new title alone does not send the secret again.
				Config:           credentialConfig(`  title = "CI"` + "\n" + bearer(firstToken, 1)),
				ConfigPlanChecks: providertest.ExpectAction(credentialAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					credentialValue("title", knownvalue.StringExact("CI")),
				},
			},
			{
				// A new version sends the rotated secret; the title is emptied.
				Config:           credentialConfig(bearer(secondToken, 2)),
				ConfigPlanChecks: providertest.ExpectAction(credentialAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					credentialValue("title", knownvalue.Null()),
					credentialValue("is_default", knownvalue.Bool(true)),
					credentialValue("credentials_wo_version", knownvalue.Int64Exact(2)),
				},
				Check: noSecretInState,
			},
			{
				ResourceName:            credentialAddr,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"credentials_wo_version"},
			},
			{
				// The state of an import has no credentials_wo_version: the
				// first apply sends the secret.
				ResourceName:       credentialAddr,
				ImportState:        true,
				ImportStateKind:    resource.ImportBlockWithResourceIdentity,
				ExpectNonEmptyPlan: true,
				ImportPlanChecks: resource.ImportPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(credentialAddr, plancheck.ResourceActionUpdate)},
				},
			},
		},
	})

	f.AssertJSONBodies(routeCreateCredentials, `{"name": "ci", "title": "CI token", "credentials": {"bearer_token": "`+firstToken+`"}}`)
	f.AssertJSONBodies(routeUpdateCredentials,
		`{"name": "ci", "title": "CI"}`,
		`{"name": "ci", "title": null, "credentials": {"bearer_token": "`+secondToken+`"}}`)
	f.AssertCalled(routeDeleteCredentials, 1)
	if want := "/v1/connectors/" + connectorID + "/workspace/credentials/ci"; f.Requests(routeDeleteCredentials)[0].Path != want {
		t.Errorf("DELETE path = %s, want %s", f.Requests(routeDeleteCredentials)[0].Path, want)
	}
	for _, r := range f.Requests(routeListCredentials) {
		if len(r.Query) != 0 {
			t.Errorf("the list sent the query %q", r.Query.Encode())
		}
	}
	f.AssertCredential("", sdktest.APIKey)
}

// The second credentials of a method are not its default; is_default = true
// promotes them, and the first ones, whose is_default the configuration
// leaves to the API, read as false without a plan.
func TestConnectorCredentialResourcePromotesTheDefault(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	backup := func(isDefault string) string {
		return credentialConfig(bearer(firstToken, 1)) + `
resource "mistral_connector_credential" "backup" {
  connector_id   = mistral_connector.this.id
  scope          = "workspace"
  name           = "backup"
  # Unknown at plan time: the validator checks it at apply time.
  credentials_wo = jsonencode({ bearer_token = "${mistral_connector.this.id}-` + secondToken + `" })
  depends_on     = [mistral_connector_credential.ci]
` + isDefault + `
}
`
	}
	const backupAddr = "mistral_connector_credential.backup"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config: backup(""),
				ConfigStateChecks: []statecheck.StateCheck{
					credentialValue("is_default", knownvalue.Bool(true)),
					statecheck.ExpectKnownValue(backupAddr, tfjsonpath.New("is_default"), knownvalue.Bool(false)),
				},
			},
			{
				Config:           backup("  is_default = true"),
				ConfigPlanChecks: providertest.ExpectAction(backupAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(backupAddr, tfjsonpath.New("is_default"), knownvalue.Bool(true)),
				},
			},
			{
				Config:           backup("  is_default = true"),
				ConfigPlanChecks: providertest.ExpectAction(credentialAddr, plancheck.ResourceActionNoop),
				ConfigStateChecks: []statecheck.StateCheck{
					credentialValue("is_default", knownvalue.Bool(false)),
				},
			},
		},
	})
	f.AssertJSONBodies(routeUpdateCredentials, `{"name": "backup", "title": null, "is_default": true}`)
}

// The POST of credentials is not an upsert, and the PATCH is: a create must
// not meet credentials that exist outside Terraform.
func TestConnectorCredentialResourceRefusesToTakeOverCredentials(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	id := f.seed(map[string]any{"name": "studio", "description": "d", "server": "https://a.example", "visibility": "shared_workspace"})
	f.seedCredentials(id, "workspace", "ci", true)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig() + `
resource "mistral_connector_credential" "ci" {
  connector_id   = "` + id + `"
  scope          = "workspace"
  name           = "ci"
  credentials_wo = jsonencode({ headers = {} })
}
`,
			ExpectError: regexp.MustCompile(`Connector\s+credentials\s+already\s+exist(.|\n)*import\s+block\s+\(id\s+=\s+"` + id + `:workspace:ci"\)`),
		}},
	})
	f.AssertCalled(routeCreateCredentials, 0)
	f.AssertCalled(routeUpdateCredentials, 0)
}

// Credentials deleted outside Terraform plan a create.
func TestConnectorCredentialResourceRecreatesCredentialsDeletedOutsideTerraform(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	var key string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config: credentialConfig(bearer(firstToken, 1)),
				Check: func(s *terraform.State) error {
					key = s.RootModule().Resources[credentialAddr].Primary.Attributes["id"]
					return nil
				},
			},
			{
				PreConfig:        func() { f.credentials.Delete(key) },
				Config:           credentialConfig(bearer(firstToken, 1)),
				ConfigPlanChecks: providertest.ExpectAction(credentialAddr, plancheck.ResourceActionCreate),
			},
		},
	})
	f.AssertCalled(routeCreateCredentials, 2)
}

// When the connector is gone, so are its credentials: the list answers 404,
// and Read removes them from the state.
func TestConnectorCredentialResourceFollowsItsConnectorDeletedOutsideTerraform(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	var connectorID string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				Config: credentialConfig(bearer(firstToken, 1)),
				Check: func(s *terraform.State) error {
					connectorID = s.RootModule().Resources[connectorAddr].Primary.ID
					return nil
				},
			},
			{
				PreConfig:        func() { f.connectors.Delete(connectorID) },
				Config:           credentialConfig(bearer(firstToken, 1)),
				ConfigPlanChecks: providertest.ExpectAction(credentialAddr, plancheck.ResourceActionCreate),
			},
		},
	})
}

// Unsharing a connector from the workspace removes its workspace
// credentials (https://docs.mistral.ai/api/endpoint/beta/connectors): the
// next plan creates them again.
func TestConnectorCredentialResourceIsRecreatedAfterAnUnshare(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	private := strings.Replace(credentialConfig(bearer(firstToken, 1)), `"shared_workspace"`, `"private"`, 1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: credentialConfig(bearer(firstToken, 1))},
			{
				Config: private,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(connectorAddr, plancheck.ResourceActionUpdate)},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectResourceAction(credentialAddr, plancheck.ResourceActionCreate)},
				},
				ExpectNonEmptyPlan: true,
			},
			{
				Config:           private,
				ConfigPlanChecks: providertest.ExpectAction(credentialAddr, plancheck.ResourceActionCreate),
			},
		},
	})
	f.AssertCalled(routeUnshare, 1)
	f.AssertCalled(routeCreateCredentials, 2)
}
