// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package sdktest

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
)

var uuidCounter atomic.Int64

// NewUUID returns a well-formed, unique UUID: most Mistral objects use UUIDs
// as their IDs.
func NewUUID() string {
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", uuidCounter.Add(1))
}

// Collection is an in-memory store of JSON objects keyed by one of their
// fields, in creation order: the minimal state a fake needs for the
// Terraform lifecycle. It is safe for concurrent use; every object it
// returns is a deep copy.
type Collection struct {
	key string

	mu    sync.Mutex
	order []string
	items map[string]map[string]any
}

// NewCollection returns an empty Collection keyed by the field key ("id",
// "uuid", "name").
func NewCollection(key string) *Collection {
	return &Collection{key: key, items: map[string]map[string]any{}}
}

// Create stores a copy of obj, with a NewUUID under the key field when obj
// has none, and returns the stored object.
func (c *Collection) Create(obj map[string]any) map[string]any {
	o := DeepCopy(obj)
	if _, ok := o[c.key].(string); !ok {
		o[c.key] = NewUUID()
	}
	c.Put(o)
	return DeepCopy(o)
}

// Put stores a copy of obj under its key field, replacing any object with
// the same key and keeping its position.
func (c *Collection) Put(obj map[string]any) {
	o := DeepCopy(obj)
	k, _ := o[c.key].(string)
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.items[k]; !exists {
		c.order = append(c.order, k)
	}
	c.items[k] = o
}

// Get returns the object stored under key.
func (c *Collection) Get(key string) (map[string]any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	o, ok := c.items[key]
	if !ok {
		return nil, false
	}
	return DeepCopy(o), true
}

// Patch sets the top-level fields of body on the object stored under key,
// null included, and returns the result.
func (c *Collection) Patch(key string, body map[string]any) (map[string]any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	o, ok := c.items[key]
	if !ok {
		return nil, false
	}
	maps.Copy(o, DeepCopy(body))
	return DeepCopy(o), true
}

// Delete removes the object stored under key and reports whether it existed.
func (c *Collection) Delete(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.items[key]; !ok {
		return false
	}
	delete(c.items, key)
	for i, k := range c.order {
		if k == key {
			c.order = append(c.order[:i], c.order[i+1:]...)
			break
		}
	}
	return true
}

// List returns the objects keep accepts (all of them when keep is nil), in
// creation order.
func (c *Collection) List(keep func(map[string]any) bool) []map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := []map[string]any{}
	for _, k := range c.order {
		if o := c.items[k]; keep == nil || keep(o) {
			out = append(out, DeepCopy(o))
		}
	}
	return out
}

// Len returns the number of objects.
func (c *Collection) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

// DeepCopy copies a JSON object through encoding/json, numbers kept as
// json.Number, so a test that changes a returned object never changes the
// store.
func DeepCopy(o map[string]any) map[string]any {
	if o == nil {
		return nil
	}
	b, err := json.Marshal(o)
	if err != nil {
		panic(fmt.Sprintf("sdktest: DeepCopy of a non-JSON value: %v", err))
	}
	var out map[string]any
	if err := unmarshalNumbers(b, &out); err != nil {
		panic(fmt.Sprintf("sdktest: DeepCopy: %v", err))
	}
	return out
}

func unmarshalNumbers(b []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	return dec.Decode(v)
}

// QueryInt returns the integer query parameter name of r, or def when it is
// absent or not an integer.
func QueryInt(r *http.Request, name string, def int) int {
	v, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil {
		return def
	}
	return v
}

// PageOf returns page number page of items, size per page, for a list whose
// first page is first (0 or 1, depending on the endpoint).
func PageOf[T any](items []T, page, size, first int) []T {
	if size <= 0 || page < first {
		return []T{}
	}
	return window(items, (page-first)*size, size)
}

// OffsetOf returns limit items of items from offset.
func OffsetOf[T any](items []T, offset, limit int) []T {
	if limit <= 0 || offset < 0 {
		return []T{}
	}
	return window(items, offset, limit)
}

// CursorOf returns limit items of items after cursor, and the cursor of the
// next page, "" on the last one. Cursors are opaque to the client, as the
// API's are.
func CursorOf[T any](items []T, cursor string, limit int) ([]T, string) {
	start := 0
	if cursor != "" {
		b, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return []T{}, ""
		}
		if start, err = strconv.Atoi(string(b)); err != nil {
			return []T{}, ""
		}
	}
	page := window(items, start, limit)
	next := ""
	if start+len(page) < len(items) {
		next = base64.RawURLEncoding.EncodeToString([]byte(strconv.Itoa(start + len(page))))
	}
	return page, next
}

func window[T any](items []T, start, n int) []T {
	if start >= len(items) || n <= 0 {
		return []T{}
	}
	end := min(start+n, len(items))
	return items[start:end]
}
