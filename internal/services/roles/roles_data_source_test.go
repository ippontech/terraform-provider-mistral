// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package roles_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-mistral/internal/acctest"
)

// TestAccRolesDataSource lists the roles against the live API, and checks
// that their names are the role names the requests take (member,
// workspace_admin), which is our reading of the guide.
func TestAccRolesDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckAdmin(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: `data "mistral_roles" "all" {}`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.mistral_roles.all", tfjsonpath.New("organization_roles"), knownvalue.SetPartial([]knownvalue.Check{
					knownvalue.ObjectPartial(map[string]knownvalue.Check{"name": knownvalue.StringExact("member")}),
				})),
				statecheck.ExpectKnownValue("data.mistral_roles.all", tfjsonpath.New("workspace_roles"), knownvalue.SetPartial([]knownvalue.Check{
					knownvalue.ObjectPartial(map[string]knownvalue.Check{"name": knownvalue.StringExact("workspace_admin")}),
				})),
			},
		}},
	})
}
