// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package acctest_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// liveFuncs are, by import path, the functions that mark a test of the live
// API: resource.Test and resource.ParallelTest run only with TF_ACC, and the
// prechecks of acctest skip a test without it.
var liveFuncs = map[string][]string{
	"github.com/hashicorp/terraform-plugin-testing/helper/resource":    {"Test", "ParallelTest"},
	"github.com/ippontech/terraform-provider-mistral/internal/acctest": {"PreCheck", "PreCheckAdmin", "SkipUnlessAcc"},
}

// acceptanceName is the name make testacc runs (-run '^TestAcc[A-Z]').
var acceptanceName = regexp.MustCompile(`^TestAcc[A-Z]`)

// TestLiveTestsAreNamedTestAcc checks that every test function of internal/
// that reaches the live API (liveTests) is named TestAcc followed by a
// capital letter: make testacc runs only those names, and would leave any
// other one out without a word.
func TestLiveTestsAreNamedTestAcc(t *testing.T) {
	t.Parallel()
	packages := map[string][]*ast.File{}
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return err
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		// The test files of a directory form two packages: <name> and <name>_test.
		key := filepath.Dir(path) + ":" + file.Name.Name
		packages[key] = append(packages[key], file)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for key, files := range packages {
		for _, name := range liveTests(files) {
			found++
			if !acceptanceName.MatchString(name) {
				t.Errorf("%s: %s reaches the live API, so its name must start with TestAcc and a capital letter", key, name)
			}
		}
	}
	if found == 0 {
		t.Error("no test of the live API found under internal/: the walk no longer reaches the services")
	}
}

// liveTests returns the test functions of files, the files of one package,
// that call one of liveFuncs, directly or through the functions of the
// package they call. Each file's imports give the local name of liveFuncs'
// packages, alias or dot import included. A call through a method or a
// function value is not followed: a live test calls resource.Test, or a
// function of its package that does.
func liveTests(files []*ast.File) []string {
	funcs := map[string]*ast.FuncDecl{}
	fileOf := map[string]*ast.File{}
	for _, file := range files {
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Body != nil {
				funcs[fn.Name.Name], fileOf[fn.Name.Name] = fn, file
			}
		}
	}
	live := map[string]bool{}
	for name, fn := range funcs {
		live[name] = callsLiveFunc(fn.Body, importedLiveFuncs(fileOf[name]), nil)
	}
	for changed := true; changed; {
		changed = false
		for name, fn := range funcs {
			if !live[name] && callsLiveFunc(fn.Body, nil, live) {
				live[name], changed = true, true
			}
		}
	}
	var tests []string
	for name := range funcs {
		if live[name] && strings.HasPrefix(name, "Test") {
			tests = append(tests, name)
		}
	}
	slices.Sort(tests)
	return tests
}

// importedLiveFuncs maps the way file calls each of liveFuncs, "resource.Test"
// or, after a dot import, "Test", to true.
func importedLiveFuncs(file *ast.File) map[string]bool {
	calls := map[string]bool{}
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || liveFuncs[path] == nil {
			continue
		}
		prefix := filepath.Base(path) + "."
		if spec.Name != nil {
			prefix = spec.Name.Name + "."
		}
		if prefix == ".." {
			prefix = ""
		}
		for _, name := range liveFuncs[path] {
			calls[prefix+name] = true
		}
	}
	return calls
}

// callsLiveFunc reports whether body calls one of qualified, written as in
// the source ("resource.Test"), or a function of the package that local
// marks as live.
func callsLiveFunc(body *ast.BlockStmt, qualified, local map[string]bool) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return !found
		}
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			found = found || qualified[fun.Name] || local[fun.Name]
		case *ast.SelectorExpr:
			if pkg, ok := fun.X.(*ast.Ident); ok {
				found = found || qualified[pkg.Name+"."+fun.Sel.Name]
			}
		}
		return !found
	})
	return found
}

// TestLiveTestsFollowsAliasesAndHelpers checks liveTests on small packages:
// a live call through an aliased or dot import, or through a helper of the
// package, makes a live test, and a method of the same name on another value
// does not.
func TestLiveTestsFollowsAliasesAndHelpers(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		source string
		want   []string
	}{
		"direct": {`package x
import "github.com/hashicorp/terraform-plugin-testing/helper/resource"
func TestAccDirect(t *testing.T) { resource.Test(t, resource.TestCase{}) }
func TestUnit(t *testing.T) { resource.UnitTest(t, resource.TestCase{}) }`, []string{"TestAccDirect"}},
		"aliased import": {`package x
import tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
func TestAliased(t *testing.T) { tfresource.ParallelTest(t, tfresource.TestCase{}) }`, []string{"TestAliased"}},
		"dot import": {`package x
import . "github.com/ippontech/terraform-provider-mistral/internal/acctest"
func TestDot(t *testing.T) { PreCheck(t) }`, []string{"TestDot"}},
		"helpers": {`package x
import "github.com/hashicorp/terraform-plugin-testing/helper/resource"
func run(t *testing.T) { resource.Test(t, resource.TestCase{}) }
func runTwice(t *testing.T) { run(t); run(t) }
func TestThroughHelpers(t *testing.T) { runTwice(t) }`, []string{"TestThroughHelpers"}},
		"another package named resource": {`package x
import resource "example.com/other/resource"
func TestOther(t *testing.T) { resource.Test(t) }`, nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			file, err := parser.ParseFile(token.NewFileSet(), name+".go", c.source, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			if got := liveTests([]*ast.File{file}); !slices.Equal(got, c.want) {
				t.Errorf("liveTests = %v, want %v", got, c.want)
			}
		})
	}
}
