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
	// nameless marks an init function or a function named _: Go lets a file
	// declare these names more than once, so the name says nothing about
	// which function a declaration is.
	nameless bool
	// sourceOnly marks a nameless declaration among several of its name in
	// one tree: matchRepeated pairs it by its source only.
	sourceOnly bool
}

// scoreTree scores every function declared in the files of t, generated
// files aside, in file order then declaration order, each with its key
// unqualified (see keyAcross). The scores are gocognit's, computed file by file
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
// by key, so that evaluate compares each function with its own former
// version. A name declared at most once in each tree keeps its plain key: the
// function is matched by package and name, wherever it moved in its package;
// so is the only init of a package, which nothing else could be confused
// with.
// The declarations of a name declared more than once in either tree (init
// functions, build-tagged twins) are paired by matchRepeated and keyed by
// keyRepeated.
func keyAcross(base, head []function) (map[string]function, map[string]function) {
	baseByName, headByName := byName(base), byName(head)
	names := map[string]bool{}
	for name := range baseByName {
		names[name] = true
	}
	for name := range headByName {
		names[name] = true
	}
	baseOut, headOut := map[string]function{}, map[string]function{}
	for name := range names {
		b, h := baseByName[name], headByName[name]
		if len(b) > 1 || len(h) > 1 {
			keyRepeated(b, h, baseOut, headOut)
			continue
		}
		for _, fn := range b {
			baseOut[name] = fn
		}
		for _, fn := range h {
			headOut[name] = fn
		}
	}
	return baseOut, headOut
}

// byName groups fns by their plain key, each group in the order of fns.
func byName(fns []function) map[string][]function {
	out := map[string][]function{}
	for _, fn := range fns {
		out[fn.key] = append(out[fn.key], fn)
	}
	return out
}

// keyRepeated adds to baseOut and headOut the declarations of one repeated
// name. A declaration of the working tree is keyed by its file, then by its
// rank within the file: "internal/a.init in internal/a/a.go", then "...#2".
// The declaration of the merge base paired with it takes the same key; an
// unpaired one is keyed by its own position, marked "(merge base)" when a
// declaration of the working tree holds that key, so that evaluate never
// compares the two.
func keyRepeated(b, h []function, baseOut, headOut map[string]function) {
	headKeys := positionKeys(h)
	for j, fn := range h {
		fn.key = headKeys[j]
		fn.sourceOnly = fn.nameless
		headOut[fn.key] = fn
	}
	baseKeys := positionKeys(b)
	for i, j := range matchRepeated(b, h) {
		fn := b[i]
		fn.sourceOnly = fn.nameless
		switch {
		case j >= 0:
			fn.key = headKeys[j]
		case slices.Contains(headKeys, baseKeys[i]):
			fn.key = baseKeys[i] + " (merge base)"
		default:
			fn.key = baseKeys[i]
		}
		baseOut[fn.key] = fn
	}
}

// positionKeys returns the key of each of fns, declarations of one name, by
// file and rank within the file.
func positionKeys(fns []function) []string {
	keys := make([]string, len(fns))
	rank := map[string]int{}
	for i, fn := range fns {
		inFile := fmt.Sprintf("%s in %s", fn.key, fn.file)
		rank[inFile]++
		keys[i] = inFile
		if rank[inFile] > 1 {
			keys[i] = fmt.Sprintf("%s#%d", inFile, rank[inFile])
		}
	}
	return keys
}

// matchRepeated pairs the declarations of one name at the merge base (b) with
// those of the working tree (h), and returns for each of b the index of its
// pair in h, -1 when it has none. It pairs only on evidence, in two passes,
// the second over the declarations the first left unpaired, each in file then
// declaration order.
//
// For init functions and functions named _, which Go lets a file declare more
// than once, the name says nothing about which declaration is which, and
// only the source does:
//
//  1. same file and same source: unchanged;
//  2. same source in another file: unchanged, moved within the package.
//
// For another name (a build-tagged twin), Go allows one declaration per file,
// so the file and the name identify a declaration as a name declared once
// does, and the file comes first: a twin whose new body copies the former
// body of another twin is still compared with its own former version.
//
//  1. same file, changed or not;
//  2. same source, among the declarations left, whose files the other tree
//     does not have: unchanged, moved to a renamed file.
//
// Any other declaration stays unpaired, new in the working tree or gone from
// the merge base, and is never compared with the score of another
// declaration. An init among several that a change edits thus counts as new,
// and must score the cap or less even when it was above it at the merge
// base; the report says so (writeReport).
func matchRepeated(b, h []function) []int {
	m := matching{base: b, head: h, partner: slices.Repeat([]int{-1}, len(b)), taken: make([]bool, len(h))}
	sameSource := func(bf, hf function) bool { return bytes.Equal(bf.src, hf.src) }
	if slices.ContainsFunc(append(slices.Clone(b), h...), func(fn function) bool { return fn.nameless }) {
		m.pair(func(bf, hf function) bool { return bf.file == hf.file && sameSource(bf, hf) })
	} else {
		baseLeft, headLeft := m.unpairedByFile()
		m.pair(func(bf, hf function) bool {
			return bf.file == hf.file && baseLeft[bf.file] == 1 && headLeft[bf.file] == 1
		})
	}
	m.pair(sameSource)
	return m.partner
}

// matching is the state of matchRepeated: base[i] is paired with
// head[partner[i]], and taken[j] tells whether head[j] is paired.
type matching struct {
	base, head []function
	partner    []int
	taken      []bool
}

// pair pairs each unpaired declaration of the base with the first unpaired
// declaration of the head that match accepts.
func (m *matching) pair(match func(b, h function) bool) {
	for i, b := range m.base {
		if m.partner[i] >= 0 {
			continue
		}
		for j, h := range m.head {
			if !m.taken[j] && match(b, h) {
				m.partner[i], m.taken[j] = j, true
				break
			}
		}
	}
}

// unpairedByFile counts the unpaired declarations of each file, in the base
// and in the head.
func (m *matching) unpairedByFile() (base, head map[string]int) {
	base, head = map[string]int{}, map[string]int{}
	for i, b := range m.base {
		if m.partner[i] < 0 {
			base[b.file]++
		}
	}
	for j, h := range m.head {
		if !m.taken[j] {
			head[h.file]++
		}
	}
	return base, head
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
			key:      path.Dir(name) + "." + funcName(fd),
			file:     name,
			line:     start.Line,
			score:    gocognit.ScanComplexityWithRecursion(fd, recursive[fd], false).Complexity,
			src:      bytes.Clone(src[start.Offset:end.Offset]),
			nameless: fd.Name.Name == "_" || (fd.Recv == nil && fd.Name.Name == "init"),
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
