// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// The fixtures in testdata are two trees of a module, base and head. In
// internal/a, every function scores the number of if statements it holds,
// and the tests cap the score at 3. The head tree also holds files out of
// scope, each with a function scoring 6: a test file, a generated file, a
// file under testdata, a test-helper package (internal/sdktest) and a file
// outside internal/.
const fixtureLimit = 3

// fixtureScope is the scope of the fixtures: internal/, test helpers aside.
func fixtureScope() scope {
	return scope{dirs: []string{"internal/"}, exclude: regexp.MustCompile(defaultExclude)}
}

// scoreFixture scores the functions of the fixture tree testdata/<name>.
func scoreFixture(t *testing.T, name string) []function {
	t.Helper()
	tr, err := dirTree(filepath.Join("testdata", name), fixtureScope())
	if err != nil {
		t.Fatal(err)
	}
	return mustScore(t, tr)
}

// loadFixture scores the fixture tree testdata/<name> and keys its functions
// on their own.
func loadFixture(t *testing.T, name string) map[string]function {
	t.Helper()
	fns, _ := keyAcross(scoreFixture(t, name), nil)
	return fns
}

// loadFixtures scores two fixture trees and keys their functions as run
// does; "" names an empty tree.
func loadFixtures(t *testing.T, base, head string) (map[string]function, map[string]function) {
	t.Helper()
	var baseFns []function
	if base != "" {
		baseFns = scoreFixture(t, base)
	}
	return keyAcross(baseFns, scoreFixture(t, head))
}

// TestScopeIncludes checks the files of internal/ that the ratchet checks.
func TestScopeIncludes(t *testing.T) {
	t.Parallel()
	sc := fixtureScope()
	for name, want := range map[string]bool{
		"internal/a/a.go":                   true,
		"internal/services/agents/x.go":     true,
		"internal/a/a_test.go":              false,
		"internal/a/a.txt":                  false,
		"internal/a/testdata/x.go":          false,
		"internal/a/.hidden/x.go":           false,
		"internal/a/_skip/x.go":             false,
		"internal/sdktest/server.go":        false,
		"internal/acctest/acctest.go":       false,
		"internal/sdktestextra/x.go":        true,
		"main.go":                           false,
		"tools/complexity/main.go":          false,
		"cmd/internal/a.go":                 false,
		"internal/providertest/provider.go": false,
	} {
		if got := sc.includes(name); got != want {
			t.Errorf("includes(%q) = %v, want %v", name, got, want)
		}
	}
}

// TestScoreTree checks the keys and scores of the functions of the head
// fixture.
func TestScoreTree(t *testing.T) {
	t.Parallel()
	head := loadFixture(t, "head")
	want := map[string]int{
		"internal/a.Untouched":    5,
		"internal/a.Touched":      5,
		"internal/a.TouchedLower": 4,
		"internal/a.Rose":         5,
		"internal/a.Crossed":      4,
		"internal/a.Lowered":      2,
		"internal/a.(*T).Method":  4,
		"internal/a.New":          4,
		"internal/a.Small":        1,
		// gocognit itself skips a function with this directive; the ratchet
		// does not.
		"internal/a.Ignored": 4,
		"internal/a.Moved":   5,
		// Two init functions: told apart by file.
		"internal/a.init in internal/a/a.go": 1,
		"internal/a.init in internal/a/c.go": 1,
	}
	if len(head) != len(want) {
		t.Errorf("scored %d functions, want %d: %v", len(head), len(want), keys(head))
	}
	for key, score := range want {
		fn, ok := head[key]
		if !ok {
			t.Errorf("%s not scored", key)
			continue
		}
		if fn.score != score {
			t.Errorf("%s scores %d, want %d", key, fn.score, score)
		}
	}
	if fn := head["internal/a.Moved"]; fn.file != "internal/a/b.go" || fn.line != 6 {
		t.Errorf("Moved at %s:%d, want internal/a/b.go:6", fn.file, fn.line)
	}
}

// keys returns the keys of m, for a failure message.
func keys(m map[string]function) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestScoreTreeRejectsInvalidGo checks that a file that does not parse is an
// error.
func TestScoreTreeRejectsInvalidGo(t *testing.T) {
	t.Parallel()
	if _, err := scoreTree(tree{"internal/a/a.go": []byte("package a\nfunc {")}); err == nil {
		t.Fatal("no error for a file that does not parse")
	}
}

// TestEvaluate checks the state of each function from the base fixture to
// the head fixture.
func TestEvaluate(t *testing.T) {
	t.Parallel()
	base, head := loadFixtures(t, "base", "head")
	r := evaluate(base, head, fixtureLimit)
	type want struct {
		score, base int
		state       state
	}
	wants := map[string]want{
		"internal/a.New":          {4, 0, added},
		"internal/a.Ignored":      {4, 0, added},
		"internal/a.Crossed":      {4, 2, crossed},
		"internal/a.Rose":         {5, 4, rose},
		"internal/a.Touched":      {5, 5, touched},
		"internal/a.TouchedLower": {4, 5, touched},
		"internal/a.(*T).Method":  {4, 4, touched},
		"internal/a.Lowered":      {2, 5, lowered},
		"internal/a.Removed":      {0, 5, removed},
		"internal/a.Untouched":    {5, 5, unchanged},
		// Moved to another file of the package, unchanged.
		"internal/a.Moved": {5, 5, unchanged},
	}
	if len(r.entries) != len(wants) {
		t.Errorf("%d entries, want %d: %+v", len(r.entries), len(wants), r.entries)
	}
	for i, e := range r.entries {
		w, ok := wants[e.key]
		if !ok {
			t.Errorf("unexpected entry %+v", e)
			continue
		}
		if got := (want{e.score, e.baseScore, e.state}); got != w {
			t.Errorf("%s: got %+v, want %+v", e.key, got, w)
		}
		if i > 0 && r.entries[i-1].state > e.state {
			t.Errorf("entries not sorted by state: %s before %s", r.entries[i-1].key, e.key)
		}
	}
	if r.passed() || r.failures() != 4 {
		t.Errorf("passed=%v failures=%d, want false and 4", r.passed(), r.failures())
	}
	if r.baseAbove != 8 || r.checked != 13 {
		t.Errorf("baseAbove=%d checked=%d, want 8 and 13", r.baseAbove, r.checked)
	}
}

// TestEvaluateRaisedByRecursion checks that a function whose score rises
// through a recursion cycle fails.
func TestEvaluateRaisedByRecursion(t *testing.T) {
	t.Parallel()
	// gocognit adds one point to a function that takes part in a recursion
	// cycle: the score of an unchanged function can rise when a change adds
	// the other half of the cycle, and the ratchet catches it.
	base := mustScore(t, tree{"internal/a/a.go": []byte(`package a

func f(x int) int {
	if x > 0 {
		if x > 1 {
			return 1
		}
	}
	return g(x)
}

func g(x int) int { return x }
`)})
	head := mustScore(t, tree{"internal/a/a.go": []byte(`package a

func f(x int) int {
	if x > 0 {
		if x > 1 {
			return 1
		}
	}
	return g(x)
}

func g(x int) int { return f(x - 1) }
`)})
	baseFns, headFns := keyAcross(base, head)
	r := evaluate(baseFns, headFns, 2)
	if len(r.entries) != 1 || r.entries[0].key != "internal/a.f" || r.entries[0].state != rose {
		t.Fatalf("entries %+v, want f rising", r.entries)
	}
}

// mustScore scores the functions of tr, failing the test on an error.
func mustScore(t *testing.T, tr tree) []function {
	t.Helper()
	fns, err := scoreTree(tr)
	if err != nil {
		t.Fatal(err)
	}
	return fns
}

// TestEvaluateUnchangedTreePasses checks that a tree compared with itself
// passes, every function above the cap unchanged.
func TestEvaluateUnchangedTreePasses(t *testing.T) {
	t.Parallel()
	base, head := loadFixtures(t, "base", "base")
	r := evaluate(base, head, fixtureLimit)
	if !r.passed() || len(r.entries) != r.count(unchanged) || r.count(unchanged) != 8 {
		t.Errorf("passed=%v entries=%+v, want 8 unchanged", r.passed(), r.entries)
	}
}

// reportOf returns the report of r with fixed details.
func reportOf(t *testing.T, r result) string {
	t.Helper()
	var out strings.Builder
	rep := report{mergeBase: "0123456789ab", dirs: []string{"internal/"}, exclude: defaultExclude, gocognit: "v1.3.0"}
	if err := writeReport(&out, r, rep); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// TestReport checks the Markdown report of failures, of a tree left alone
// and of an absolute cap.
func TestReport(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		base, head    string // fixture trees, "" for none
		limit         int
		want, notWant []string
	}{
		{
			name: "failures", base: "base", head: "head", limit: fixtureLimit,
			want: []string{
				"**The ratchet fails**: see the functions marked FAIL.",
				"| A new function scores 3 or less | 2 above | **FAIL** |",
				"| A function at 3 or less at the merge base stays at 3 or less | 1 above | **FAIL** |",
				"| A function above 3 at the merge base may change, its score may not rise | 1 rose, 3 changed without rising, 1 brought to 3 or less | **FAIL** |",
				"8 functions score above 3 at the merge base (`0123456789ab`). Once none is left, the cap is absolute.",
				"To fix a failure, split the function",
				"| `internal/a.New` | `internal/a/a.go:",
				" | 4 | - | **FAIL**: new function above 3 |",
				" | 4 | 2 | **FAIL**: was 3 or less at the merge base |",
				" | 5 | 4 | **FAIL**: rose from 4 |",
				" | 5 | 5 | changed, not higher (pass) |",
				" | 4 | 5 | changed, lowered from 5 (pass) |",
				" | 2 | 5 | brought to 3 or less |",
				" | - | 5 | gone: deleted, renamed or moved to another package |",
				"<details><summary>2 other functions above 3 at the merge base, unchanged</summary>",
				"| `internal/a.Moved` | `internal/a/b.go:6` | 5 | 5 | unchanged |",
				"Scope: 13 functions in the non-test Go files under `internal/`, outside `" + defaultExclude + "` and generated files. Scores from gocognit v1.3.0",
			},
			notWant: []string{"Small", "init", "Generated", "testHelper", "InTestdata", "Helper", "OutsideInternal", "All rules pass."},
		},
		{
			name: "nothing changed", base: "base", head: "base", limit: fixtureLimit,
			want:    []string{"All rules pass.", "| 0 rose, 0 changed without rising, 0 brought to 3 or less | pass |", "<details><summary>8 other functions"},
			notWant: []string{"### Functions this change concerns", "To fix a failure"},
		},
		{
			name: "nothing above the cap at the merge base: absolute cap", base: "base", head: "head", limit: 5,
			want:    []string{"All rules pass.", "No function scores above 5 at the merge base (`0123456789ab`): the cap is absolute."},
			notWant: []string{"<details>", "### Functions this change concerns"},
		},
		{
			name: "absolute cap, a new function above it", base: "", head: "head", limit: 4,
			want: []string{
				"**The ratchet fails**",
				"No function scores above 4 at the merge base (`0123456789ab`): the cap is absolute.",
				"| A new function scores 4 or less | 4 above | **FAIL** |",
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			base, head := loadFixtures(t, tc.base, tc.head)
			out := reportOf(t, evaluate(base, head, tc.limit))
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("report lacks %q:\n%s", w, out)
				}
			}
			for _, nw := range tc.notWant {
				if strings.Contains(out, nw) {
					t.Errorf("report holds %q:\n%s", nw, out)
				}
			}
		})
	}
}

// TestScopeIncludesSeveralDirs checks the files the ratchet checks when it
// covers internal/ and tools/.
func TestScopeIncludesSeveralDirs(t *testing.T) {
	t.Parallel()
	sc := scope{dirs: []string{"internal/", "tools/"}, exclude: regexp.MustCompile(defaultExclude)}
	for name, want := range map[string]bool{
		"internal/a/a.go":                                true,
		"tools/complexity/main.go":                       true,
		"tools/coversummary/gate.go":                     true,
		"tools/complexity/main_test.go":                  false,
		"tools/complexity/testdata/head/internal/a/a.go": false,
		"tools/complexity/testdata/head/cmd/main.go":     false,
		"internal/sdktest/server.go":                     false,
		"main.go":                                        false,
		"toolsextra/x.go":                                false,
	} {
		if got := sc.includes(name); got != want {
			t.Errorf("includes(%q) = %v, want %v", name, got, want)
		}
	}
}

// TestDirTreeSeveralDirs checks that dirTree reads every directory of the
// scope, and fails on one that does not exist.
func TestDirTreeSeveralDirs(t *testing.T) {
	t.Parallel()
	sc := scope{dirs: []string{"internal/", "cmd/"}, exclude: regexp.MustCompile(defaultExclude)}
	tr, err := dirTree(filepath.Join("testdata", "head"), sc)
	if err != nil {
		t.Fatal(err)
	}
	fns, _ := keyAcross(mustScore(t, tr), nil)
	if got := fns["cmd.OutsideInternal"].score; got != 6 {
		t.Errorf("cmd.OutsideInternal scores %d, want 6 (functions: %d)", got, len(fns))
	}
	if _, ok := fns["internal/a.New"]; !ok {
		t.Error("internal/a.New is missing")
	}
	if _, err := dirTree(filepath.Join("testdata", "head"), scope{dirs: []string{"absent/"}}); err == nil {
		t.Error("a directory that does not exist is not an error")
	}
}

// TestCodeList checks the list of directories in the report.
func TestCodeList(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   []string
		want string
	}{
		{[]string{"internal/"}, "`internal/`"},
		{[]string{"internal/", "tools/"}, "`internal/` and `tools/`"},
		{[]string{"a/", "b/", "c/"}, "`a/`, `b/` and `c/`"},
	} {
		if got := codeList(tc.in); got != tc.want {
			t.Errorf("codeList(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestKeysStableWhenANameRepeats checks that a function above the cap that a
// change leaves alone stays unchanged when the change adds or removes another
// declaration of its name: a second init, or a build-tagged twin.
func TestKeysStableWhenANameRepeats(t *testing.T) {
	t.Parallel()
	// Above the cap of 3: four nested ifs score 1+2+3+4.
	const big = "\tif a {\n\t\tif b {\n\t\t\tif c {\n\t\t\t\tif d {\n\t\t\t\t}\n\t\t\t}\n\t\t}\n\t}\n"
	initBig := []byte("package a\n\nfunc init() {\n" + big + "}\n")
	initSmall := []byte("package a\n\nfunc init() {}\n")
	for _, tc := range []struct {
		name       string
		base, head tree
		key        string
	}{
		{
			name: "second init added in another file",
			base: tree{"internal/a/a.go": initBig},
			head: tree{"internal/a/a.go": initBig, "internal/a/b.go": initSmall},
			key:  "internal/a.init in internal/a/a.go",
		},
		{
			name: "second init removed",
			base: tree{"internal/a/a.go": initBig, "internal/a/b.go": initSmall},
			head: tree{"internal/a/a.go": initBig},
			key:  "internal/a.init in internal/a/a.go",
		},
		{
			name: "second init added after the first in its file",
			base: tree{"internal/a/a.go": initBig},
			head: tree{"internal/a/a.go": append(slices.Clone(initBig), "\nfunc init() {}\n"...)},
			key:  "internal/a.init in internal/a/a.go",
		},
		{
			name: "build-tagged twin added",
			base: tree{"internal/a/f_linux.go": []byte("package a\n\nfunc F() {\n" + big + "}\n")},
			head: tree{
				"internal/a/f_linux.go":   []byte("package a\n\nfunc F() {\n" + big + "}\n"),
				"internal/a/f_windows.go": []byte("package a\n\nfunc F() {}\n"),
			},
			key: "internal/a.F in internal/a/f_linux.go",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			base, head := keyAcross(mustScore(t, tc.base), mustScore(t, tc.head))
			r := evaluate(base, head, fixtureLimit)
			if !r.passed() || len(r.entries) != 1 || r.entries[0].key != tc.key || r.entries[0].state != unchanged {
				t.Errorf("entries %+v, want %s alone, unchanged", r.entries, tc.key)
			}
		})
	}
}

// scoring returns the source of a function named name that holds n nested
// ifs, so that it scores 1+2+...+n.
func scoring(name string, n int) string {
	return "\nfunc " + name + "() {\n" + strings.Repeat("if x {\n", n) + strings.Repeat("}\n", n) + "}\n"
}

// pairingCase is a change to the declarations of a repeated name, and what
// the ratchet must say about it at a cap of 3.
type pairingCase struct {
	name       string
	base, head tree
	passes     bool
	entries    map[string]pairingEntry
}

// pairingEntry is what the report says about one function.
type pairingEntry struct {
	score, base int
	state       state
}

// checkPairing evaluates each case through keyAcross, as run does.
func checkPairing(t *testing.T, cases []pairingCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			base, head := keyAcross(mustScore(t, tc.base), mustScore(t, tc.head))
			r := evaluate(base, head, fixtureLimit)
			got := map[string]pairingEntry{}
			for _, e := range r.entries {
				got[e.key] = pairingEntry{e.score, e.baseScore, e.state}
			}
			if !reflect.DeepEqual(got, tc.entries) || r.passed() != tc.passes {
				t.Errorf("entries %+v, passed %v; want %+v, passed %v", got, r.passed(), tc.entries, tc.passes)
			}
		})
	}
}

// pkg returns the source of a file of package a holding decls.
func pkg(decls ...string) []byte { return []byte("package a\n" + strings.Join(decls, "")) }

// TestRepeatedInitsArePairedBySource checks that the init functions of a
// package that has several are paired across the trees by their source only,
// wherever they moved: an edited one counts as new, never compared with the
// score of another, whether a change edits one in place, moves and edits it,
// or deletes one and adds another.
func TestRepeatedInitsArePairedBySource(t *testing.T) {
	t.Parallel()
	ten, six, one := scoring("init", 4), scoring("init", 3), scoring("init", 1)
	checkPairing(t, []pairingCase{
		{
			name: "first of two removed, the second raised above the cap",
			base: tree{"internal/a/a.go": pkg(ten, one)}, head: tree{"internal/a/a.go": pkg(six)},
			entries: map[string]pairingEntry{
				"internal/a.init in internal/a/a.go":              {6, 0, added},
				"internal/a.init in internal/a/a.go (merge base)": {0, 10, removed},
			},
		},
		{
			name: "first of two removed, the second unchanged",
			base: tree{"internal/a/a.go": pkg(ten, six)}, head: tree{"internal/a/a.go": pkg(six)}, passes: true,
			entries: map[string]pairingEntry{
				"internal/a.init in internal/a/a.go":              {6, 6, unchanged},
				"internal/a.init in internal/a/a.go (merge base)": {0, 10, removed},
			},
		},
		{
			name: "identical in two files, one removed",
			base: tree{"internal/a/a.go": pkg(ten), "internal/a/b.go": pkg(ten)}, head: tree{"internal/a/b.go": pkg(ten)}, passes: true,
			entries: map[string]pairingEntry{
				"internal/a.init in internal/a/b.go": {10, 10, unchanged},
				"internal/a.init in internal/a/a.go": {0, 10, removed},
			},
		},
		{
			name: "moved to another file, unchanged",
			base: tree{"internal/a/a.go": pkg(ten), "internal/a/b.go": pkg(one)}, head: tree{"internal/a/c.go": pkg(ten), "internal/a/b.go": pkg(one)}, passes: true,
			entries: map[string]pairingEntry{"internal/a.init in internal/a/c.go": {10, 10, unchanged}},
		},
		{
			name: "two moved to another file and swapped, unchanged",
			base: tree{"internal/a/a.go": pkg(ten, six)}, head: tree{"internal/a/c.go": pkg(six, ten)}, passes: true,
			entries: map[string]pairingEntry{
				"internal/a.init in internal/a/c.go":   {6, 6, unchanged},
				"internal/a.init in internal/a/c.go#2": {10, 10, unchanged},
			},
		},
		{
			name: "edited in place, lowered but above the cap",
			base: tree{"internal/a/a.go": pkg(ten, one)}, head: tree{"internal/a/a.go": pkg(six, one)},
			entries: map[string]pairingEntry{
				"internal/a.init in internal/a/a.go":              {6, 0, added},
				"internal/a.init in internal/a/a.go (merge base)": {0, 10, removed},
			},
		},
		{
			name: "edited in place, alone in its file",
			base: tree{"internal/a/a.go": pkg(ten), "internal/a/b.go": pkg(one)}, head: tree{"internal/a/a.go": pkg(six), "internal/a/b.go": pkg(one)},
			entries: map[string]pairingEntry{
				"internal/a.init in internal/a/a.go":              {6, 0, added},
				"internal/a.init in internal/a/a.go (merge base)": {0, 10, removed},
			},
		},
		{
			name: "edited in place, brought to the cap",
			base: tree{"internal/a/a.go": pkg(ten, one)}, head: tree{"internal/a/a.go": pkg(scoring("init", 2), one)}, passes: true,
			entries: map[string]pairingEntry{"internal/a.init in internal/a/a.go (merge base)": {0, 10, removed}},
		},
		{
			name: "the second raised in place",
			base: tree{"internal/a/a.go": pkg(ten, one)}, head: tree{"internal/a/a.go": pkg(ten, six)},
			entries: map[string]pairingEntry{
				"internal/a.init in internal/a/a.go":   {10, 10, unchanged},
				"internal/a.init in internal/a/a.go#2": {6, 0, added},
			},
		},
		{
			name: "grandfathered deleted, a new one above the cap in another file",
			base: tree{"internal/a/a.go": pkg(ten), "internal/a/b.go": pkg(one)}, head: tree{"internal/a/b.go": pkg(one), "internal/a/c.go": pkg(six)},
			entries: map[string]pairingEntry{
				"internal/a.init in internal/a/c.go": {6, 0, added},
				"internal/a.init in internal/a/a.go": {0, 10, removed},
			},
		},
		{
			name: "grandfathered deleted, a new one above the cap in its file",
			base: tree{"internal/a/a.go": pkg(ten, one)}, head: tree{"internal/a/a.go": pkg(one, six)},
			entries: map[string]pairingEntry{
				"internal/a.init in internal/a/a.go#2":            {6, 0, added},
				"internal/a.init in internal/a/a.go (merge base)": {0, 10, removed},
			},
		},
		{
			name: "functions named _ too",
			base: tree{"internal/a/a.go": pkg(scoring("_", 4), scoring("_", 1))}, head: tree{"internal/a/a.go": pkg(scoring("_", 3), scoring("_", 1))},
			entries: map[string]pairingEntry{
				"internal/a._ in internal/a/a.go":              {6, 0, added},
				"internal/a._ in internal/a/a.go (merge base)": {0, 10, removed},
			},
		},
	})
}

// TestBuildTaggedTwinsArePairedByFile checks that the declarations of a
// repeated name other than init, one per build-tagged file, are paired by
// file first, whatever their source: Go allows one declaration of such a name
// per file. A twin moved to a renamed file pairs by its source if unchanged,
// and counts as new if changed.
func TestBuildTaggedTwinsArePairedByFile(t *testing.T) {
	t.Parallel()
	ten, six, one := scoring("F", 4), scoring("F", 3), scoring("F", 1)
	checkPairing(t, []pairingCase{
		{
			name: "edited in place, lowered",
			base: tree{"internal/a/f_linux.go": pkg(ten), "internal/a/f_windows.go": pkg(one)},
			head: tree{"internal/a/f_linux.go": pkg(six), "internal/a/f_windows.go": pkg(one)}, passes: true,
			entries: map[string]pairingEntry{"internal/a.F in internal/a/f_linux.go": {6, 10, touched}},
		},
		{
			name: "raised in place",
			base: tree{"internal/a/f_linux.go": pkg(ten), "internal/a/f_windows.go": pkg(one)},
			head: tree{"internal/a/f_linux.go": pkg(ten), "internal/a/f_windows.go": pkg(six)},
			entries: map[string]pairingEntry{
				"internal/a.F in internal/a/f_linux.go":   {10, 10, unchanged},
				"internal/a.F in internal/a/f_windows.go": {6, 1, crossed},
			},
		},
		{
			name: "a method named init, edited in place",
			base: tree{"internal/a/f_linux.go": pkg(scoring("(T) init", 4)), "internal/a/f_windows.go": pkg(scoring("(T) init", 1))},
			head: tree{"internal/a/f_linux.go": pkg(scoring("(T) init", 3)), "internal/a/f_windows.go": pkg(scoring("(T) init", 1))}, passes: true,
			entries: map[string]pairingEntry{"internal/a.(T).init in internal/a/f_linux.go": {6, 10, touched}},
		},
		{
			name: "raised by copying the former body of another twin",
			base: tree{"internal/a/f_linux.go": pkg(ten), "internal/a/f_windows.go": pkg(one)},
			head: tree{"internal/a/f_linux.go": pkg(scoring("F", 2)), "internal/a/f_windows.go": pkg(ten)},
			entries: map[string]pairingEntry{
				"internal/a.F in internal/a/f_linux.go":   {3, 10, lowered},
				"internal/a.F in internal/a/f_windows.go": {10, 1, crossed},
			},
		},
		{
			name: "moved to a renamed file, unchanged",
			base: tree{"internal/a/f_linux.go": pkg(ten), "internal/a/f_windows.go": pkg(one)},
			head: tree{"internal/a/f_unix.go": pkg(ten), "internal/a/f_windows.go": pkg(six)},
			entries: map[string]pairingEntry{
				"internal/a.F in internal/a/f_unix.go":    {10, 10, unchanged},
				"internal/a.F in internal/a/f_windows.go": {6, 1, crossed},
			},
		},
		{
			name: "moved to another file and edited",
			base: tree{"internal/a/f_linux.go": pkg(ten), "internal/a/f_windows.go": pkg(one)},
			head: tree{"internal/a/f_unix.go": pkg(six), "internal/a/f_windows.go": pkg(one)},
			entries: map[string]pairingEntry{
				"internal/a.F in internal/a/f_unix.go":  {6, 0, added},
				"internal/a.F in internal/a/f_linux.go": {0, 10, removed},
			},
		},
	})
}

// TestReportExplainsEditedInits checks that a failure on an init among
// several says why it counts as new and what to do, and that other failures
// do not.
func TestReportExplainsEditedInits(t *testing.T) {
	t.Parallel()
	ten, six, one := scoring("init", 4), scoring("init", 3), scoring("init", 1)
	base, head := keyAcross(mustScore(t, tree{"internal/a/a.go": pkg(ten, one)}), mustScore(t, tree{"internal/a/a.go": pkg(six, one)}))
	out := reportOf(t, evaluate(base, head, fixtureLimit))
	for _, want := range []string{
		"| `internal/a.init in internal/a/a.go` | `internal/a/a.go:3` | 6 | - | **FAIL**: new function above 3 (an edited `init` among several counts as new) |",
		"| `internal/a.init in internal/a/a.go (merge base)` | `internal/a/a.go:3` | - | 10 | gone: deleted, edited (an edited `init` among several counts as new) or moved to another package |",
		"An `init` function of a package that has several is matched with the merge base by its source only",
		"move its logic into named functions that score 3 or less each.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
	base, head = keyAcross(mustScore(t, tree{"internal/a/a.go": pkg(scoring("F", 1))}), mustScore(t, tree{"internal/a/a.go": pkg(scoring("F", 3))}))
	if out := reportOf(t, evaluate(base, head, fixtureLimit)); strings.Contains(out, "`init`") {
		t.Errorf("a failure on another function speaks of init:\n%s", out)
	}
}
