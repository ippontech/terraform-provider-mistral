// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// typeName returns the type name a resource or data source registers.
func typeName(t *testing.T, v any) string {
	t.Helper()
	switch x := v.(type) {
	case resource.Resource:
		resp := &resource.MetadataResponse{}
		x.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: providerTypeName}, resp)
		return resp.TypeName
	case datasource.DataSource:
		resp := &datasource.MetadataResponse{}
		x.Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: providerTypeName}, resp)
		return resp.TypeName
	}
	t.Fatalf("%T is neither a resource nor a data source", v)
	return ""
}

// checkRegistrations fails on a type with a missing declaration, a duplicate
// name, or a name without the provider prefix.
func checkRegistrations[T any](t *testing.T, kind string, regs []registration[T]) {
	t.Helper()
	seen := map[string]bool{}
	for _, r := range regs {
		name := typeName(t, r.New())
		if !strings.HasPrefix(name, providerTypeName+"_") {
			t.Errorf("%s %q does not start with %q", kind, name, providerTypeName+"_")
		}
		if seen[name] {
			t.Errorf("%s %q is registered twice", kind, name)
		}
		seen[name] = true
		if r.Audience == 0 {
			t.Errorf("%s %q declares no Audience", kind, name)
		}
		if r.Stability == 0 {
			t.Errorf("%s %q declares no Stability", kind, name)
		}
		if r.Live == 0 {
			t.Errorf("%s %q declares no Live coverage", kind, name)
		}
	}
}

func TestRegistrationsDeclareEverything(t *testing.T) {
	checkRegistrations(t, "resource", resourceRegistrations())
	checkRegistrations(t, "data source", dataSourceRegistrations())
}

func TestProviderServesTheRegisteredTypes(t *testing.T) {
	p := New("test")()
	ctx := context.Background()
	if got, want := len(p.Resources(ctx)), len(resourceRegistrations()); got != want {
		t.Errorf("Resources() returns %d factories, the registry has %d", got, want)
	}
	if got, want := len(p.DataSources(ctx)), len(dataSourceRegistrations()); got != want {
		t.Errorf("DataSources() returns %d factories, the registry has %d", got, want)
	}
}

func TestDeclarationStrings(t *testing.T) {
	for s, want := range map[stability]string{generallyAvailable: "generally available", beta: "beta", 0: "stability(0)"} {
		if got := s.String(); got != want {
			t.Errorf("stability %d = %q, want %q", int(s), got, want)
		}
	}
	for l, want := range map[liveCoverage]string{acceptanceTested: "acceptance tested", fakeOnly: "fake API only", 0: "liveCoverage(0)"} {
		if got := l.String(); got != want {
			t.Errorf("liveCoverage %d = %q, want %q", int(l), got, want)
		}
	}
}
