// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// gitRepo commits testdata/base on main, then testdata/head on a branch
// "change" that it leaves checked out, and returns the repository. It sets
// environment variables with t.Setenv, so the tests that call it cannot run
// in parallel.
func gitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found")
	}
	// No user or system configuration (signing, hooks, default branch).
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	copyTree(t, filepath.Join("testdata", "base"), dir)
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "base")
	runGit(t, dir, "checkout", "-q", "-b", "change")
	if err := os.RemoveAll(filepath.Join(dir, "internal")); err != nil {
		t.Fatal(err)
	}
	copyTree(t, filepath.Join("testdata", "head"), dir)
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-q", "-m", "change")
	return dir
}

// runGit runs git in dir with a committer identity, and returns its trimmed
// output.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := git(dir, append([]string{"-c", "user.name=t", "-c", "user.email=t@example.com"}, args...)...)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(out))
}

// copyTree copies the files under from to to.
func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(from, p)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		dst := filepath.Join(to, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
			return err
		}
		return os.WriteFile(dst, content, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// fixtureOptions are the options of run on the repository of gitRepo.
func fixtureOptions(dir, base string) options {
	return options{base: base, root: dir, limit: fixtureLimit, dirs: []string{"internal/"}, exclude: regexp.MustCompile(defaultExclude)}
}

// TestRunAgainstGit checks run against main, against HEAD, and with an
// uncommitted change.
func TestRunAgainstGit(t *testing.T) {
	dir := gitRepo(t)
	mergeBase := runGit(t, dir, "rev-parse", "main")

	var out strings.Builder
	err := run(&out, fixtureOptions(dir, "main"))
	if !errors.Is(err, errRatchetFailed) {
		t.Fatalf("run against main: err = %v, want errRatchetFailed", err)
	}
	for _, w := range []string{
		"| A new function scores 3 or less | 2 above | **FAIL** |",
		"8 functions score above 3 at the merge base (`" + mergeBase[:12] + "`).",
		" | - | 5 | gone: deleted, renamed or moved to another package |",
		"Scope: 13 functions",
	} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("report lacks %q:\n%s", w, out.String())
		}
	}

	// Against HEAD itself, the merge base is the working tree's commit: the
	// files out of scope (test, generated, testdata, sdktest, outside
	// internal/) must be left out of the merge base too, or they would show
	// up as gone.
	out.Reset()
	if err := run(&out, fixtureOptions(dir, "HEAD")); err != nil {
		t.Fatalf("run against HEAD: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "All rules pass.") || strings.Contains(out.String(), "gone") {
		t.Errorf("run against HEAD:\n%s", out.String())
	}

	// The working tree counts, committed or not.
	if err := os.WriteFile(filepath.Join(dir, "internal", "a", "a.go"), []byte("package a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := run(&out, fixtureOptions(dir, "HEAD")); err != nil {
		t.Fatalf("run against HEAD after an edit: %v", err)
	}
	if !strings.Contains(out.String(), "| `internal/a.Untouched` | `internal/a/a.go:11` | - | 5 | gone") {
		t.Errorf("an uncommitted deletion is not seen:\n%s", out.String())
	}
}

// TestRunInputErrors checks that a -base git refuses is an input error.
func TestRunInputErrors(t *testing.T) {
	dir := gitRepo(t)
	for _, base := range []string{"--output=x", "no-such-branch"} {
		var out strings.Builder
		err := run(&out, fixtureOptions(dir, base))
		if err == nil || errors.Is(err, errRatchetFailed) {
			t.Errorf("base %q: err = %v, want an input error", base, err)
		}
	}
}

// TestRunKeepsTheKeyOfARepeatedName checks, through git, that a change
// adding a build-tagged twin of a function above the cap leaves that
// function unchanged rather than new.
func TestRunKeepsTheKeyOfARepeatedName(t *testing.T) {
	dir := gitRepo(t)
	twin := "//go:build windows\n\npackage a\n\nfunc Untouched() {}\n"
	if err := os.WriteFile(filepath.Join(dir, "internal", "a", "untouched_windows.go"), []byte(twin), 0o600); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := run(&out, fixtureOptions(dir, "HEAD")); err != nil {
		t.Fatalf("run against HEAD: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "| `internal/a.Untouched in internal/a/a.go` | `internal/a/a.go:11` | 5 | 5 | unchanged |") {
		t.Errorf("Untouched is not unchanged:\n%s", out.String())
	}
}

// TestRunSeveralDirs checks run over internal/ and cmd/.
func TestRunSeveralDirs(t *testing.T) {
	dir := gitRepo(t)
	opts := fixtureOptions(dir, "main")
	opts.dirs = []string{"internal/", "cmd/"}
	var out strings.Builder
	if err := run(&out, opts); !errors.Is(err, errRatchetFailed) {
		t.Fatalf("run against main: err = %v, want errRatchetFailed", err)
	}
	for _, w := range []string{
		"| A new function scores 3 or less | 3 above | **FAIL** |",
		"| `cmd.OutsideInternal` | `cmd/main.go:6` | 6 | - | **FAIL**: new function above 3 |",
		"Scope: 15 functions in the non-test Go files under `internal/` and `cmd/`, outside",
	} {
		if !strings.Contains(out.String(), w) {
			t.Errorf("report lacks %q:\n%s", w, out.String())
		}
	}
	out.Reset()
	opts.base = "HEAD"
	if err := run(&out, opts); err != nil {
		t.Fatalf("run against HEAD: %v\n%s", err, out.String())
	}
}

// TestValidDirs checks the directories -dir accepts.
func TestValidDirs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		dir  string
		want bool
	}{
		{"internal/", true},
		{"internal/,tools/", true},
		{"internal", false},
		{"internal/,tools", false},
		{"internal/,", false},
		{"/", false},
	} {
		if got := validDirs(strings.Split(tc.dir, ",")); got != tc.want {
			t.Errorf("validDirs(%q) = %v, want %v", tc.dir, got, tc.want)
		}
	}
}
