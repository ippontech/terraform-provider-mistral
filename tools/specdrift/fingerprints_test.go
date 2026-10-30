// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Tests of what a fingerprint covers and of the operation a route matches,
// on small specifications loaded as the command loads them.

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadText loads the specification text through loadSpec.
func loadText(t *testing.T, text string) map[string]any {
	t.Helper()
	path := filepath.Join(t.TempDir(), "openapi.yaml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	spec, err := loadSpec(path)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

// fingerprintOf returns the fingerprint of route in the specification text.
func fingerprintOf(t *testing.T, text, route string) string {
	t.Helper()
	fps, ambiguities := fingerprints(loadText(t, text), []string{route})
	if len(ambiguities) > 0 {
		t.Fatalf("%s is ambiguous: %v", route, ambiguities)
	}
	if fps[route] == "missing" {
		t.Fatalf("%s is missing from the specification", route)
	}
	return fps[route]
}

// edit returns text with old replaced by replacement, failing the test when
// old does not occur exactly once.
func edit(t *testing.T, text, old, replacement string) string {
	t.Helper()
	if n := strings.Count(text, old); n != 1 {
		t.Fatalf("%q occurs %d times in the fixture, want 1", old, n)
	}
	return strings.Replace(text, old, replacement, 1)
}

// specNames has, in the response of one operation, names that are spelt like
// prose or extensions: properties, a header, a link, its parameter and its
// server variable, $defs, definitions and dependencies, a discriminator value,
// and a key of a default value.
const specNames = `
openapi: 3.1.0
paths:
  /v1/libraries/{library_id}:
    get:
      responses:
        "200":
          headers:
            x-request-id: {schema: {type: string}}
          links:
            x-next:
              operationId: getLibrary
              parameters: {description: "$response.body#/description"}
              server: {url: "https://{x-region}.mistral.ai", variables: {x-region: {default: eu}}}
          content: {application/json: {schema: {$ref: "#/components/schemas/Library"}}}
components:
  schemas:
    Library:
      type: object
      title: Library
      $defs: {title: {type: string}}
      definitions: {x-legacy: {type: string}}
      dependencies: {title: [description]}
      properties:
        description: {type: string, description: What the library holds.}
        title: {type: string}
        x-owner: {type: string}
        chunk:
          type: object
          discriminator: {propertyName: type, mapping: {summary: "#/components/schemas/Summary"}}
        sort: {type: object, default: {title: asc}}
    Summary: {type: object, properties: {type: {const: summary}}}
`

// TestNamesSpeltLikeProseCount checks that a property, a header, a
// discriminator value or a key of a default value named like prose or like an
// extension is contract, while the prose of the schemas still does not count.
func TestNamesSpeltLikeProseCount(t *testing.T) {
	t.Parallel()
	const route = "GET /v1/libraries/{library_id}"
	base := fingerprintOf(t, specNames, route)
	for _, tc := range []struct {
		name, old, replacement string
		changes                bool
	}{
		{"property description retyped", "description: {type: string,", "description: {type: integer,", true},
		{"property title removed", "        title: {type: string}\n", "", true},
		{"property x-owner removed", "        x-owner: {type: string}\n", "", true},
		{"header x-request-id renamed", "x-request-id:", "x-trace-id:", true},
		{"discriminator value renamed", "mapping: {summary:", "mapping: {abstract:", true},
		{"key of a default value changed", "default: {title: asc}", "default: {title: desc}", true},
		{"link x-next renamed", "x-next:", "x-following:", true},
		{"link parameter renamed", "parameters: {description:", "parameters: {summary:", true},
		{"server variable renamed", "variables: {x-region:", "variables: {x-zone:", true},
		{"$def title retyped", "$defs: {title: {type: string}}", "$defs: {title: {type: integer}}", true},
		{"definition x-legacy removed", "definitions: {x-legacy: {type: string}}", "definitions: {}", true},
		{"dependency of title removed", "dependencies: {title: [description]}", "dependencies: {}", true},
		{"description of a property reworded", "What the library holds.", "The documents of the library.", false},
		{"title of a schema renamed", "title: Library", "title: DocumentLibrary", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := fingerprintOf(t, edit(t, specNames, tc.old, tc.replacement), route)
			if changed := got != base; changed != tc.changes {
				t.Errorf("fingerprint changed = %v, want %v", changed, tc.changes)
			}
		})
	}
}

// specStream has an operation and its "#stream" twin under one method and
// path shape, as Mistral's specification documents POST /v1/conversations.
const specStream = `
openapi: 3.1.0
paths:
  /v1/conversations:
    post:
      responses: {"200": {content: {application/json: {schema: {type: object}}}}}
  /v1/conversations#stream:
    post:
      responses: {"200": {content: {text/event-stream: {schema: {type: string}}}}}
`

// TestPlainPathWinsOverItsTwin checks that a route matches the operation
// whose path has no "#..." suffix, on every run: the operations are indexed
// from maps, whose iteration order Go randomizes.
func TestPlainPathWinsOverItsTwin(t *testing.T) {
	t.Parallel()
	const route = "POST /v1/conversations"
	plainOnly, _, _ := strings.Cut(specStream, "  /v1/conversations#stream:")
	want := fingerprintOf(t, plainOnly, route)
	spec := loadText(t, specStream)
	for range 64 {
		fps, ambiguities := fingerprints(spec, []string{route})
		if len(ambiguities) > 0 {
			t.Fatalf("%s is ambiguous: %v", route, ambiguities)
		}
		if fps[route] != want {
			t.Fatalf("fingerprint %s, want %s, that of the plain path", fps[route], want)
		}
	}
}

// specAmbiguous has two operations for DELETE /v1/connectors/{id}, neither
// of them without a suffix, besides the one operation of GET
// /v1/agents/{agent_id}: the two routes of repo.
const specAmbiguous = `
openapi: 3.1.0
paths:
  /v1/agents/{agent_id}:
    get:
      responses: {"200": {content: {application/json: {schema: {type: object, properties: {id: {type: string}}}}}}}
  /v1/connectors/{connector_id}#id:
    delete: {responses: {"204": {description: Deleted}}}
  /v1/connectors/{connector_name}#name:
    delete: {responses: {"204": {description: Deleted}}}
`

// TestAmbiguousRouteIsReported checks that a route matching several
// operations, none of them the only one without a suffix, is named with
// their paths and fails the command, while the other routes are still
// fingerprinted by -update and checked; -update keeps the committed
// fingerprint of the ambiguous route.
func TestAmbiguousRouteIsReported(t *testing.T) {
	t.Parallel()
	root, specPath := repo(t)
	writeSpec := func(text string) {
		t.Helper()
		if err := os.WriteFile(specPath, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeSpec(specAmbiguous)
	code, out := runCmd("-spec", specPath, "-root", root, "-update")
	if code != 2 || !strings.Contains(out, "DELETE /v1/connectors/{id} matches several operations of the specification (/v1/connectors/{connector_id}#id, /v1/connectors/{connector_name}#name)") {
		t.Errorf("update: %d %s", code, out)
	}
	written, err := readFingerprints(filepath.Join(root, fingerprintsFile))
	if err != nil {
		t.Fatal(err)
	}
	if want := fingerprintOf(t, specAmbiguous, "GET /v1/agents/{agent_id}"); written["GET /v1/agents/{agent_id}"] != want || written["DELETE /v1/connectors/{id}"] != ambiguous {
		t.Errorf("update wrote %v, want GET at %s and DELETE %s", written, want, ambiguous)
	}

	// Fingerprinted while it matched one operation, the route keeps that
	// fingerprint through an -update that finds it ambiguous, and the other
	// route is rewritten.
	specOne, _, _ := strings.Cut(specAmbiguous, "  /v1/connectors/{connector_name}#name:")
	writeSpec(specOne)
	if code, out := runCmd("-spec", specPath, "-root", root, "-update"); code != 0 {
		t.Fatalf("update, one operation: %d %s", code, out)
	}
	known := fingerprintOf(t, specOne, "DELETE /v1/connectors/{id}")
	retyped := edit(t, specAmbiguous, "properties: {id: {type: string}}", "properties: {id: {type: string}, name: {type: string}}")
	writeSpec(retyped)
	if code, out := runCmd("-spec", specPath, "-root", root, "-update"); code != 2 || !strings.Contains(out, "its fingerprint stays "+known) {
		t.Errorf("update, ambiguous again: %d %s", code, out)
	}
	if written, err = readFingerprints(filepath.Join(root, fingerprintsFile)); err != nil {
		t.Fatal(err)
	}
	if want := fingerprintOf(t, retyped, "GET /v1/agents/{agent_id}"); written["DELETE /v1/connectors/{id}"] != known || written["GET /v1/agents/{agent_id}"] != want {
		t.Errorf("update wrote %v, want DELETE kept at %s and GET at %s", written, known, want)
	}
	writeSpec(specAmbiguous)
	if code, out := runCmd("-spec", specPath, "-root", root, "-update"); code != 2 {
		t.Fatalf("update: %d %s", code, out)
	}
	if written, err = readFingerprints(filepath.Join(root, fingerprintsFile)); err != nil {
		t.Fatal(err)
	}

	// A route fingerprinted before the specification made it ambiguous is
	// reported once, as ambiguous, not also as changed.
	written["DELETE /v1/connectors/{id}"] = "0123456789abcdef"
	if err := os.WriteFile(filepath.Join(root, fingerprintsFile), []byte(format(written)), 0o600); err != nil {
		t.Fatal(err)
	}
	const ambiguity = "- `DELETE /v1/connectors/{id}`: `/v1/connectors/{connector_id}#id`, `/v1/connectors/{connector_name}#name`"
	code, out = runCmd("-spec", specPath, "-root", root)
	if code != 1 || !strings.Contains(out, ambiguity) || strings.Contains(out, "### Changed") {
		t.Errorf("check, the ambiguity alone: %d %s", code, out)
	}

	writeSpec(edit(t, specAmbiguous, "id: {type: string}", "id: {type: integer}"))
	code, out = runCmd("-spec", specPath, "-root", root)
	if code != 1 || !strings.Contains(out, ambiguity) || !strings.Contains(out, "### Changed in the specification: check the client and the fake against the new contract\n\n- `GET /v1/agents/{agent_id}`\n\n") {
		t.Errorf("check, a drift beside the ambiguity: %d %s", code, out)
	}
}

// specUnquoted writes its status code without quotes, which YAML reads as an
// integer.
const specUnquoted = `
openapi: 3.1.0
paths:
  /v1/agents/{agent_id}:
    get:
      responses:
        200: {content: {application/json: {schema: {type: object, properties: {id: {type: string}}}}}}
`

// TestUnquotedStatusCodesCount checks that the responses of an operation
// whose status codes are not quoted count as when they are.
func TestUnquotedStatusCodesCount(t *testing.T) {
	t.Parallel()
	const route = "GET /v1/agents/{agent_id}"
	got := fingerprintOf(t, specUnquoted, route)
	if quoted := fingerprintOf(t, edit(t, specUnquoted, "200:", `"200":`), route); got != quoted {
		t.Errorf("fingerprint %s with 200:, %s with \"200\":", got, quoted)
	}
	if retyped := fingerprintOf(t, edit(t, specUnquoted, "id: {type: string}", "id: {type: integer}"), route); retyped == got {
		t.Error("a change to a response under an unquoted status code does not count")
	}
}

// TestMergeKeysStillMerge checks that a merge key (<<) of the specification
// merges its mapping rather than becoming a key of its own.
func TestMergeKeysStillMerge(t *testing.T) {
	t.Parallel()
	const route = "GET /v1/agents"
	merged := fingerprintOf(t, `
openapi: 3.1.0
x-shared: &object {type: object}
paths:
  /v1/agents:
    get:
      responses: {"200": {content: {application/json: {schema: {<<: *object, properties: {id: {type: string}}}}}}}
`, route)
	written := fingerprintOf(t, `
openapi: 3.1.0
paths:
  /v1/agents:
    get:
      responses: {"200": {content: {application/json: {schema: {type: object, properties: {id: {type: string}}}}}}}
`, route)
	if merged != written {
		t.Errorf("fingerprint %s with a merge key, %s with the mapping written out", merged, written)
	}
}

// specShared declares parameters on its path item, one of them through a
// $ref, besides the operation's own.
const specShared = `
openapi: 3.1.0
paths:
  /v1/libraries/{library_id}/documents:
    parameters:
      - {name: library_id, in: path, required: true, schema: {type: string}}
      - {$ref: "#/components/parameters/Limit"}
    get:
      parameters:
        - {name: page, in: query, schema: {type: integer}}
      responses: {"200": {content: {application/json: {schema: {type: array}}}}}
components:
  parameters:
    Limit: {name: limit, in: query, schema: {type: integer}}
`

// TestPathItemParametersCount checks that the parameters of a path item
// count in the fingerprint of each of its operations, after the operation's
// own, and that an operation's parameter overrides the path item's of the
// same name and location.
func TestPathItemParametersCount(t *testing.T) {
	t.Parallel()
	const route = "GET /v1/libraries/{library_id}/documents"
	base := fingerprintOf(t, specShared, route)

	writtenOut := strings.Replace(specShared, `    parameters:
      - {name: library_id, in: path, required: true, schema: {type: string}}
      - {$ref: "#/components/parameters/Limit"}
`, "", 1)
	writtenOut = edit(t, writtenOut, "        - {name: page, in: query, schema: {type: integer}}\n", `        - {name: page, in: query, schema: {type: integer}}
        - {name: library_id, in: path, required: true, schema: {type: string}}
        - {$ref: "#/components/parameters/Limit"}
`)
	if got := fingerprintOf(t, writtenOut, route); got != base {
		t.Errorf("fingerprint %s with path item parameters, %s with them written in the operation", base, got)
	}

	if got := fingerprintOf(t, edit(t, specShared, "library_id, in: path, required: true, schema: {type: string}", "library_id, in: path, required: true, schema: {type: integer}"), route); got == base {
		t.Error("a change to a parameter of the path item does not count")
	}

	overridden := edit(t, specShared, "        - {name: page, in: query, schema: {type: integer}}\n", `        - {name: page, in: query, schema: {type: integer}}
        - {name: limit, in: query, schema: {type: integer}}
`)
	before := fingerprintOf(t, overridden, route)
	after := fingerprintOf(t, edit(t, overridden, "Limit: {name: limit, in: query, schema: {type: integer}}", "Limit: {name: limit, in: query, schema: {type: string}}"), route)
	if before != after {
		t.Error("a parameter of the path item that the operation overrides counts")
	}
}

// TestUnreadableParametersCount checks that each parameter whose location
// and name cannot be read, behind a $ref that does not resolve or that
// loops, counts in the fingerprint rather than being taken as overridden by
// another such parameter.
func TestUnreadableParametersCount(t *testing.T) {
	t.Parallel()
	const route = "GET /v1/libraries"
	spec := `
openapi: 3.1.0
paths:
  /v1/libraries:
    parameters:
      - {$ref: "#/components/parameters/GoneA"}
      - {$ref: "#/components/parameters/Loop"}
    get:
      parameters:
        - {$ref: "#/components/parameters/GoneB"}
      responses: {"200": {content: {application/json: {schema: {type: array}}}}}
components:
  parameters:
    Loop: {$ref: "#/components/parameters/Loop"}
`
	base := fingerprintOf(t, spec, route)
	if got := fingerprintOf(t, edit(t, spec, "parameters/GoneA", "parameters/GoneC"), route); got == base {
		t.Error("a path item parameter whose $ref does not resolve does not count")
	}
	if got := fingerprintOf(t, edit(t, spec, "      - {$ref: \"#/components/parameters/Loop\"}\n", ""), route); got == base {
		t.Error("a path item parameter whose $ref loops does not count")
	}
}

// TestChainedParameterReferences checks that the location and name of a
// parameter are read at the end of its chain of $ref, so that an operation's
// parameter overrides the path item's.
func TestChainedParameterReferences(t *testing.T) {
	t.Parallel()
	const route = "GET /v1/libraries"
	spec := `
openapi: 3.1.0
paths:
  /v1/libraries:
    parameters:
      - {$ref: "#/components/parameters/Alias"}
    get:
      parameters:
        - {name: limit, in: query, schema: {type: integer}}
      responses: {"200": {content: {application/json: {schema: {type: array}}}}}
components:
  parameters:
    Alias: {$ref: "#/components/parameters/Limit"}
    Limit: {name: limit, in: query, schema: {type: integer}}
`
	before := fingerprintOf(t, spec, route)
	after := fingerprintOf(t, edit(t, spec, "Limit: {name: limit, in: query, schema: {type: integer}}", "Limit: {name: limit, in: query, schema: {type: string}}"), route)
	if before != after {
		t.Error("a path item parameter that the operation overrides, through two $ref, counts")
	}
}
