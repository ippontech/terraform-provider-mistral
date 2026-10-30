// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/uudashr/gocognit"
)

// scope selects the files the ratchet checks.
type scope struct {
	// dirs are the repository-relative directories checked, each with a
	// trailing slash.
	dirs []string
	// exclude matches the module-relative paths left out (the test-helper
	// packages).
	exclude *regexp.Regexp
}

// includes reports whether a module-relative, slash-separated path is a
// non-test Go file the ratchet checks. Like the go command, it skips testdata
// directories and those whose name starts with "." or "_".
func (s scope) includes(name string) bool {
	if !s.under(name) || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
		return false
	}
	if s.exclude != nil && s.exclude.MatchString(name) {
		return false
	}
	for d := range strings.SplitSeq(path.Dir(name), "/") {
		if d == "testdata" || strings.HasPrefix(d, ".") || strings.HasPrefix(d, "_") {
			return false
		}
	}
	return true
}

// under reports whether a path lies in one of the directories checked.
func (s scope) under(name string) bool {
	return slices.ContainsFunc(s.dirs, func(dir string) bool { return strings.HasPrefix(name, dir) })
}

// tree maps the module-relative, slash-separated paths of Go files to their
// content.
type tree map[string][]byte

// function is one function or method declaration and its score.
type function struct {
	// key identifies the function across the two trees: its package
	// directory, then its name as gocognit prints it, e.g.
	// "internal/services/agents.(*AgentResource).Update", qualified by
	// file when the name repeats (keyAcross).
	key   string
	file  string
	line  int
	score int
	// src is the declaration from `func` to the closing brace: when it
	// differs between the two trees, the change touched the function.
	src []byte
}

// scoreTree scores every function declared in the files of t, generated
// files aside, in file order then declaration order, each with its key
// unqualified (see keyed). The scores are gocognit's, computed file by file
// as golangci-lint's gocognit linter does, except that the //gocognit:ignore
// directive is not honoured.
func scoreTree(t tree) ([]function, error) {
	var out []function
	for _, name := range slices.Sorted(maps.Keys(t)) {
		fns, err := scoreFile(name, t[name])
		if err != nil {
			return nil, err
		}
		out = append(out, fns...)
	}
	return out, nil
}

// keyAcross indexes the functions of the merge base and of the working tree
// by key, with the same rule in both trees: a key declared more than once in
// either tree is qualified in both. Qualifying it in one tree only would
// change the key of a function that a change leaves alone, when the change
// adds or removes a second declaration of its name (a second init, a
// build-tagged twin): the ratchet would then report the function as new, and
// its old key as gone.
func keyAcross(base, head []function) (map[string]function, map[string]function) {
	repeated := map[string]bool{}
	for _, fns := range [][]function{base, head} {
		seen := map[string]bool{}
		for _, fn := range fns {
			repeated[fn.key] = repeated[fn.key] || seen[fn.key]
			seen[fn.key] = true
		}
	}
	return keyed(base, repeated), keyed(head, repeated)
}

// keyed indexes fns by key. A repeated key, that of init functions or of one
// function per build-tagged file, is told apart by file, then by rank within
// the file: "internal/a.init in internal/a/a.go", then "...#2".
func keyed(fns []function, repeated map[string]bool) map[string]function {
	out := make(map[string]function, len(fns))
	rank := map[string]int{}
	for _, fn := range fns {
		if repeated[fn.key] {
			inFile := fmt.Sprintf("%s in %s", fn.key, fn.file)
			rank[inFile]++
			fn.key = inFile
			if rank[inFile] > 1 {
				fn.key = fmt.Sprintf("%s#%d", inFile, rank[inFile])
			}
		}
		out[fn.key] = fn
	}
	return out
}

// scoreFile scores the functions declared in one file, none when the file is
// generated, each keyed by its package directory and name.
func scoreFile(name string, src []byte) ([]function, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	if ast.IsGenerated(f) {
		return nil, nil
	}
	recursive := gocognit.RecursiveFuncs([]*ast.File{f}, nil)
	var fns []function
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		start, end := fset.Position(fd.Pos()), fset.Position(fd.End())
		fns = append(fns, function{
			key:   path.Dir(name) + "." + funcName(fd),
			file:  name,
			line:  start.Line,
			score: gocognit.ScanComplexityWithRecursion(fd, recursive[fd], false).Complexity,
			src:   bytes.Clone(src[start.Offset:end.Offset]),
		})
	}
	return fns, nil
}

// funcName returns "Name" for a function and "(T).Name" or "(*T).Name" for a
// method, type parameters left out, as gocognit and golangci-lint print it.
func funcName(fd *ast.FuncDecl) string {
	if fd.Recv == nil || len(fd.Recv.List) == 0 {
		return fd.Name.Name
	}
	return fmt.Sprintf("(%s).%s", recvString(fd.Recv.List[0].Type), fd.Name.Name)
}

// recvString returns the type name of a receiver, with its "*" for a pointer
// and without its type parameters.
func recvString(x ast.Expr) string {
	switch t := x.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + recvString(t.X)
	case *ast.IndexExpr:
		return recvString(t.X)
	case *ast.IndexListExpr:
		return recvString(t.X)
	case *ast.ParenExpr:
		return recvString(t.X)
	}
	return "BADRECV"
}
