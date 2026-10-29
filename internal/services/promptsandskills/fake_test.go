// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package promptsandskills_test

import (
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/ippontech/terraform-provider-mistral/internal/sdktest"
)

// The routes the provider calls, as the fake registers them.
const (
	routePromptCreate  = "POST /v2/prompts"
	routePromptGet     = "GET /v2/prompts/{prompt_id}"
	routePromptUpdate  = "PATCH /v2/prompts/{prompt_id}"
	routePromptDelete  = "DELETE /v2/prompts/{prompt_id}"
	routePromptVersion = "POST /v2/prompts/{prompt_id}/versions"
	routePromptNotes   = "PATCH /v2/prompts/{prompt_id}/versions/{version}"

	routeSkillCreate  = "POST /v2/skills"
	routeSkillGet     = "GET /v2/skills/{skill_id}"
	routeSkillUpdate  = "PATCH /v2/skills/{skill_id}"
	routeSkillDelete  = "DELETE /v2/skills/{skill_id}"
	routeSkillVersion = "POST /v2/skills/{skill_id}/versions"
	routeSkillNotes   = "PATCH /v2/skills/{skill_id}/versions/{version}"
)

// fake serves the prompts (https://docs.mistral.ai/api/endpoint/beta/prompts)
// and the skills (https://docs.mistral.ai/api/endpoint/beta/skills) of the
// registry over an in-memory store. Besides the transport, it holds what the
// provider depends on:
//   - a create stores the first version, numbered 1 as in the examples of the
//     specification, and each new version takes the next number;
//   - a read without a query returns the latest version
//     (https://docs.mistral.ai/studio/conversations/chat-completion/prompt-registry);
//   - a new version identical to the latest one is deduplicated: the answer
//     names the latest version and nothing is created; dedupAnyVersion
//     extends this to every version, the other reading of "deduplicated";
//   - an update takes the fields of its request body only, a null leaving a
//     field unchanged, and refuses workspaceRelation without the workspace
//     scope, as the specification says;
//   - answers leave out empty fields, as a protobuf gateway does, and never
//     return workspaceRelation, which the Prompt and Skill schemas lack.
type fake struct {
	*sdktest.Server
	t       *testing.T
	prompts *kind
	skills  *kind

	// mu serializes the writes, each a read-modify-write of one object, and
	// guards clock.
	mu    sync.Mutex
	clock time.Time
	// dedupAnyVersion makes a new version deduplicate against every version,
	// not only the latest one. Set it before the first request.
	dedupAnyVersion bool
}

// kind is one of the two object types of the registry.
type kind struct {
	f *fake
	// param is the name of the ID in the routes: prompt_id or skill_id.
	param string
	// create and update are the fields CreateXRequest and UpdateXRequest
	// accept; the specification declares additionalProperties false.
	create, update []string
	objects        *sdktest.Collection
}

// newFake starts a fake with no prompt and no skill.
func newFake(t *testing.T) *fake {
	t.Helper()
	f := &fake{Server: sdktest.NewServer(t), t: t, clock: time.Date(2026, 10, 29, 10, 0, 0, 0, time.UTC)}
	f.prompts = &kind{
		f: f, param: "prompt_id", objects: sdktest.NewCollection("id"),
		create: []string{"name", "definition", "title", "description", "notes", "sharingScope", "aliases", "workspaceRelation"},
		update: []string{"title", "description", "sharingScope", "workspaceRelation"},
	}
	f.skills = &kind{
		f: f, param: "skill_id", objects: sdktest.NewCollection("id"),
		create: []string{"name", "definition", "notes", "sharingScope", "aliases", "workspaceRelation"},
		update: []string{"sharingScope", "workspaceRelation"},
	}
	for k, routes := range map[*kind][6]string{
		f.prompts: {routePromptCreate, routePromptGet, routePromptUpdate, routePromptDelete, routePromptVersion, routePromptNotes},
		f.skills:  {routeSkillCreate, routeSkillGet, routeSkillUpdate, routeSkillDelete, routeSkillVersion, routeSkillNotes},
	} {
		f.Handle(routes[0], k.createObject)
		f.Handle(routes[1], k.get)
		f.Handle(routes[2], k.updateObject)
		f.Handle(routes[3], k.delete)
		f.Handle(routes[4], k.createVersion)
		f.Handle(routes[5], k.setVersionMetadata)
	}
	return f
}

// tick returns the time of the next write, one minute after the previous
// one. The caller holds mu.
func (f *fake) tick() string {
	f.clock = f.clock.Add(time.Minute)
	return f.clock.Format(time.RFC3339)
}

// at is the time of the n-th write of the fake, from 1.
func at(n int) string {
	return time.Date(2026, 10, 29, 10, n, 0, 0, time.UTC).Format(time.RFC3339)
}

// createObject stores the object with its first version.
func (k *kind) createObject(w http.ResponseWriter, r *http.Request) {
	body, ok := k.body(w, r, k.create, "name", "definition")
	if !ok || !sharingAccepted(w, body["sharingScope"], body["workspaceRelation"]) {
		return
	}
	k.f.mu.Lock()
	defer k.f.mu.Unlock()
	now := k.f.tick()
	obj := map[string]any{"createdAt": now, "updatedAt": now}
	for _, field := range k.create {
		if body[field] != nil && field != "definition" && field != "notes" && field != "aliases" {
			obj[field] = body[field]
		}
	}
	obj["versions"] = []any{newVersion(1, body, now)}
	sdktest.WriteJSON(w, http.StatusOK, view(k.objects.Create(obj), 1))
}

// get returns the latest version of the object.
func (k *kind) get(w http.ResponseWriter, r *http.Request) {
	obj, ok := k.find(w, r)
	if !ok {
		return
	}
	sdktest.WriteJSON(w, http.StatusOK, view(obj, len(versions(obj))))
}

// updateObject sets the fields of the body that are not null.
func (k *kind) updateObject(w http.ResponseWriter, r *http.Request) {
	body, ok := k.body(w, r, k.update)
	if !ok {
		return
	}
	k.f.mu.Lock()
	defer k.f.mu.Unlock()
	obj, ok := k.find(w, r)
	if !ok {
		return
	}
	scope := obj["sharingScope"]
	if body["sharingScope"] != nil {
		scope = body["sharingScope"]
	}
	if !sharingAccepted(w, scope, body["workspaceRelation"]) {
		return
	}
	for field, value := range body {
		if value != nil {
			obj[field] = value
		}
	}
	obj["updatedAt"] = k.f.tick()
	k.objects.Put(obj)
	sdktest.WriteJSON(w, http.StatusOK, view(obj, len(versions(obj))))
}

// delete deletes the object and its versions.
func (k *kind) delete(w http.ResponseWriter, r *http.Request) {
	if !k.objects.Delete(r.PathValue(k.param)) {
		sdktest.WriteError(w, http.StatusNotFound, "Not found")
		return
	}
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{})
}

// createVersion adds a version, unless the definition matches one the
// deduplication compares with.
func (k *kind) createVersion(w http.ResponseWriter, r *http.Request) {
	body, ok := k.body(w, r, []string{"definition", "notes", "aliases"}, "definition")
	if !ok {
		return
	}
	k.f.mu.Lock()
	defer k.f.mu.Unlock()
	obj, ok := k.find(w, r)
	if !ok {
		return
	}
	all := versions(obj)
	compared := all[len(all)-1:]
	if k.f.dedupAnyVersion {
		compared = all
	}
	for _, v := range compared {
		if sameDefinition(v.(map[string]any)["definition"], body["definition"]) {
			sdktest.WriteJSON(w, http.StatusOK, map[string]any{"version": v.(map[string]any)["version"], "deduplicated": true})
			return
		}
	}
	now := k.f.tick()
	obj["versions"] = append(all, newVersion(len(all)+1, body, now))
	obj["updatedAt"] = now
	k.objects.Put(obj)
	// deduplicated is false, which a protobuf gateway leaves out.
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{"version": len(all) + 1})
}

// setVersionMetadata sets the notes, and the aliases, of one version.
func (k *kind) setVersionMetadata(w http.ResponseWriter, r *http.Request) {
	body, ok := k.body(w, r, []string{"notes", "aliases"})
	if !ok {
		return
	}
	k.f.mu.Lock()
	defer k.f.mu.Unlock()
	obj, ok := k.find(w, r)
	if !ok {
		return
	}
	n, err := strconv.Atoi(r.PathValue("version"))
	all := versions(obj)
	if err != nil || n < 1 || n > len(all) {
		sdktest.WriteError(w, http.StatusNotFound, "Version not found")
		return
	}
	v := all[n-1].(map[string]any)
	if body["notes"] != nil {
		v["notes"] = body["notes"]
	}
	if aliases, ok := body["aliases"].(map[string]any); ok {
		v["aliases"] = aliases["values"]
	}
	obj["updatedAt"] = k.f.tick()
	k.objects.Put(obj)
	sdktest.WriteJSON(w, http.StatusOK, view(obj, n))
}

// body decodes the request body, and fails the test on a field the request
// schema does not declare or on a required field that is missing: both are
// bugs of the client.
func (k *kind) body(w http.ResponseWriter, r *http.Request, allowed []string, required ...string) (map[string]any, bool) {
	body, ok := k.f.DecodeBody(w, r)
	if !ok {
		return nil, false
	}
	for field := range body {
		if !slices.Contains(allowed, field) {
			k.f.t.Errorf("fake: %s %s sent %q, which the request schema does not declare", r.Method, r.URL.Path, field)
			sdktest.WriteError(w, http.StatusBadRequest, "unknown field "+field)
			return nil, false
		}
	}
	for _, field := range required {
		if body[field] == nil {
			k.f.t.Errorf("fake: %s %s did not send the required %q", r.Method, r.URL.Path, field)
			sdktest.WriteError(w, http.StatusBadRequest, "missing field "+field)
			return nil, false
		}
	}
	return body, true
}

// find returns the object of the request's ID, or answers 404.
func (k *kind) find(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	obj, ok := k.objects.Get(r.PathValue(k.param))
	if !ok {
		sdktest.WriteError(w, http.StatusNotFound, "Not found")
	}
	return obj, ok
}

// sharingAccepted answers 400 to a relation without the workspace scope:
// "Rejected unless sharing_scope is `workspace`".
func sharingAccepted(w http.ResponseWriter, scope, relation any) bool {
	if relation != nil && scope != "workspace" {
		sdktest.WriteError(w, http.StatusBadRequest, "workspaceRelation requires sharingScope workspace")
		return false
	}
	return true
}

// newVersion is version n of an object, from a create or a new version body.
func newVersion(n int, body map[string]any, now string) map[string]any {
	return map[string]any{"version": n, "definition": body["definition"], "notes": body["notes"], "aliases": body["aliases"], "createdAt": now}
}

// versions returns the versions of a stored object, oldest first.
func versions(obj map[string]any) []any {
	return obj["versions"].([]any)
}

// view is the answer for version n of obj: the object's fields, the
// version's definition, notes and aliases, and the latest version number,
// without the empty fields a protobuf gateway leaves out.
func view(obj map[string]any, n int) map[string]any {
	v := versions(obj)[n-1].(map[string]any)
	out := map[string]any{
		"id": obj["id"], "name": obj["name"], "version": n, "latestVersion": len(versions(obj)),
		"definition": compactDefinition(v["definition"]), "notes": v["notes"], "aliases": v["aliases"],
		"sharingScope": obj["sharingScope"], "title": obj["title"], "description": obj["description"],
		"createdAt": obj["createdAt"], "updatedAt": obj["updatedAt"],
	}
	for key, value := range out {
		if value == nil || value == "" || reflect.DeepEqual(value, []any{}) {
			delete(out, key)
		}
	}
	return out
}

// compactDefinition leaves out the empty fields of a definition, and the
// false isExecutable of each asset. A set branch of the asset union stays,
// even empty, as protobuf keeps a set oneof field.
func compactDefinition(d any) map[string]any {
	out := map[string]any{}
	for key, value := range d.(map[string]any) {
		switch v := value.(type) {
		case string:
			if v != "" {
				out[key] = v
			}
		case []any:
			if len(v) > 0 {
				out[key] = v
			}
		case map[string]any:
			if len(v) > 0 {
				out[key] = compactAssets(v)
			}
		}
	}
	return out
}

// compactAssets leaves out a false isExecutable.
func compactAssets(assets map[string]any) map[string]any {
	out := map[string]any{}
	for name, a := range assets {
		asset := sdktest.DeepCopy(a.(map[string]any))
		if asset["isExecutable"] == false {
			delete(asset, "isExecutable")
		}
		out[name] = asset
	}
	return out
}

// sameDefinition compares two definitions as their answers would show them.
func sameDefinition(a, b any) bool {
	return reflect.DeepEqual(compactDefinition(a), compactDefinition(b))
}

// seed stores an object created outside Terraform, with one version of
// definition, and returns its ID.
func (k *kind) seed(fields, definition map[string]any) string {
	k.f.mu.Lock()
	defer k.f.mu.Unlock()
	now := k.f.tick()
	obj := sdktest.DeepCopy(fields)
	obj["createdAt"], obj["updatedAt"] = now, now
	obj["versions"] = []any{newVersion(1, map[string]any{"definition": definition}, now)}
	return k.objects.Create(obj)["id"].(string)
}

// addVersion adds a version outside Terraform, as Studio does.
func (k *kind) addVersion(id string, definition map[string]any, notes string) {
	k.f.mu.Lock()
	defer k.f.mu.Unlock()
	obj, ok := k.objects.Get(id)
	if !ok {
		k.f.t.Fatalf("fake: no object %s", id)
	}
	now := k.f.tick()
	all := versions(obj)
	obj["versions"] = append(all, newVersion(len(all)+1, map[string]any{"definition": definition, "notes": notes}, now))
	obj["updatedAt"] = now
	k.objects.Put(obj)
}

// latest returns the latest version of a stored object, for assertions.
func (k *kind) latest(id string) map[string]any {
	obj, ok := k.objects.Get(id)
	if !ok {
		k.f.t.Fatalf("fake: no object %s", id)
	}
	all := versions(obj)
	return all[len(all)-1].(map[string]any)
}
