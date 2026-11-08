// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package xpprovider_test

import (
	"context"
	"maps"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"

	mistral "github.com/ippontech/terraform-provider-mistral/internal/provider"
	"github.com/ippontech/terraform-provider-mistral/xpprovider"
)

// schemaOver serves p over protocol 6, as Upjet does in process, and returns
// its schema.
func schemaOver(t *testing.T, p provider.Provider) *tfprotov6.GetProviderSchemaResponse {
	t.Helper()
	server, err := providerserver.NewProtocol6WithError(p)()
	if err != nil {
		t.Fatal(err)
	}
	resp, err := server.GetProviderSchema(context.Background(), &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range resp.Diagnostics {
		t.Errorf("schema diagnostic: %s: %s", d.Summary, d.Detail)
	}
	return resp
}

// TestNewServesThePluginProvider checks that the exported provider reports
// the type name and the version it is given, and serves over protocol 6 the
// resources and data sources of the plugin binary, which main.go builds with
// internal/provider.New.
func TestNewServesThePluginProvider(t *testing.T) {
	t.Parallel()
	exported := xpprovider.New("1.2.3")

	var meta provider.MetadataResponse
	exported.Metadata(context.Background(), provider.MetadataRequest{}, &meta)
	if meta.TypeName != "mistral" || meta.Version != "1.2.3" {
		t.Errorf("Metadata = %q %q, want mistral 1.2.3", meta.TypeName, meta.Version)
	}

	got, want := schemaOver(t, exported), schemaOver(t, mistral.New("1.2.3")())
	gotResources, wantResources := slices.Sorted(maps.Keys(got.ResourceSchemas)), slices.Sorted(maps.Keys(want.ResourceSchemas))
	if !slices.Equal(gotResources, wantResources) || !slices.Contains(gotResources, "mistral_agent") {
		t.Errorf("resources = %v, want %v", gotResources, wantResources)
	}
	gotDataSources, wantDataSources := slices.Sorted(maps.Keys(got.DataSourceSchemas)), slices.Sorted(maps.Keys(want.DataSourceSchemas))
	if !slices.Equal(gotDataSources, wantDataSources) || !slices.Contains(gotDataSources, "mistral_models") {
		t.Errorf("data sources = %v, want %v", gotDataSources, wantDataSources)
	}
}
