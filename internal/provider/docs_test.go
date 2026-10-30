// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/ippontech/terraform-provider-mistral/internal/providerdata"
)

// The callouts a type's page carries, by its registry declarations. The
// Terraform Registry renders "~>" as a warning and "->" as a note.
const (
	notVerifiedBanner = "~> **Not verified against the live API.**"
	previewNote       = "-> **Public preview at Mistral.**"
)

// alphaNotice opens the index page while no registered type has passed an
// acceptance test: the provider was published as an alpha on that basis
// (RELEASE.md, Versions).
const alphaNotice = "~> **Alpha.**"

// subcategories are the Registry groups of CLAUDE.md, Naming and grouping.
var subcategories = map[string]providerdata.Audience{
	"Agents":                             providerdata.Workspace,
	"Libraries":                          providerdata.Workspace,
	"Connectors":                         providerdata.Workspace,
	"Prompts and Skills":                 providerdata.Workspace,
	"Service Accounts":                   providerdata.Workspace,
	"Models":                             providerdata.Workspace,
	"Admin API: Workspaces and API Keys": providerdata.Admin,
	"Admin API: Users and Groups":        providerdata.Admin,
	"Admin API: Billing":                 providerdata.Admin,
}

var subcategoryLine = regexp.MustCompile(`(?m)^subcategory: "([^"]*)"$`)

// page is what the docs of one registered type must agree with.
type page struct {
	name      string // mistral_model
	dir       string // resources or data-sources
	example   string // resource.tf or data-source.tf
	audience  providerdata.Audience
	stability stability
	live      liveCoverage
}

func pages[T any](t *testing.T, dir, example string, regs []registration[T]) []page {
	t.Helper()
	out := make([]page, 0, len(regs))
	for _, r := range regs {
		out = append(out, page{typeName(t, r.New()), dir, example, r.Audience, r.Stability, r.Live})
	}
	return out
}

// TestTypePagesMatchTheRegistry fails when a type's template or example is
// missing, or when its page says something its registration contradicts: the
// subcategory and the credential of its audience, the not-verified banner
// for a fakeOnly type, the preview note for a beta one.
func TestTypePagesMatchTheRegistry(t *testing.T) {
	all := append(
		pages(t, "resources", "resource.tf", resourceRegistrations()),
		pages(t, "data-sources", "data-source.tf", dataSourceRegistrations())...)
	for _, p := range all {
		t.Run(p.name, func(t *testing.T) { checkPage(t, p) })
	}
}

func checkPage(t *testing.T, p page) {
	t.Helper()
	short := strings.TrimPrefix(p.name, providerTypeName+"_")
	tmplPath := filepath.Join("..", "..", "templates", p.dir, short+".md.tmpl")
	raw, err := os.ReadFile(tmplPath)
	if err != nil {
		t.Fatalf("no template: %v", err)
	}
	tmpl := string(raw)

	m := subcategoryLine.FindStringSubmatch(tmpl)
	switch {
	case m == nil:
		t.Errorf("%s has no subcategory", tmplPath)
	case subcategories[m[1]] == 0:
		t.Errorf("%s: subcategory %q is not one of CLAUDE.md", tmplPath, m[1])
	case subcategories[m[1]] != p.audience:
		t.Errorf("%s: subcategory %q is for the %s credential, the registry declares %s", tmplPath, m[1], subcategories[m[1]], p.audience)
	}

	if credential := "`" + p.audience.String() + "`"; !strings.Contains(tmpl, credential) {
		t.Errorf("%s does not name the credential %s", tmplPath, credential)
	}
	if got, want := strings.Contains(tmpl, notVerifiedBanner), p.live == fakeOnly; got != want {
		t.Errorf("%s: not-verified banner present = %t, the registry declares %s", tmplPath, got, p.live)
	}
	if got, want := strings.Contains(tmpl, previewNote), p.stability == beta; got != want {
		t.Errorf("%s: preview note present = %t, the registry declares %s", tmplPath, got, p.stability)
	}

	example := filepath.ToSlash(filepath.Join("examples", p.dir, p.name, p.example))
	if !strings.Contains(tmpl, `{{ tffile "`+example+`" }}`) {
		t.Errorf("%s does not include %s", tmplPath, example)
	}
	for _, f := range []string{example, filepath.Join("examples", p.dir, p.name, "versions.tf")} {
		if _, err := os.Stat(filepath.Join("..", "..", f)); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
}

// TestNoOrphanPages fails on a template or an example directory that no
// registered type owns, as a rename leaves behind.
// TestIndexAlphaNoticeMatchesTheRegistry keeps the alpha notice of the index
// page while every type is fakeOnly, and fails once one type is
// acceptanceTested, so that the provider's status gets reworded then.
func TestIndexAlphaNoticeMatchesTheRegistry(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "templates", "index.md.tmpl"))
	if err != nil {
		t.Fatalf("no index template: %v", err)
	}
	all := append(
		pages(t, "resources", "", resourceRegistrations()),
		pages(t, "data-sources", "", dataSourceRegistrations())...)
	alpha := !slices.ContainsFunc(all, func(p page) bool { return p.live == acceptanceTested })
	if got := strings.Contains(string(raw), alphaNotice); got != alpha {
		t.Errorf("index alpha notice present = %t, but every type fakeOnly = %t: reword the provider's status in templates/index.md.tmpl and README.md", got, alpha)
	}
}

func TestNoOrphanPages(t *testing.T) {
	var names []string
	for _, p := range append(
		pages(t, "resources", "", resourceRegistrations()),
		pages(t, "data-sources", "", dataSourceRegistrations())...) {
		names = append(names, p.dir+"/"+p.name)
	}

	for _, dir := range []string{"resources", "data-sources"} {
		templates, _ := filepath.Glob(filepath.Join("..", "..", "templates", dir, "*.md.tmpl"))
		for _, f := range templates {
			name := dir + "/" + providerTypeName + "_" + strings.TrimSuffix(filepath.Base(f), ".md.tmpl")
			if !slices.Contains(names, name) {
				t.Errorf("%s belongs to no registered type", f)
			}
		}
		examples, _ := filepath.Glob(filepath.Join("..", "..", "examples", dir, "*"))
		for _, f := range examples {
			if name := dir + "/" + filepath.Base(f); !slices.Contains(names, name) {
				t.Errorf("%s belongs to no registered type", f)
			}
		}
	}
}
