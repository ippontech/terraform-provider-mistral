// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"errors"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"
)

// RetryPolicy says when and how long to wait before replaying a request.
//
// No Mistral endpoint documents an idempotency key, so a write that may have
// landed is never replayed (CLAUDE.md, "Retries"):
//   - a read, a delete, or a write the API documents as idempotent (an
//     upsert) is retried on 408, 429, 500, 502, 503, 504 and on any
//     connection error;
//   - any other write, a create first, is retried only on 429, which the API
//     answers before doing anything, or on a connection error raised before
//     the request was written. After a 5xx or a dropped connection the object
//     may exist: the apply fails, and the next one converges.
//
// The error glossary (https://docs.mistral.ai/resources/error-glossary) asks
// for exponential backoff on 429 and 503, and a retry after a delay on 500
// and 502.
type RetryPolicy struct {
	// MaxRetries is the number of replays after the first attempt.
	MaxRetries int
	// BaseDelay is the first backoff, doubled at each attempt.
	BaseDelay time.Duration
	// MaxDelay caps the backoff.
	MaxDelay time.Duration
	// MaxRetryAfter caps a Retry-After header. A longer one is not waited
	// for: the error goes back to the user.
	MaxRetryAfter time.Duration
}

// DefaultRetryPolicy is the policy of the provider.
var DefaultRetryPolicy = RetryPolicy{
	MaxRetries:    3,
	BaseDelay:     500 * time.Millisecond,
	MaxDelay:      8 * time.Second,
	MaxRetryAfter: 60 * time.Second,
}

type retryDecision struct {
	retry bool
	delay time.Duration
}

// decide tells whether to replay c after an attempt that returned resp or
// err. sent reports whether the request reached the connection.
func (p RetryPolicy) decide(c call, resp *http.Response, sent bool, err error, attempt int) retryDecision {
	if attempt >= p.MaxRetries {
		return retryDecision{}
	}
	if err != nil {
		if _, ok := errors.AsType[*buildError](err); ok {
			return retryDecision{}
		}
		if c.isIdempotent() || !sent {
			return retryDecision{retry: true, delay: p.backoff(attempt)}
		}
		return retryDecision{}
	}
	if !retryableStatus(resp.StatusCode, c.isIdempotent()) {
		return retryDecision{}
	}
	if after, ok := retryAfter(resp.Header.Get("Retry-After"), time.Now()); ok {
		if after > p.MaxRetryAfter {
			return retryDecision{}
		}
		return retryDecision{retry: true, delay: after}
	}
	return retryDecision{retry: true, delay: p.backoff(attempt)}
}

// isIdempotent reports whether replaying c cannot change the result: the
// methods RFC 9110 defines as idempotent, and the writes marked idempotent.
func (c call) isIdempotent() bool {
	switch c.method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPut, http.MethodDelete:
		return true
	}
	return c.idempotent
}

func retryableStatus(status int, idempotent bool) bool {
	if status == http.StatusTooManyRequests {
		return true
	}
	if !idempotent {
		return false
	}
	switch status {
	case http.StatusRequestTimeout, http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}

// backoff is BaseDelay doubled attempt times, capped at MaxDelay, with up to
// 25 % of jitter so that parallel resources do not retry in step.
func (p RetryPolicy) backoff(attempt int) time.Duration {
	d := p.BaseDelay
	for range attempt {
		d *= 2
		if d >= p.MaxDelay {
			d = p.MaxDelay
			break
		}
	}
	if d <= 0 {
		return 0
	}
	return d + time.Duration(rand.Int64N(int64(d)/4+1)) //nolint:gosec // G404: jitter, not a secret
}

// retryAfter parses a Retry-After header: seconds, or an HTTP date.
func retryAfter(v string, now time.Time) (time.Duration, bool) {
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0, false
		}
		return time.Duration(secs) * time.Second, true
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(t.Sub(now), 0), true
	}
	return 0, false
}
