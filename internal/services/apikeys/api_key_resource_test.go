// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package apikeys_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-mistral/internal/acctest"
)

// envKeyWorkspaceID and envKeyUserID name the workspace and the user of the
// test organization the acceptance test creates a key for: the Admin API key
// alone cannot tell which ones exist.
const (
	envKeyWorkspaceID = "MISTRAL_ACC_WORKSPACE_ID"
	envKeyUserID      = "MISTRAL_ACC_MEMBER_USER_ID"
)

// TestAccAPIKeyResource creates an API key that expires in a year, replaces
// it by renaming it, imports it, and deletes it, against the live API.
func TestAccAPIKeyResource(t *testing.T) {
	name := acctest.RandomName("key")
	workspace, user := os.Getenv(envKeyWorkspaceID), os.Getenv(envKeyUserID)
	config := func(keyName string) string {
		return fmt.Sprintf(`
resource "mistral_api_key" "this" {
  workspace_id = %q
  user_id      = %q
  name         = %q
  expiration   = formatdate("YYYY-MM-DD", timeadd(plantimestamp(), "8760h"))

  lifecycle {
    ignore_changes = [expiration]
  }
}
`, workspace, user, keyName)
	}

	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			acctest.PreCheckAdmin(t)
			for _, v := range []string{envKeyWorkspaceID, envKeyUserID} {
				if os.Getenv(v) == "" {
					t.Skipf("%s is not set: the API key acceptance test is skipped", v)
				}
			}
		},
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config(name),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("mistral_api_key.this", tfjsonpath.New("name"), knownvalue.StringExact(name)),
					statecheck.ExpectKnownValue("mistral_api_key.this", tfjsonpath.New("workspace_id"), knownvalue.StringExact(workspace)),
					statecheck.ExpectKnownValue("mistral_api_key.this", tfjsonpath.New("key"), knownvalue.NotNull()),
					statecheck.ExpectKnownValue("mistral_api_key.this", tfjsonpath.New("expiration_date"), knownvalue.NotNull()),
				},
			},
			{Config: config(name + "-renamed")},
			{
				ResourceName:            "mistral_api_key.this",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"key", "user_id", "expiration"},
			},
		},
	})
}
