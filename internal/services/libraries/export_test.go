// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package libraries

import "time"

// SetPollDelays sets the delays between two reads of the processing status,
// for the tests of the package: TestMain calls it before any test runs.
func SetPollDelays(first, limit time.Duration) {
	pollFirst, pollMax = first, limit
}
