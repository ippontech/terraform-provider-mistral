// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package providerdata

import (
	"github.com/hashicorp/terraform-plugin-framework/diag"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
)

// Configure is the shared Configure of every resource and data source: it
// takes the provider data Terraform passes in (nil before the provider is
// configured, which is not an error) and checks that the credential of a is
// set. It returns the client, or nil when the type must not go on.
func Configure(providerData any, a Audience, diags *diag.Diagnostics) *client.Client {
	if providerData == nil {
		return nil
	}
	pd, ok := providerData.(*ProviderData)
	if !ok {
		diags.AddError("Unexpected provider data",
			"Expected *providerdata.ProviderData. This is a bug of the provider: please report it.")
		return nil
	}
	if _, d := pd.Credential(a); d.HasError() {
		diags.Append(d...)
		return nil
	}
	return pd.Client
}
