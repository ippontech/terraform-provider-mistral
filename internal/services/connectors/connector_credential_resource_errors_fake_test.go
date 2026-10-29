// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package connectors_test

import (
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/ippontech/terraform-provider-mistral/internal/providertest"
	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

// A POST of credentials that meets a 5xx is sent once: it is not an
// upsert, and the credentials may exist.
func TestConnectorCredentialResourceCreateIsSentOnceOnA5xx(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: connectorConfig(minimalConnector)},
			{
				PreConfig:   func() { f.FailNext(routeCreateCredentials, 500, 1) },
				Config:      credentialConfig(bearer(firstToken, 1)),
				ExpectError: regexp.MustCompile(`Cannot\s+create\s+the\s+connector\s+credentials(.|\n)*500`),
			},
		},
	})
	f.AssertCalled(routeCreateCredentials, 1)
}

// The PATCH is "create or update", an upsert: a 503 is retried.
func TestConnectorCredentialResourceUpdateIsRetriedOnA5xx(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: credentialConfig(bearer(firstToken, 1))},
			{
				PreConfig: func() { f.FailNext(routeUpdateCredentials, 503, 1) },
				Config:    credentialConfig(bearer(secondToken, 2)),
			},
		},
	})
	f.AssertCalled(routeUpdateCredentials, 2)
}

// Each failing call of a step is reported with the message of the API: the
// list before a create, the create, the refresh, an update the API refuses
// and the delete.
func TestConnectorCredentialResourceReportsErrors(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{
				PreConfig:   func() { f.FailNextWith(routeListCredentials, 403, "Forbidden", 1) },
				Config:      credentialConfig(bearer(firstToken, 1)),
				ExpectError: regexp.MustCompile(`Cannot\s+create\s+the\s+connector\s+credentials(.|\n)*403`),
			},
			{
				PreConfig:   func() { f.FailNextWith(routeCreateCredentials, 422, "Invalid credentials", 1) },
				Config:      credentialConfig(bearer(firstToken, 1)),
				ExpectError: regexp.MustCompile(`Cannot\s+create\s+the\s+connector\s+credentials(.|\n)*Invalid\s+credentials`),
			},
			{Config: credentialConfig(bearer(firstToken, 1))},
			{
				PreConfig:   func() { f.FailNext(routeListCredentials, 500, 4) },
				Config:      credentialConfig(bearer(firstToken, 1)),
				ExpectError: regexp.MustCompile(`Cannot\s+read\s+the\s+connector\s+credentials(.|\n)*500`),
			},
			{
				// The API refuses to demote the default credentials.
				Config:      credentialConfig(bearer(firstToken, 1) + "  is_default = false\n"),
				ExpectError: regexp.MustCompile(`Cannot\s+update\s+the\s+connector\s+credentials(.|\n)*Promote\s+another`),
			},
			{
				PreConfig:   func() { f.FailNext(routeDeleteCredentials, 500, 4) },
				Config:      connectorConfig(minimalConnector),
				ExpectError: regexp.MustCompile(`Cannot\s+delete\s+the\s+connector\s+credentials(.|\n)*500`),
			},
		},
	})
}

// cannedCredentials is a server whose create of credentials succeeds and
// whose list answers what list returns for the n-th call, from 1: the cases
// the fake cannot produce, of an API that accepts credentials it then does
// not list.
func cannedCredentials(t *testing.T, list func(n int64) (int, any)) *sdktest.Server {
	t.Helper()
	srv := sdktest.NewServer(t)
	var calls atomic.Int64
	srv.Handle(routeListCredentials, func(w http.ResponseWriter, _ *http.Request) {
		status, body := list(calls.Add(1))
		sdktest.WriteJSON(w, status, body)
	})
	srv.Respond(routeCreateCredentials, http.StatusOK, map[string]any{"message": "Credentials created"})
	return srv
}

// standaloneCredential is credentials of a connector given by its ID.
const standaloneCredential = `
resource "mistral_connector_credential" "ci" {
  connector_id   = "00000000-0000-4000-8000-000000000001"
  scope          = "organization"
  name           = "ci"
  credentials_wo = jsonencode({ headers = { "X-Api-Key" = "fake" } })
}
`

// Credentials the create accepted but the list does not return fail the
// apply, instead of writing a state the next refresh would remove.
func TestConnectorCredentialResourceReportsCredentialsTheListDoesNotReturn(t *testing.T) {
	t.Parallel()
	empty := map[string]any{"credentials": []any{}}
	srv := cannedCredentials(t, func(int64) (int, any) { return http.StatusOK, empty })

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv),
		Steps: []resource.TestStep{{
			Config:      providertest.ProviderConfig() + standaloneCredential,
			ExpectError: regexp.MustCompile(`accepted\s+the\s+credentials\s+"ci",\s+but\s+does\s+not\s+list\s+them\s+for\s+the\s+organization\s+scope`),
		}},
	})
	srv.AssertJSONBodies(routeCreateCredentials, `{"name": "ci", "title": null, "credentials": {"headers": {"X-Api-Key": "fake"}}}`)
	if p := srv.Requests(routeCreateCredentials)[0].Path; p != "/v1/connectors/00000000-0000-4000-8000-000000000001/organization/credentials" {
		t.Errorf("path = %s", p)
	}
}

// A failure of the list that follows the create is reported.
func TestConnectorCredentialResourceReportsTheReadAfterACreate(t *testing.T) {
	t.Parallel()
	srv := cannedCredentials(t, func(n int64) (int, any) {
		if n == 1 {
			return http.StatusOK, map[string]any{"credentials": []any{}}
		}
		return http.StatusForbidden, map[string]any{"detail": "Forbidden"}
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv),
		Steps: []resource.TestStep{{
			Config:      providertest.ProviderConfig() + standaloneCredential,
			ExpectError: regexp.MustCompile(`Cannot\s+read\s+the\s+connector\s+credentials(.|\n)*403`),
		}},
	})
}

// Invalid arguments fail the plan, before any call.
func TestConnectorCredentialResourceValidatesItsArguments(t *testing.T) {
	t.Parallel()
	f := newFake(t)
	credential := func(scope, name, credentials string) string {
		return providertest.ProviderConfig() + `
resource "mistral_connector_credential" "ci" {
  connector_id   = "c-1"
  scope          = "` + scope + `"
  name           = "` + name + `"
  credentials_wo = ` + credentials + `
}
`
	}
	token := `jsonencode({ bearer_token = "fake" })`
	cases := map[string]struct{ config, err string }{
		"user scope":          {credential("user", "ci", token), `value\s+must\s+be\s+one\s+of`},
		"name with a space":   {credential("workspace", "c i", token), `1\s+to\s+64\s+letters`},
		"not a JSON object":   {credential("workspace", "ci", `jsonencode(["fake"])`), `must\s+be\s+a\s+JSON\s+object`},
		"not JSON":            {credential("workspace", "ci", `"bearer fake"`), `must\s+be\s+a\s+JSON\s+object`},
		"unknown field":       {credential("workspace", "ci", `jsonencode({ token = "fake" })`), `"token"\s+is\s+not\s+one\s+of\s+headers`},
		"headers not strings": {credential("workspace", "ci", `jsonencode({ headers = { a = 1 } })`), `"headers"\s+must\s+be\s+an\s+object\s+of\s+strings`},
		"token not a string":  {credential("workspace", "ci", `jsonencode({ bearer_token = ["fake"] })`), `"bearer_token"\s+must\s+be\s+a\s+string`},
		"oauth not an object": {credential("workspace", "ci", `jsonencode({ oauth = "fake" })`), `"oauth"\s+must\s+be\s+an\s+object`},
		"connector with a colon": {
			strings.Replace(credential("workspace", "ci", token), `"c-1"`, `"c:1"`, 1), `must\s+not\s+be\s+empty\s+or\s+contain\s+a\s+colon`,
		},
	}
	// The parallel subtests run once this function returns: the cleanup
	// checks the fake after all of them.
	t.Cleanup(func() { f.AssertCalled("", 0) })
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
				Steps: []resource.TestStep{{
					Config:      c.config,
					PlanOnly:    true,
					ExpectError: regexp.MustCompile(c.err),
				}},
			})
		})
	}
}

// An import ID needs three non-empty parts and a supported scope.
func TestConnectorCredentialResourceRejectsAMalformedImportID(t *testing.T) {
	t.Parallel()
	f := newFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
		Steps: []resource.TestStep{
			{Config: credentialConfig(bearer(firstToken, 1))},
			{
				ResourceName:  credentialAddr,
				ImportState:   true,
				ImportStateId: "c-1:workspace",
				ExpectError:   regexp.MustCompile(`Invalid\s+import\s+ID(.|\n)*<connector_id>:<scope>:<name>`),
			},
			{
				ResourceName:  credentialAddr,
				ImportState:   true,
				ImportStateId: "c-1::ci",
				ExpectError:   regexp.MustCompile(`Invalid\s+import\s+ID(.|\n)*<connector_id>:<scope>:<name>`),
			},
			{
				ResourceName:  credentialAddr,
				ImportState:   true,
				ImportStateId: "c-1:user:ci",
				ExpectError:   regexp.MustCompile(`Invalid\s+import\s+ID(.|\n)*workspace\s+or\s+organization,\s+got\s+"user"`),
			},
		},
	})
}
