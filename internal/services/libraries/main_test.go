// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package libraries_test

import (
	"os"
	"testing"
	"time"

	"github.com/ippontech/terraform-provider-mistral/internal/services/libraries"
)

// TestMain polls the processing status every millisecond, so a create waits
// for the fake without slowing the tests down.
func TestMain(m *testing.M) {
	libraries.SetPollDelays(time.Millisecond, time.Millisecond)
	os.Exit(m.Run())
}
