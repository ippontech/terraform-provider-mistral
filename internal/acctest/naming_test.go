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
	"strings"
	"testing"
)

// liveCalls are the calls that mark a test of the live API: resource.Test and
// resource.ParallelTest run only with TF_ACC, and the prechecks of acctest
// skip a test without it.
var liveCalls = map[string]bool{
	"resource.Test": true, "resource.ParallelTest": true,
	"acctest.PreCheck": true, "acctest.PreCheckAdmin": true, "acctest.SkipUnlessAcc": true,
}

// TestLiveTestsAreNamedTestAcc checks that every test function of internal/
// that calls one of liveCalls is named TestAcc followed by a capital letter:
// make testacc runs only those names (-run '^TestAcc[A-Z]'), and would leave
// any other one out without a word.
func TestLiveTestsAreNamedTestAcc(t *testing.T) {
	t.Parallel()
	acceptance := regexp.MustCompile(`^TestAcc[A-Z]`)
	found := 0
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, "_test.go") {
			return err
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Body == nil || !strings.HasPrefix(fn.Name.Name, "Test") || !callsLiveAPI(fn.Body) {
				continue
			}
			found++
			if !acceptance.MatchString(fn.Name.Name) {
				t.Errorf("%s: %s reaches the live API, so its name must start with TestAcc and a capital letter", path, fn.Name.Name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found == 0 {
		t.Error("no test of the live API found under internal/: the walk no longer reaches the services")
	}
}

// callsLiveAPI reports whether body calls one of liveCalls.
func callsLiveAPI(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				if pkg, ok := sel.X.(*ast.Ident); ok && liveCalls[pkg.Name+"."+sel.Sel.Name] {
					found = true
				}
			}
		}
		return !found
	})
	return found
}
