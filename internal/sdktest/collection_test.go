// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package sdktest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCollection(t *testing.T) {
	t.Parallel()
	c := NewCollection("id")

	a := c.Create(map[string]any{"name": "a"})
	b := c.Create(map[string]any{"id": "fixed", "name": "b"})
	if id, _ := a["id"].(string); !strings.HasPrefix(id, "00000000-0000-4000-8000-") {
		t.Errorf("generated id = %v", a["id"])
	}
	if b["id"] != "fixed" || c.Len() != 2 {
		t.Errorf("Create kept %v, Len = %d", b["id"], c.Len())
	}

	a["name"] = "changed by the test"
	if got, _ := c.Get(a["id"].(string)); got["name"] != "a" {
		t.Error("a returned object shares memory with the store")
	}
	if _, ok := c.Get("missing"); ok {
		t.Error("Get of a missing key")
	}

	patched, ok := c.Patch("fixed", map[string]any{"name": "b2", "description": nil})
	if !ok || patched["name"] != "b2" || patched["description"] != nil {
		t.Errorf("Patch = %v, %v", patched, ok)
	}
	if _, ok := c.Patch("missing", map[string]any{}); ok {
		t.Error("Patch of a missing key")
	}

	c.Put(map[string]any{"id": "fixed", "name": "b3"})
	list := c.List(nil)
	if len(list) != 2 || list[1]["name"] != "b3" {
		t.Errorf("Put must replace in place: %v", list)
	}
	only := c.List(func(o map[string]any) bool { return o["name"] == "b3" })
	if len(only) != 1 {
		t.Errorf("List(keep) = %v", only)
	}

	if !c.Delete("fixed") || c.Delete("fixed") || c.Len() != 1 {
		t.Error("Delete")
	}
	if DeepCopy(nil) != nil {
		t.Error("DeepCopy(nil)")
	}
	n := DeepCopy(map[string]any{"n": json.Number("1.50")})
	if n["n"] != json.Number("1.50") {
		t.Errorf("DeepCopy lost the number text: %v", n["n"])
	}
}

func TestDeepCopyPanicsOnNonJSON(t *testing.T) {
	t.Parallel()
	defer func() {
		if recover() == nil {
			t.Error("no panic")
		}
	}()
	DeepCopy(map[string]any{"c": make(chan int)})
}

func TestPageHelpers(t *testing.T) {
	t.Parallel()
	items := []int{0, 1, 2, 3, 4}

	if got := PageOf(items, 1, 2, 1); len(got) != 2 || got[0] != 0 {
		t.Errorf("PageOf first page = %v", got)
	}
	if got := PageOf(items, 2, 2, 0); len(got) != 1 || got[0] != 4 {
		t.Errorf("PageOf last page = %v", got)
	}
	if got := PageOf(items, 0, 2, 1); len(got) != 0 {
		t.Errorf("PageOf before the first page = %v", got)
	}
	if got := OffsetOf(items, 3, 10); len(got) != 2 {
		t.Errorf("OffsetOf = %v", got)
	}
	if got := OffsetOf(items, -1, 2); len(got) != 0 {
		t.Errorf("OffsetOf negative = %v", got)
	}

	page, next := CursorOf(items, "", 2)
	var all []int
	for {
		all = append(all, page...)
		if next == "" {
			break
		}
		page, next = CursorOf(items, next, 2)
	}
	if len(all) != 5 {
		t.Errorf("CursorOf walk = %v", all)
	}
	if got, next := CursorOf(items, "!!", 2); len(got) != 0 || next != "" {
		t.Error("a bad cursor must give an empty last page")
	}
	if got, _ := CursorOf(items, "YQ", 2); len(got) != 0 {
		t.Error("a cursor that is not a number must give an empty page")
	}

	r := httptest.NewRequest(http.MethodGet, "/x?page=3&size=bad", nil)
	if QueryInt(r, "page", 1) != 3 || QueryInt(r, "size", 7) != 7 || QueryInt(r, "absent", 9) != 9 {
		t.Error("QueryInt")
	}
}
