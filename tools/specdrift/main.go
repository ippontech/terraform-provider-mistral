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
// of its parameters (those of its path item included), request body and
// success responses, with every $ref resolved and the prose (descriptions,
// titles, examples, x- extensions) left out, so that only a change of
// contract changes it. A property, header or discriminator value that is
// named like prose ("description", "title") is contract, and counts.
//
// The specification has no licence: only the fingerprints are committed, in
// tools/specdrift/fingerprints.txt, never the specification.
//
//	curl -sSfo /tmp/openapi.yaml https://docs.mistral.ai/openapi.yaml
//	go run -C tools ./specdrift -spec /tmp/openapi.yaml -root ..          # check
//	go run -C tools ./specdrift -spec /tmp/openapi.yaml -root .. -update  # rewrite
//
// It exits 1 when an operation changed, disappeared, is new to the
// fingerprints file or matches several operations of the specification, and
// 2 on a usage or input error. With -update, it rewrites every fingerprint it
// can, keeps the committed one of a route that matches several operations,
// and then exits 2.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

const fingerprintsFile = "tools/specdrift/fingerprints.txt"

// main runs the command and exits with its status.
func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the command without the process around it: it parses args, writes
// the report on stdout and the errors on stderr, and returns the exit status.
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
	current, ambiguities := fingerprints(spec, routes)
	if *update {
		return rewrite(path, current, ambiguities, stdout, stderr)
	}

	committed, err := readFingerprints(path)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "specdrift:", err)
		return 2
	}
	report := compare(committed, current) + ambiguityReport(ambiguities)
	if report == "" {
		_, _ = fmt.Fprintf(stdout, "The %d operations the provider calls match their committed fingerprints.\n", len(current))
		return 0
	}
	_, _ = fmt.Fprint(stdout, report)
	return 1
}

// rewrite writes the fingerprints file. A route that matches several
// operations keeps its committed fingerprint (keepCommitted) and is named on
// stderr, and rewrite then returns 2: the other routes are still rewritten.
func rewrite(path string, current map[string]string, ambiguities map[string][]string, stdout, stderr io.Writer) int {
	written, err := keepCommitted(path, current, ambiguities)
	if err == nil {
		err = os.WriteFile(path, []byte(format(written)), 0o600)
	}
	if err != nil {
		_, _ = fmt.Fprintln(stderr, "specdrift:", err)
		return 2
	}
	_, _ = fmt.Fprintf(stdout, "wrote %d fingerprints to %s\n", len(written), fingerprintsFile)
	for _, r := range slices.Sorted(maps.Keys(ambiguities)) {
		_, _ = fmt.Fprintf(stderr, "specdrift: %s matches several operations of the specification (%s): its fingerprint stays %s\n", r, strings.Join(ambiguities[r], ", "), written[r])
	}
	if len(ambiguities) > 0 {
		return 2
	}
	return 0
}

// keepCommitted returns current with the fingerprint committed in the file at
// path for each ambiguous route, and ambiguous for one that has none. Once
// pick learns which operation the route calls, the check then compares that
// operation with the last contract verified, instead of taking whatever it
// has become as the new baseline. The file is read only when a route is
// ambiguous, and a missing file commits nothing.
func keepCommitted(path string, current map[string]string, ambiguities map[string][]string) (map[string]string, error) {
	if len(ambiguities) == 0 {
		return current, nil
	}
	committed, err := readFingerprints(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	written := maps.Clone(current)
	for r := range ambiguities {
		if fp, ok := committed[r]; ok {
			written[r] = fp
		}
	}
	return written, nil
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

// loadSpec reads the specification at path, every mapping key a string.
func loadSpec(path string) (map[string]any, error) {
	b, err := os.ReadFile(path) //nolint:gosec // G304: the path is the -spec flag
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	stringKeys(&doc)
	var spec map[string]any
	if err := doc.Decode(&spec); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if _, ok := spec["paths"].(map[string]any); !ok {
		return nil, errors.New("the specification has no paths")
	}
	return spec, nil
}

// stringKeys tags every scalar mapping key of n as a string, so that it
// decodes as written. yaml.v3 decodes a mapping with a key of another type,
// such as an unquoted status code (200:), as a map[interface{}]interface{},
// which every type assertion on map[string]any would skip without a word:
// the responses of the operation would drop out of its fingerprint. Merge
// keys (<<) keep their tag, which tells yaml.v3 to merge.
func stringKeys(n *yaml.Node) {
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			if key := n.Content[i]; key.Kind == yaml.ScalarNode && key.Tag != "!!merge" {
				key.Tag = "!!str"
			}
		}
	}
	for _, c := range n.Content {
		stringKeys(c)
	}
}

var pathParam = regexp.MustCompile(`\{[^}]*\}`)

// shape is a path with its parameter names and "#..." suffix removed.
func shape(path string) string {
	path, _, _ = strings.Cut(path, "#")
	return pathParam.ReplaceAllString(path, "{}")
}

// operation is an operation of the specification.
type operation struct {
	path string // as the specification writes it, "#..." suffix included
	op   map[string]any
	// shared are the parameters of the path item, which apply to each of its
	// operations.
	shared any
}

// ambiguous is the fingerprint of a route that matches several operations of
// the specification, none of them preferred (see pick).
const ambiguous = "ambiguous"

// fingerprints returns, for each route, the hash of its operation in spec,
// "missing" when the specification has none, or ambiguous when pick cannot
// tell which of several it calls. ambiguities gives the paths of those
// operations for each ambiguous route.
func fingerprints(spec map[string]any, routes []string) (fps map[string]string, ambiguities map[string][]string) {
	paths, _ := spec["paths"].(map[string]any)
	byShape := operations(paths)
	fps = make(map[string]string, len(routes))
	ambiguities = map[string][]string{}
	for _, r := range routes {
		method, path, _ := strings.Cut(r, " ")
		ops, ok := byShape[method+" "+shape(path)]
		if !ok {
			fps[r] = "missing"
			continue
		}
		o, ok := pick(ops)
		if !ok {
			fps[r] = ambiguous
			for _, o := range ops {
				ambiguities[r] = append(ambiguities[r], o.path)
			}
			continue
		}
		fps[r] = hash(contract(spec, o))
	}
	return fps, ambiguities
}

// operations indexes the operations of the specification by method and path
// shape, each list in path order.
func operations(paths map[string]any) map[string][]operation {
	byShape := map[string][]operation{}
	for _, p := range slices.Sorted(maps.Keys(paths)) {
		item, _ := paths[p].(map[string]any)
		for method, op := range item {
			if o, ok := op.(map[string]any); ok {
				key := strings.ToUpper(method) + " " + shape(p)
				byShape[key] = append(byShape[key], operation{path: p, op: o, shared: item["parameters"]})
			}
		}
	}
	return byShape
}

// pick returns the operation a route calls among those of its method and
// path shape: the only one, or else the only one whose path has no "#..."
// suffix. The specification gives such a suffix to a variant of an operation
// it documents apart, such as the "#stream" twin of POST /v1/conversations;
// the provider calls the plain one. Without a single plain path, pick cannot
// tell, and returns false rather than pick one at random.
func pick(ops []operation) (operation, bool) {
	if len(ops) == 1 {
		return ops[0], true
	}
	plain := slices.DeleteFunc(slices.Clone(ops), func(o operation) bool { return strings.Contains(o.path, "#") })
	if len(plain) == 1 {
		return plain[0], true
	}
	return operation{}, false
}

// contract keeps what a client depends on: parameters, request body, success
// responses.
func contract(spec map[string]any, o operation) any {
	responses := map[string]any{}
	if rs, ok := o.op["responses"].(map[string]any); ok {
		for code, r := range rs {
			if strings.HasPrefix(code, "2") {
				responses[code] = r
			}
		}
	}
	c := map[string]any{
		"parameters":  parameters(spec, o.op["parameters"], o.shared),
		"requestBody": o.op["requestBody"],
		"responses":   responses,
		"deprecated":  o.op["deprecated"],
	}
	return resolve(spec, c, map[string]bool{})
}

// parameters returns the parameters of an operation: its own, then those of
// its path item that it does not override. An operation's parameter
// overrides the path item's of the same name and location (OpenAPI 3.1, Path
// Item Object). Without parameters on the path item, the operation's list is
// returned as it is, so that its fingerprint stays what it was before path
// items counted.
func parameters(spec map[string]any, own, shared any) any {
	sharedList, _ := shared.([]any)
	if len(sharedList) == 0 {
		return own
	}
	ownList, _ := own.([]any)
	merged := slices.Clone(ownList)
	for _, p := range sharedList {
		overridden := slices.ContainsFunc(ownList, func(o any) bool { return parameterKey(spec, o) == parameterKey(spec, p) })
		if !overridden {
			merged = append(merged, p)
		}
	}
	return merged
}

// parameterKey identifies a parameter by its location and name, following
// its chain of $ref. A parameter whose location and name cannot be read (a
// $ref that does not resolve or loops, an entry that is not an object) is
// identified by the entry itself, as fmt prints it with its map keys sorted:
// it overrides, and is overridden by, nothing but an identical entry, so that
// it never drops out of the fingerprint.
func parameterKey(spec map[string]any, p any) string {
	target := p
	for followed := map[string]bool{}; ; {
		ref, ok := mapOf(target)["$ref"].(string)
		if !ok || followed[ref] {
			break
		}
		followed[ref] = true
		target = lookup(spec, ref)
	}
	in, inOK := mapOf(target)["in"].(string)
	name, nameOK := mapOf(target)["name"].(string)
	if inOK && nameOK {
		return "in " + in + " name " + name
	}
	return fmt.Sprintf("entry %#v", p)
}

// mapOf returns v as an object of the specification, nil when it is not one.
func mapOf(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

// prose are the keys left out of a fingerprint: they document a contract
// without changing it.
var prose = map[string]bool{"description": true, "summary": true, "title": true, "example": true, "examples": true}

// names are the keys whose value maps names that the API chooses, not
// keywords of OpenAPI or JSON Schema, to what they name. Each of these names
// is contract, even one spelt like prose (a property "description") or an
// extension ("x-request-id"), and what it names is resolved as any other
// node. They are the maps of names of JSON Schema 2020-12 (properties,
// patternProperties, dependentSchemas, dependentRequired, $defs), of its
// draft 7 predecessors (definitions, dependencies), and of the objects of
// OpenAPI 3.1 that the contract of an operation holds: its responses, the
// content of a Parameter, Request Body or Response, the headers and links of
// a Response, the encoding of a Media Type, the mapping of a Discriminator,
// the parameters of a Link (an operation's parameters are a list, resolved
// as such) and the variables of the Server of a Link.
var names = map[string]bool{
	"properties": true, "patternProperties": true, "dependentSchemas": true, "dependentRequired": true, "$defs": true,
	"definitions": true, "dependencies": true,
	"responses": true, "content": true, "headers": true, "links": true, "encoding": true,
	"mapping": true, "parameters": true, "variables": true,
}

// literals are the keys whose value is data of the API, kept as written:
// a default object with a "title" key is not prose, and a "$ref" in it is not
// a reference.
var literals = map[string]bool{"default": true, "const": true, "enum": true}

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
		return resolveObject(spec, x, inProgress)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = resolve(spec, e, inProgress)
		}
		return out
	}
	return v
}

// resolveObject resolves the fields of an object of the specification (an
// operation, a parameter, a schema...), whose keys are keywords.
func resolveObject(spec, x map[string]any, inProgress map[string]bool) map[string]any {
	out := make(map[string]any, len(x))
	for k, e := range x {
		switch {
		case prose[k] || strings.HasPrefix(k, "x-"):
		case literals[k]:
			out[k] = e
		case names[k]:
			out[k] = resolveNames(spec, e, inProgress)
		default:
			out[k] = resolve(spec, e, inProgress)
		}
	}
	return out
}

// resolveNames resolves the value of a key of names: every name stays, and
// what it names is resolved.
func resolveNames(spec map[string]any, v any, inProgress map[string]bool) any {
	named, ok := v.(map[string]any)
	if !ok {
		return resolve(spec, v, inProgress)
	}
	out := make(map[string]any, len(named))
	for name, e := range named {
		out[name] = resolve(spec, e, inProgress)
	}
	return out
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

// format returns the content of the fingerprints file: a header, then one
// "<route> <fingerprint>" line per route, in route order.
func format(fps map[string]string) string {
	var b strings.Builder
	b.WriteString("# Fingerprints of the operations the provider calls, from Mistral's OpenAPI\n")
	b.WriteString("# specification. Generated by tools/specdrift with -update: do not edit.\n")
	for _, r := range slices.Sorted(maps.Keys(fps)) {
		fmt.Fprintf(&b, "%s %s\n", r, fps[r])
	}
	return b.String()
}

// readFingerprints reads the fingerprints file that format writes into a map
// of route to fingerprint, skipping comments and blank lines.
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

// compare returns the Markdown report of what differs, or "" when nothing
// does. An ambiguous route is left to ambiguityReport, which names the
// operations it matches.
func compare(committed, current map[string]string) string {
	var changed, missing, added, dropped []string
	for _, r := range slices.Sorted(maps.Keys(current)) {
		old, known := committed[r]
		switch {
		case current[r] == ambiguous:
		case current[r] == "missing" && old != "missing":
			missing = append(missing, r)
		case !known:
			added = append(added, r)
		case old != current[r]:
			changed = append(changed, r)
		}
	}
	for _, r := range slices.Sorted(maps.Keys(committed)) {
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

// ambiguityReport returns the Markdown section of the routes that match
// several operations of the specification, with their paths, or "" when there
// is none. Such a route is reported on every run, whatever the fingerprints
// file holds, until pick learns which operation the provider calls.
func ambiguityReport(ambiguities map[string][]string) string {
	if len(ambiguities) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("### Matches several operations of the specification: teach pick in tools/specdrift/main.go which one the provider calls\n\n")
	for _, r := range slices.Sorted(maps.Keys(ambiguities)) {
		fmt.Fprintf(&b, "- `%s`: `%s`\n", r, strings.Join(ambiguities[r], "`, `"))
	}
	b.WriteString("\n")
	return b.String()
}

// section writes a Markdown section titled title that lists routes, and
// nothing when routes is empty.
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
