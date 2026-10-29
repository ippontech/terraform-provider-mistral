// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Command specdrift reports the operations of Mistral's OpenAPI specification
// that the provider calls and that changed since their fingerprint was
// committed.
//
// The provider's operations are the routes its fakes serve: every string
// literal "<METHOD> /v<n>/..." of internal/services/*/fake_test.go. Each is
// matched to its operation in the specification by method and path shape
// (parameter names aside, and without the "#id" suffixes the specification
// uses to tell two operations of one path apart). Its fingerprint is a hash
// of its parameters, request body and success responses, with every $ref
// resolved and the prose (descriptions, titles, examples, x- extensions)
// left out, so that only a change of contract changes it.
//
// The specification has no licence: only the fingerprints are committed, in
// tools/specdrift/fingerprints.txt, never the specification.
//
//	curl -sSfo /tmp/openapi.yaml https://docs.mistral.ai/openapi.yaml
//	go run -C tools ./specdrift -spec /tmp/openapi.yaml -root ..          # check
//	go run -C tools ./specdrift -spec /tmp/openapi.yaml -root .. -update  # rewrite
//
// It exits 1 when an operation changed, disappeared or is new to the
// fingerprints file, and 2 on a usage or input error.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

const fingerprintsFile = "tools/specdrift/fingerprints.txt"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("specdrift", flag.ContinueOnError)
	fs.SetOutput(stderr)
	specPath := fs.String("spec", "", "path of the OpenAPI specification (YAML)")
	root := fs.String("root", ".", "root of the provider repository")
	update := fs.Bool("update", false, "rewrite the fingerprints file instead of checking it")
	routesOnly := fs.Bool("routes-only", false, "check only that the fingerprints file lists the routes of the fakes, without a specification")
	if err := fs.Parse(args); err != nil || (*specPath == "" && !*routesOnly) {
		_, _ = fmt.Fprintln(stderr, "usage: specdrift -spec <openapi.yaml> [-root <repo>] [-update] | specdrift -routes-only [-root <repo>]")
		return 2
	}

	routes, err := providerRoutes(*root)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "specdrift:", err)
		return 2
	}
	path := filepath.Join(*root, fingerprintsFile)
	if *routesOnly {
		return checkRoutes(routes, path, stdout, stderr)
	}
	spec, err := loadSpec(*specPath)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "specdrift:", err)
		return 2
	}
	current := fingerprints(spec, routes)

	if *update {
		if err := os.WriteFile(path, []byte(format(current)), 0o600); err != nil {
			_, _ = fmt.Fprintln(stderr, "specdrift:", err)
			return 2
		}
		_, _ = fmt.Fprintf(stdout, "wrote %d fingerprints to %s\n", len(current), fingerprintsFile)
		return 0
	}

	committed, err := readFingerprints(path)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "specdrift:", err)
		return 2
	}
	report := compare(committed, current)
	if report == "" {
		_, _ = fmt.Fprintf(stdout, "The %d operations the provider calls match their committed fingerprints.\n", len(current))
		return 0
	}
	_, _ = fmt.Fprint(stdout, report)
	return 1
}

// checkRoutes fails when the fingerprints file and the fakes list different
// routes: a pull request that serves a new route must fingerprint it.
func checkRoutes(routes []string, path string, stdout, stderr io.Writer) int {
	committed, err := readFingerprints(path)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "specdrift:", err)
		return 2
	}
	current := make(map[string]string, len(routes))
	for _, r := range routes {
		current[r] = committed[r]
		if _, ok := committed[r]; !ok {
			current[r] = "new"
		}
	}
	if report := compare(committed, current); report != "" {
		_, _ = fmt.Fprint(stdout, report+"Run `make spec-drift SPECDRIFT_FLAGS=-update` and commit tools/specdrift/fingerprints.txt.\n")
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "The fingerprints file lists the %d routes of the fakes.\n", len(routes))
	return 0
}

// routeLiteral is a route of a fake, as written in Go: "GET /v1/agents/{agent_id}".
var routeLiteral = regexp.MustCompile(`"((?:GET|POST|PUT|PATCH|DELETE) /v\d+/[^"\s]*)"`)

// providerRoutes returns the routes the fakes serve, sorted and unique.
func providerRoutes(root string) ([]string, error) {
	files, err := filepath.Glob(filepath.Join(root, "internal", "services", "*", "fake_test.go"))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no internal/services/*/fake_test.go under %s", root)
	}
	var routes []string
	for _, f := range files {
		b, err := os.ReadFile(f) //nolint:gosec // G304: a file of the repository, found by the glob above
		if err != nil {
			return nil, err
		}
		for _, m := range routeLiteral.FindAllStringSubmatch(string(b), -1) {
			routes = append(routes, m[1])
		}
	}
	slices.Sort(routes)
	return slices.Compact(routes), nil
}

func loadSpec(path string) (map[string]any, error) {
	b, err := os.ReadFile(path) //nolint:gosec // G304: the path is the -spec flag
	if err != nil {
		return nil, err
	}
	var spec map[string]any
	if err := yaml.Unmarshal(b, &spec); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if _, ok := spec["paths"].(map[string]any); !ok {
		return nil, errors.New("the specification has no paths")
	}
	return spec, nil
}

var pathParam = regexp.MustCompile(`\{[^}]*\}`)

// shape is a path with its parameter names and "#..." suffix removed.
func shape(path string) string {
	path, _, _ = strings.Cut(path, "#")
	return pathParam.ReplaceAllString(path, "{}")
}

// fingerprints returns, for each route, the hash of its operation in spec, or
// "missing" when the specification has none.
func fingerprints(spec map[string]any, routes []string) map[string]string {
	paths, _ := spec["paths"].(map[string]any)
	byShape := map[string]map[string]any{}
	for p, item := range paths {
		ops, _ := item.(map[string]any)
		for method, op := range ops {
			if o, ok := op.(map[string]any); ok {
				byShape[strings.ToUpper(method)+" "+shape(p)] = o
			}
		}
	}
	out := make(map[string]string, len(routes))
	for _, r := range routes {
		method, path, _ := strings.Cut(r, " ")
		op, ok := byShape[method+" "+shape(path)]
		if !ok {
			out[r] = "missing"
			continue
		}
		out[r] = hash(contract(spec, op))
	}
	return out
}

// contract keeps what a client depends on: parameters, request body, success
// responses.
func contract(spec map[string]any, op map[string]any) any {
	responses := map[string]any{}
	if rs, ok := op["responses"].(map[string]any); ok {
		for code, r := range rs {
			if strings.HasPrefix(code, "2") {
				responses[code] = r
			}
		}
	}
	c := map[string]any{
		"parameters":  op["parameters"],
		"requestBody": op["requestBody"],
		"responses":   responses,
		"deprecated":  op["deprecated"],
	}
	return resolve(spec, c, map[string]bool{})
}

// prose are the keys left out of a fingerprint: they document a contract
// without changing it.
var prose = map[string]bool{"description": true, "summary": true, "title": true, "example": true, "examples": true}

// resolve replaces every $ref by its target and drops the prose. A reference
// already being resolved on the current branch stays a reference, so that a
// recursive schema terminates.
func resolve(spec map[string]any, v any, inProgress map[string]bool) any {
	switch x := v.(type) {
	case map[string]any:
		if ref, ok := x["$ref"].(string); ok {
			if inProgress[ref] {
				return map[string]any{"$ref": ref}
			}
			inProgress[ref] = true
			defer delete(inProgress, ref)
			return resolve(spec, lookup(spec, ref), inProgress)
		}
		out := make(map[string]any, len(x))
		for k, e := range x {
			if prose[k] || strings.HasPrefix(k, "x-") {
				continue
			}
			out[k] = resolve(spec, e, inProgress)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = resolve(spec, e, inProgress)
		}
		return out
	}
	return v
}

// lookup follows a local reference such as "#/components/schemas/Agent".
func lookup(spec map[string]any, ref string) any {
	var node any = spec
	for part := range strings.SplitSeq(strings.TrimPrefix(ref, "#/"), "/") {
		m, ok := node.(map[string]any)
		if !ok {
			return map[string]any{"$unresolved": ref}
		}
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		if node, ok = m[part]; !ok {
			return map[string]any{"$unresolved": ref}
		}
	}
	return node
}

// hash is the first 16 hexadecimal digits of the SHA-256 of v in JSON, whose
// encoder sorts map keys.
func hash(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "unencodable"
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])[:16]
}

func format(fps map[string]string) string {
	var b strings.Builder
	b.WriteString("# Fingerprints of the operations the provider calls, from Mistral's OpenAPI\n")
	b.WriteString("# specification. Generated by tools/specdrift with -update: do not edit.\n")
	for _, r := range sortedKeys(fps) {
		fmt.Fprintf(&b, "%s %s\n", r, fps[r])
	}
	return b.String()
}

func readFingerprints(path string) (map[string]string, error) {
	b, err := os.ReadFile(path) //nolint:gosec // G304: a file of the repository
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for line := range strings.SplitSeq(string(b), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		route, fp, ok := strings.CutLast(line, " ")
		if !ok {
			return nil, fmt.Errorf("%s: malformed line %q", path, line)
		}
		out[route] = fp
	}
	return out, nil
}

// compare returns the Markdown report of what differs, or "" when nothing does.
func compare(committed, current map[string]string) string {
	var changed, missing, added, dropped []string
	for _, r := range sortedKeys(current) {
		old, known := committed[r]
		switch {
		case current[r] == "missing" && old != "missing":
			missing = append(missing, r)
		case !known:
			added = append(added, r)
		case old != current[r]:
			changed = append(changed, r)
		}
	}
	for _, r := range sortedKeys(committed) {
		if _, ok := current[r]; !ok {
			dropped = append(dropped, r)
		}
	}
	if len(changed)+len(missing)+len(added)+len(dropped) == 0 {
		return ""
	}
	var b strings.Builder
	section(&b, "Changed in the specification: check the client and the fake against the new contract", changed)
	section(&b, "No longer in the specification: the provider calls an operation Mistral no longer documents", missing)
	section(&b, "Served by a fake but without a committed fingerprint: run specdrift -update", added)
	section(&b, "Fingerprinted but no longer served by any fake: run specdrift -update", dropped)
	return b.String()
}

func section(b *strings.Builder, title string, routes []string) {
	if len(routes) == 0 {
		return
	}
	fmt.Fprintf(b, "### %s\n\n", title)
	for _, r := range routes {
		fmt.Fprintf(b, "- `%s`\n", r)
	}
	b.WriteString("\n")
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
