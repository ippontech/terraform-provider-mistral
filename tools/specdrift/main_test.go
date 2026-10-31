// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const specV1 = `
openapi: 3.1.0
paths:
  /v1/agents/{agent_id}:
    get:
      description: Retrieve an agent.
      parameters:
        - {name: agent_id, in: path, required: true, schema: {type: string}}
      responses:
        "200": {content: {application/json: {schema: {$ref: "#/components/schemas/Agent"}}}}
        "422": {description: Validation Error}
  /v1/connectors/{connector_id}#id:
    delete:
      responses:
        "204": {description: Deleted}
components:
  schemas:
    Agent:
      type: object
      title: Agent
      properties:
        id: {type: string}
        handoffs: {type: array, items: {$ref: "#/components/schemas/Agent"}}
`

// repo writes a repository with one fake serving routes, and specV1.
func repo(t *testing.T) (root, specPath string) {
	t.Helper()
	root = t.TempDir()
	dir := filepath.Join(root, "internal", "services", "agents")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "tools", "specdrift"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fake_test.go"), []byte(routes), 0o600); err != nil {
		t.Fatal(err)
	}
	specPath = filepath.Join(root, "openapi.yaml")
	if err := os.WriteFile(specPath, []byte(specV1), 0o600); err != nil {
		t.Fatal(err)
	}
	return root, specPath
}

func runCmd(args ...string) (int, string) {
	var out, errOut bytes.Buffer
	code := run(args, &out, &errOut)
	return code, out.String() + errOut.String()
}

const routes = `const (
	routeGet    = "GET /v1/agents/{agent_id}"
	routeDelete = "DELETE /v1/connectors/{id}"
)`

func TestUpdateThenCheckPasses(t *testing.T) {
	t.Parallel()
	root, spec := repo(t)
	if code, out := runCmd("-spec", spec, "-root", root, "-update"); code != 0 || !strings.Contains(out, "wrote 2 fingerprints") {
		t.Fatalf("update: %d %s", code, out)
	}
	if code, out := runCmd("-spec", spec, "-root", root); code != 0 {
		t.Fatalf("check: %d %s", code, out)
	}
}

func TestProseChangesDoNotCount(t *testing.T) {
	t.Parallel()
	root, spec := repo(t)
	runCmd("-spec", spec, "-root", root, "-update")
	edited := strings.ReplaceAll(specV1, "Retrieve an agent.", "Retrieve one agent entity.")
	edited = strings.ReplaceAll(edited, "title: Agent", "title: AgentEntity")
	if err := os.WriteFile(spec, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out := runCmd("-spec", spec, "-root", root); code != 0 {
		t.Errorf("a description change must not count: %d %s", code, out)
	}
}

func TestContractChangesAreReported(t *testing.T) {
	t.Parallel()
	root, spec := repo(t)
	runCmd("-spec", spec, "-root", root, "-update")
	edited := strings.ReplaceAll(specV1, "id: {type: string}", "id: {type: integer}")
	edited = strings.ReplaceAll(edited, "/v1/connectors/{connector_id}#id:", "/v1/connector/{connector_id}:")
	if err := os.WriteFile(spec, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out := runCmd("-spec", spec, "-root", root)
	if code != 1 {
		t.Fatalf("code = %d, want 1: %s", code, out)
	}
	for _, want := range []string{"### Changed", "`GET /v1/agents/{agent_id}`", "### No longer in the specification", "`DELETE /v1/connectors/{id}`"} {
		if !strings.Contains(out, want) {
			t.Errorf("report misses %q:\n%s", want, out)
		}
	}
}

func TestNewAndDroppedRoutesAreReported(t *testing.T) {
	t.Parallel()
	root, spec := repo(t)
	runCmd("-spec", spec, "-root", root, "-update")
	fake := filepath.Join(root, "internal", "services", "agents", "fake_test.go")
	if err := os.WriteFile(fake, []byte(`"GET /v1/agents/{id}" "DELETE /v1/agents/{agent_id}"`), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out := runCmd("-spec", spec, "-root", root)
	if code != 1 || !strings.Contains(out, "without a committed fingerprint") || !strings.Contains(out, "no longer served by any fake") {
		t.Errorf("%d %s", code, out)
	}
}

func TestUsageAndInputErrors(t *testing.T) {
	t.Parallel()
	if code, _ := runCmd(); code != 2 {
		t.Errorf("no -spec: %d", code)
	}
	root, spec := repo(t)
	if code, out := runCmd("-spec", spec, "-root", root); code != 2 || !strings.Contains(out, "fingerprints.txt") {
		t.Errorf("no fingerprints file: %d %s", code, out)
	}
	if code, _ := runCmd("-spec", filepath.Join(root, "missing.yaml"), "-root", root); code != 2 {
		t.Errorf("no spec: %d", code)
	}
	if err := os.WriteFile(spec, []byte("openapi: 3.1.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out := runCmd("-spec", spec, "-root", root); code != 2 || !strings.Contains(out, "no paths") {
		t.Errorf("no paths: %d %s", code, out)
	}
	if code, _ := runCmd("-spec", spec, "-root", t.TempDir()); code != 2 {
		t.Errorf("no fakes: %d", code)
	}
}

func TestLookupOfAMissingReference(t *testing.T) {
	t.Parallel()
	got := resolve(map[string]any{}, map[string]any{"$ref": "#/components/schemas/Gone"}, map[string]bool{})
	if m, ok := got.(map[string]any); !ok || m["$unresolved"] != "#/components/schemas/Gone" {
		t.Errorf("resolve = %v", got)
	}
}

func TestRoutesOnly(t *testing.T) {
	t.Parallel()
	root, spec := repo(t)
	if code, _ := runCmd("-routes-only", "-root", root); code != 2 {
		t.Errorf("no fingerprints file: %d", code)
	}
	runCmd("-spec", spec, "-root", root, "-update")
	if code, out := runCmd("-routes-only", "-root", root); code != 0 {
		t.Errorf("in sync: %d %s", code, out)
	}
	fake := filepath.Join(root, "internal", "services", "agents", "fake_test.go")
	if err := os.WriteFile(fake, []byte(routes+`
const routeList = "GET /v1/agents"`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out := runCmd("-routes-only", "-root", root); code != 1 || !strings.Contains(out, "`GET /v1/agents`") {
		t.Errorf("new route: %d %s", code, out)
	}
}

func TestMalformedFingerprintsFile(t *testing.T) {
	t.Parallel()
	root, spec := repo(t)
	if err := os.WriteFile(filepath.Join(root, fingerprintsFile), []byte("no-space-here\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out := runCmd("-spec", spec, "-root", root); code != 2 || !strings.Contains(out, "malformed line") {
		t.Errorf("%d %s", code, out)
	}
}

// TestSpecificationErrorsArePrintedQuoted checks that an error quoting the
// specification reaches stderr quoted, on one line, with no "##[": a parse
// error carries the decoded scalar as it is, escape sequence, line break and
// workflow commands included.
func TestSpecificationErrorsArePrintedQuoted(t *testing.T) {
	t.Parallel()
	root, spec := repo(t)
	if err := os.WriteFile(spec, []byte("openapi: 3.1.0\npaths:\n  /v1/agents/{agent_id}: !!int \"\\e[2J\\n::error::x ##[error]y\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := run([]string{"-spec", spec, "-root", root}, &out, &errOut)
	got := errOut.String()
	if code != 2 || strings.ContainsAny(strings.TrimSuffix(got, "\n"), "\x1b\n") || strings.Contains(got, "##[") ||
		!strings.Contains(got, `\x1b[2J\n::error::x \x23\x23[error]y`) {
		t.Errorf("run: %d %q", code, got)
	}
}
