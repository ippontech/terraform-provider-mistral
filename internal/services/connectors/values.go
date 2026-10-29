// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package connectors

import (
	"time"

	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
)

// nonEmpty reads an optional string of the API: null and "" are both
// null, since the schema refuses an empty value and the two must not fight.
func nonEmpty(s *string) types.String {
	if s == nil || *s == "" {
		return types.StringNull()
	}
	return types.StringValue(*s)
}

// timestamp writes a date of the API in RFC 3339, null when the API gave
// none.
func timestamp(t time.Time) types.String {
	if t.IsZero() {
		return types.StringNull()
	}
	return types.StringValue(t.UTC().Format(time.RFC3339))
}

// protocolOrDefault is the protocol of a connector, mcp when the API leaves
// it out: the server-side default of CreateConnectorRequest.
func protocolOrDefault(p string) string {
	if p == "" {
		return client.ConnectorProtocolMCP
	}
	return p
}
