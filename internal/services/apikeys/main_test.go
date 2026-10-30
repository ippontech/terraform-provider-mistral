// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package apikeys_test

import (
	"os"
	"testing"

	"github.com/ippontech/terraform-provider-mistral/internal/services/apikeys"
)

// TestMain sets the clock of the provider to the fake's today, so that the
// expiration dates of the tests stay ahead or behind it whatever the day of
// the run. The acceptance test ignores changes of expiration, so the clock
// never decides anything there.
func TestMain(m *testing.M) {
	apikeys.SetClock(today)
	os.Exit(m.Run())
}
