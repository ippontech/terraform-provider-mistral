// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package libraries

import (
	"context"
	"time"

	"github.com/ippontech/terraform-provider-mistral/internal/client"
)

// pollFirst and pollMax are the delays between two reads of the processing
// status: pollFirst first, doubled after each read up to pollMax, so a short
// document is ready in about a second and a long one costs a read every 10
// seconds. The guide polls every second
// (https://docs.mistral.ai/studio/search/libraries). Tests shorten both
// (export_test.go).
var (
	pollFirst = time.Second
	pollMax   = 10 * time.Second
)

// outcome is where a process_status leaves the wait of a create.
type outcome int

const (
	// pending: the library is still processing the document.
	pending outcome = iota
	// processed: the processing ended and the document can be searched.
	processed
	// failed: the processing ended without a searchable document.
	failed
)

// outcomeOf classifies a process_status (ProcessStatus in the
// specification, which describes none of its values). Our reading: done is
// the end of a processing, noop that there was nothing to process, and
// self_managed that the owner processes the document; error and
// missing_content (no text found) leave a document the library cannot
// search. todo, in_progress, waiting_for_capacity and any value Mistral adds
// later keep the wait going, until timeouts.create.
func outcomeOf(status string) outcome {
	switch status {
	case "done", "noop", "self_managed":
		return processed
	case "error", "missing_content":
		return failed
	}
	return pending
}

// waitForProcessing reads the processing status of a document until it is
// no longer pending, and returns the last status. status is the one of the
// upload answer, so a document processed at once needs no read. It returns
// the error of a read, or the error of ctx when its deadline passes first.
// This is a wait on a status, not a retry: each read keeps the retries of
// the client.
func waitForProcessing(ctx context.Context, cl *client.Client, libraryID, documentID, status string) (string, error) {
	delay := pollFirst
	for outcomeOf(status) == pending {
		if err := sleep(ctx, delay); err != nil {
			return status, err
		}
		got, err := cl.GetDocumentStatus(ctx, libraryID, documentID)
		if err != nil {
			return status, err
		}
		status = got.ProcessStatus
		delay = min(2*delay, pollMax)
	}
	return status, nil
}

// sleep waits for d, or returns the error of ctx when it ends first.
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
