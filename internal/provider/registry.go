// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"

	"github.com/ippontech/terraform-provider-mistral/internal/providerdata"
	"github.com/ippontech/terraform-provider-mistral/internal/services/models"
)

// registration is a resource or data source factory with what its
// documentation must tell users: the credential its endpoints accept, the
// stability of those endpoints in Mistral's API, and whether it was tested
// against the live API.
//
// The three declarations sit next to the factory on purpose: a branch that
// adds a type adds its factory here anyway, and declares on the same line
// what the provider index and the type's page say about it. The registry
// tests fail on a type that misses one.
type registration[T any] struct {
	New       func() T
	Audience  providerdata.Audience
	Stability stability
	Live      liveCoverage
}

// stability is how Mistral's API reference labels a type's endpoints.
type stability int

const (
	// generallyAvailable endpoints carry no beta tag in the specification.
	generallyAvailable stability = iota + 1
	// beta endpoints carry a "beta." tag and sit under "Public Preview" in the
	// API reference: their fields can change without a deprecation period.
	beta
)

// liveCoverage says whether a type passed an acceptance test against the live
// API (TF_ACC), or only the unit tests against the fake API.
type liveCoverage int

const (
	// acceptanceTested types have an acceptance test that passed against a
	// Mistral organization.
	acceptanceTested liveCoverage = iota + 1
	// fakeOnly types are tested against the fake API only: their page says
	// so, until an acceptance test passes.
	fakeOnly
)

func (s stability) String() string {
	switch s {
	case generallyAvailable:
		return "generally available"
	case beta:
		return "beta"
	}
	return fmt.Sprintf("stability(%d)", int(s))
}

func (l liveCoverage) String() string {
	switch l {
	case acceptanceTested:
		return "acceptance tested"
	case fakeOnly:
		return "fake API only"
	}
	return fmt.Sprintf("liveCoverage(%d)", int(l))
}

func resourceRegistrations() []registration[resource.Resource] {
	return []registration[resource.Resource]{}
}

func dataSourceRegistrations() []registration[datasource.DataSource] {
	return []registration[datasource.DataSource]{
		{New: models.NewModelDataSource, Audience: providerdata.Workspace, Stability: generallyAvailable, Live: fakeOnly},
		{New: models.NewModelsDataSource, Audience: providerdata.Workspace, Stability: generallyAvailable, Live: fakeOnly},
	}
}

// factories returns the New function of each registration.
func factories[T any](regs []registration[T]) []func() T {
	out := make([]func() T, 0, len(regs))
	for _, r := range regs {
		out = append(out, r.New)
	}
	return out
}
