// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

//go:build generate

package tools

import (
	_ "github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs"
)

// Format the Terraform examples, then generate the documentation from the
// provider schema, templates/ and examples/.
//go:generate terraform fmt -recursive ../examples/
//go:generate go run github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs generate --provider-dir .. --provider-name mistral
