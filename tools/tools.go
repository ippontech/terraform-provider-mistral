// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

//go:build generate

package tools

import (
	// gremlins is the mutation tester of make mutation, pinned here so that
	// every run uses the version of this go.mod.
	_ "github.com/go-gremlins/gremlins/cmd/gremlins"
	_ "github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs"
)

// Format the Terraform examples, then generate the documentation from the
// provider schema, templates/ and examples/.
//go:generate terraform fmt -recursive ../examples/
//go:generate go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs generate --provider-dir .. --provider-name mistral
