// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"errors"
	"fmt"
)

// The API pages its lists in three styles (cursor, page number, offset), and
// a fourth by time window that the provider does not use. Each walk calls
// visit for every item, in order, until visit returns false or the list
// ends, and guards against a server that does not advance: maxPages pages at
// most, and an error on a cursor that comes back.

// maxPages bounds a walk, so a list that never ends fails instead of
// looping.
const maxPages = 1000

// errTooManyPages is returned when a walk reaches maxPages.
var errTooManyPages = fmt.Errorf("mistral: more than %d pages: the list does not end", maxPages)

// walkCursor walks a cursor-paged list: fetch returns a page and the cursor
// of the next one, empty on the last page. The first call gets "".
func walkCursor[T any](ctx context.Context, fetch func(ctx context.Context, cursor string) ([]T, string, error), visit func(T) bool) error {
	seen := map[string]bool{}
	cursor := ""
	for range maxPages {
		items, next, err := fetch(ctx, cursor)
		if err != nil {
			return err
		}
		for _, it := range items {
			if !visit(it) {
				return nil
			}
		}
		if next == "" {
			return nil
		}
		if seen[next] || next == cursor {
			return fmt.Errorf("mistral: the API returned the cursor %q twice: the list does not advance", next)
		}
		seen[next] = true
		cursor = next
	}
	return errTooManyPages
}

// walkPages walks a list paged by number, from first (0 or 1, depending on
// the endpoint) with size items per page. fetch returns a page and the total
// number of items, or -1 when the endpoint gives none. The walk ends on a
// page shorter than size, or once total items were seen.
func walkPages[T any](ctx context.Context, first, size int, fetch func(ctx context.Context, page int) ([]T, int, error), visit func(T) bool) error {
	if size <= 0 {
		return errors.New("mistral: page size must be positive")
	}
	seen := 0
	for page := first; page < first+maxPages; page++ {
		items, total, err := fetch(ctx, page)
		if err != nil {
			return err
		}
		for _, it := range items {
			if !visit(it) {
				return nil
			}
		}
		seen += len(items)
		if len(items) < size || (total >= 0 && seen >= total) {
			return nil
		}
	}
	return errTooManyPages
}

// pageTotal returns the total of a page answer, decoded into a *int, or -1,
// the "no total" of walkPages, when the answer has none or a null one.
// Decoded into a plain int, a missing total would be 0 and end the walk
// after its first page.
//
// The specification marks total as required and not null in the workspaces
// and user groups lists (only the users list may answer null), so reading
// theirs through pageTotal is defensive: an answer that leaves it out still
// gives every item, not the first page only.
func pageTotal(total *int) int {
	if total == nil {
		return -1
	}
	return *total
}

// walkOffset walks a list paged by offset and limit. The walk ends on a page
// shorter than limit.
func walkOffset[T any](ctx context.Context, limit int, fetch func(ctx context.Context, offset int) ([]T, error), visit func(T) bool) error {
	if limit <= 0 {
		return errors.New("mistral: limit must be positive")
	}
	offset := 0
	for range maxPages {
		items, err := fetch(ctx, offset)
		if err != nil {
			return err
		}
		for _, it := range items {
			if !visit(it) {
				return nil
			}
		}
		if len(items) < limit {
			return nil
		}
		offset += len(items)
	}
	return errTooManyPages
}

// collect returns every item a walk visits.
func collect[T any](walk func(visit func(T) bool) error) ([]T, error) {
	var out []T
	err := walk(func(it T) bool {
		out = append(out, it)
		return true
	})
	return out, err
}

// find returns the first item a walk visits that match accepts, or nil.
func find[T any](walk func(visit func(T) bool) error, match func(T) bool) (*T, error) {
	var found *T
	err := walk(func(it T) bool {
		if match(it) {
			found = &it
			return false
		}
		return true
	})
	return found, err
}
