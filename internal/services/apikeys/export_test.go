// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package apikeys

import "time"

// SetClock sets the date expiresBy compares an expiration_date with, for the
// tests of the package: TestMain calls it before any test runs.
func SetClock(at time.Time) {
	now = func() time.Time { return at }
}
