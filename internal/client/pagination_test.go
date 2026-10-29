// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

func ints(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

func TestWalkCursor(t *testing.T) {
	t.Parallel()
	items := ints(7)
	//nolint:unparam // the signature the walk takes
	fetch := func(_ context.Context, cursor string) ([]int, string, error) {
		page, next := sdktest.CursorOf(items, cursor, 3)
		return page, next, nil
	}
	got, err := collect(func(v func(int) bool) error { return walkCursor(context.Background(), fetch, v) })
	if err != nil || len(got) != 7 || got[6] != 6 {
		t.Errorf("collect = %v, %v", got, err)
	}

	found, err := find(func(v func(int) bool) error { return walkCursor(context.Background(), fetch, v) },
		func(i int) bool { return i == 4 })
	if err != nil || found == nil || *found != 4 {
		t.Errorf("find = %v, %v", found, err)
	}
}

func TestWalkCursorRefusesACursorThatComesBack(t *testing.T) {
	t.Parallel()
	calls := 0
	fetch := func(_ context.Context, _ string) ([]int, string, error) {
		calls++
		return []int{1}, "same", nil
	}
	err := walkCursor(context.Background(), fetch, func(int) bool { return true })
	if err == nil || !strings.Contains(err.Error(), "does not advance") {
		t.Errorf("err = %v", err)
	}
	if calls != 2 {
		t.Errorf("%d calls, want 2", calls)
	}
}

func TestWalkPages(t *testing.T) {
	t.Parallel()
	items := ints(5)
	for _, first := range []int{0, 1} {
		var pages []int
		//nolint:unparam // the signature the walk takes
		fetch := func(_ context.Context, page int) ([]int, int, error) {
			pages = append(pages, page)
			return sdktest.PageOf(items, page, 2, first), -1, nil
		}
		got, err := collect(func(v func(int) bool) error { return walkPages(context.Background(), first, 2, fetch, v) })
		if err != nil || len(got) != 5 {
			t.Errorf("first %d: collect = %v, %v", first, got, err)
		}
		if len(pages) != 3 || pages[0] != first {
			t.Errorf("first %d: pages %v", first, pages)
		}
	}
}

func TestWalkPagesStopsAtTheTotal(t *testing.T) {
	t.Parallel()
	items := ints(4)
	calls := 0
	//nolint:unparam // the signature the walk takes
	fetch := func(_ context.Context, page int) ([]int, int, error) {
		calls++
		return sdktest.PageOf(items, page, 2, 1), 4, nil
	}
	got, err := collect(func(v func(int) bool) error { return walkPages(context.Background(), 1, 2, fetch, v) })
	if err != nil || len(got) != 4 || calls != 2 {
		t.Errorf("collect = %v, %v after %d calls; want 4 items in 2 calls (no empty third page)", got, err, calls)
	}
}

func TestWalkOffset(t *testing.T) {
	t.Parallel()
	items := ints(6)
	var offsets []int
	//nolint:unparam // the signature the walk takes
	fetch := func(_ context.Context, offset int) ([]int, error) {
		offsets = append(offsets, offset)
		return sdktest.OffsetOf(items, offset, 3), nil
	}
	got, err := collect(func(v func(int) bool) error { return walkOffset(context.Background(), 3, fetch, v) })
	if err != nil || len(got) != 6 {
		t.Errorf("collect = %v, %v", got, err)
	}
	if len(offsets) != 3 || offsets[2] != 6 {
		t.Errorf("offsets = %v", offsets)
	}
}

func TestWalksGuardAgainstEndlessLists(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	visit := func(int) bool { return true }

	var cursors atomic.Int64
	errCursor := walkCursor(ctx, func(context.Context, string) ([]int, string, error) {
		return []int{1}, string(rune('a'+cursors.Add(1)%26)) + strings.Repeat("x", int(cursors.Load())), nil
	}, visit)
	errPages := walkPages(ctx, 1, 1, func(context.Context, int) ([]int, int, error) { return []int{1}, -1, nil }, visit)
	errOffset := walkOffset(ctx, 1, func(context.Context, int) ([]int, error) { return []int{1}, nil }, visit)

	for name, err := range map[string]error{"cursor": errCursor, "pages": errPages, "offset": errOffset} {
		if !errors.Is(err, errTooManyPages) {
			t.Errorf("%s: err = %v, want errTooManyPages", name, err)
		}
	}
}

func TestWalksReturnFetchErrorsAndRefuseBadSizes(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	boom := errors.New("boom")
	visit := func(int) bool { return true }

	if err := walkCursor(ctx, func(context.Context, string) ([]int, string, error) { return nil, "", boom }, visit); !errors.Is(err, boom) {
		t.Errorf("cursor: %v", err)
	}
	if err := walkPages(ctx, 0, 2, func(context.Context, int) ([]int, int, error) { return nil, 0, boom }, visit); !errors.Is(err, boom) {
		t.Errorf("pages: %v", err)
	}
	if err := walkOffset(ctx, 2, func(context.Context, int) ([]int, error) { return nil, boom }, visit); !errors.Is(err, boom) {
		t.Errorf("offset: %v", err)
	}
	if err := walkPages(ctx, 0, 0, func(context.Context, int) ([]int, int, error) { return nil, 0, nil }, visit); err == nil {
		t.Error("pages: a page size of 0 must be refused")
	}
	if err := walkOffset(ctx, 0, func(context.Context, int) ([]int, error) { return nil, nil }, visit); err == nil {
		t.Error("offset: a limit of 0 must be refused")
	}
	stop := func(int) bool { return false }
	if err := walkPages(ctx, 0, 1, func(context.Context, int) ([]int, int, error) { return []int{1}, -1, nil }, stop); err != nil {
		t.Errorf("pages early stop: %v", err)
	}
	if err := walkOffset(ctx, 1, func(context.Context, int) ([]int, error) { return []int{1}, nil }, stop); err != nil {
		t.Errorf("offset early stop: %v", err)
	}
	if found, err := find(func(v func(int) bool) error {
		return walkOffset(ctx, 1, func(context.Context, int) ([]int, error) { return nil, nil }, v)
	},
		func(int) bool { return true }); found != nil || err != nil {
		t.Errorf("find on an empty list = %v, %v", found, err)
	}
}

type closeCounter struct {
	io.Reader
	n *atomic.Int64
}

func (c closeCounter) Close() error {
	c.n.Add(1)
	return nil
}

func TestMultipartUploadIsRebuiltForEveryAttempt(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.FailNext("POST /v1/upload", http.StatusTooManyRequests, 1)
	srv.Handle("POST /v1/upload", func(w http.ResponseWriter, r *http.Request) {
		parts, err := sdktest.ReadParts(r)
		if err != nil {
			t.Errorf("parts: %v", err)
		}
		if len(parts) != 2 || parts[0].Field != "purpose" || string(parts[0].Content) != "ocr" ||
			parts[1].Field != "file" || parts[1].Filename != `a "b".txt` || string(parts[1].Content) != "hello" {
			t.Errorf("parts = %+v", parts)
		}
		sdktest.WriteJSON(w, http.StatusOK, map[string]any{"id": "f1"})
	})
	var opened atomic.Int64
	form := &multipartForm{
		fields: []formField{{"purpose", "ocr"}},
		files: []formFile{{field: "file", filename: `a "b".txt`, open: func() (io.ReadCloser, error) {
			opened.Add(1)
			return io.NopCloser(strings.NewReader("hello")), nil
		}}},
	}
	var out struct {
		ID string `json:"id"`
	}
	err := newTestClient(srv).do(context.Background(), call{method: http.MethodPost, path: "/v1/upload", form: form, audience: workspaceKey}, &out)
	if err != nil || out.ID != "f1" {
		t.Fatalf("upload = %+v, %v", out, err)
	}
	srv.AssertCalled("POST /v1/upload", 2)
	if got := opened.Load(); got < 1 || got > 2 {
		t.Errorf("file opened %d times", got)
	}
}

func TestMultipartOpenErrorFailsTheUpload(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond("POST /v1/upload", http.StatusOK, map[string]any{})
	var closed atomic.Int64
	form := &multipartForm{files: []formFile{
		{field: "a", filename: "a", open: func() (io.ReadCloser, error) {
			return closeCounter{Reader: strings.NewReader("a"), n: &closed}, nil
		}},
		{field: "b", filename: "b", open: func() (io.ReadCloser, error) { return nil, errors.New("no such file") }},
	}}
	err := newTestClient(srv).do(context.Background(), call{method: http.MethodPost, path: "/v1/upload", form: form, audience: workspaceKey}, nil)
	if err == nil || !strings.Contains(err.Error(), "open b: no such file") {
		t.Errorf("err = %v", err)
	}
	srv.AssertCalled("POST /v1/upload", 0)
	if closed.Load() != 1 {
		t.Errorf("the file opened before the failure was closed %d times, want 1", closed.Load())
	}
}
