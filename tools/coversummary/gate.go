// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// config is .coverage.json.
type config struct {
	// DiffMin is the minimum covered share, in percent, of the statements a
	// change touches.
	DiffMin float64 `json:"diff_min"`
	// TotalTarget is the minimum coverage, in percent, of all the statements
	// outside the exclusions.
	TotalTarget float64 `json:"total_target"`
	// PackageMin is the coverage, in percent, each package outside the
	// exclusions must reach. While it is null, the summary lists every
	// package and its distance to total_target, but nothing fails.
	PackageMin *float64 `json:"package_min"`
	// Exclude lists module-relative paths left out of every rule: a
	// directory when it ends with "/", a file otherwise.
	Exclude []string `json:"exclude"`
}

// loadConfig reads and checks the configuration at path. A field config does
// not know is an error, so that a misspelt or obsolete setting fails instead
// of being silently ignored.
func loadConfig(path string) (config, error) {
	var cfg config
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return cfg, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	var problems []string
	for name, v := range map[string]float64{"diff_min": cfg.DiffMin, "total_target": cfg.TotalTarget} {
		if v <= 0 || v > 100 {
			problems = append(problems, fmt.Sprintf("%s must be in (0, 100], got %v", name, v))
		}
	}
	if cfg.PackageMin != nil && (*cfg.PackageMin <= 0 || *cfg.PackageMin > 100) {
		problems = append(problems, fmt.Sprintf("package_min must be null or in (0, 100], got %v", *cfg.PackageMin))
	}
	for _, e := range cfg.Exclude {
		if e == "" || strings.HasPrefix(e, "/") || strings.HasPrefix(e, "./") || slices.Contains(strings.Split(e, "/"), "..") {
			problems = append(problems, fmt.Sprintf("exclude entry %q is not a clean module-relative path", e))
		}
	}
	if len(problems) > 0 {
		slices.Sort(problems)
		return cfg, fmt.Errorf("%s: %s", path, strings.Join(problems, "; "))
	}
	return cfg, nil
}

// excluded reports whether a module-relative path is left out of the gate.
func (c config) excluded(name string) bool {
	for _, e := range c.Exclude {
		if strings.HasSuffix(e, "/") && strings.HasPrefix(name, e) || name == e {
			return true
		}
	}
	return false
}

// lineRange is an inclusive range of line numbers.
type lineRange struct{ from, to int }

// parseDiff reads the output of git diff --unified=0 and returns, per file
// of the new tree, the line ranges the change adds or modifies. Deleted
// files and pure deletions add nothing.
func parseDiff(r io.Reader) (map[string][]lineRange, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	p := diffParser{changes: map[string][]lineRange{}}
	line := 0
	for scanner.Scan() {
		line++
		text := scanner.Text()
		if p.hunk.open() {
			if !p.hunk.consume(text) {
				return nil, fmt.Errorf("line %d: hunk ends early at %q", line, text)
			}
			continue
		}
		if err := p.header(text); err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if p.hunk.open() {
		return nil, errors.New("the diff ends inside a hunk")
	}
	return p.changes, nil
}

// diffParser is the state of parseDiff between two lines.
type diffParser struct {
	changes map[string][]lineRange
	file    string     // the current file in the new tree, "" when none
	hunk    hunkCounts // content lines the current hunk still expects
}

// header reads a line outside a hunk: the start of a file, its new path, or
// a hunk header. Other lines (index, mode, the old path) say nothing the
// rule needs.
func (p *diffParser) header(text string) error {
	switch {
	case strings.HasPrefix(text, "diff --git "):
		p.file = ""
	case strings.HasPrefix(text, "+++ "):
		name, err := diffPath(strings.TrimPrefix(text, "+++ "))
		if err != nil {
			return err
		}
		p.file = name
	case strings.HasPrefix(text, "@@ "):
		oldCount, newStart, newCount, err := hunkHeader(text)
		if err != nil {
			return err
		}
		p.hunk = hunkCounts{oldLines: oldCount, newLines: newCount}
		if p.file != "" && newCount > 0 {
			p.changes[p.file] = append(p.changes[p.file], lineRange{newStart, newStart + newCount - 1})
		}
	}
	return nil
}

// hunkCounts counts the content lines a hunk still expects on each side.
type hunkCounts struct{ oldLines, newLines int }

// open reports whether the hunk still expects content lines.
func (h hunkCounts) open() bool { return h.oldLines > 0 || h.newLines > 0 }

// consume counts one content line of the hunk. It reports false for a line
// that cannot be one, which means the hunk ended early.
func (h *hunkCounts) consume(text string) bool {
	switch {
	case strings.HasPrefix(text, "-"):
		h.oldLines--
	case strings.HasPrefix(text, "+"):
		h.newLines--
	case strings.HasPrefix(text, " "):
		h.oldLines--
		h.newLines--
	case strings.HasPrefix(text, `\`): // "\ No newline at end of file"
	default:
		return false
	}
	return true
}

// diffPath returns the path of a "+++ b/path" line, "" for /dev/null. Git
// ends a path holding a space with a tab, and quotes one holding a control or
// non-ASCII character, with C escapes ("b/\303\251.go").
func diffPath(s string) (string, error) {
	s = strings.TrimSuffix(s, "\t")
	if strings.HasPrefix(s, `"`) {
		unquoted, err := strconv.Unquote(s)
		if err != nil {
			return "", fmt.Errorf("quoted path %s: %w", s, err)
		}
		s = unquoted
	}
	if s == "/dev/null" {
		return "", nil
	}
	if !strings.HasPrefix(s, "b/") {
		return "", fmt.Errorf("new path %q does not start with b/", s)
	}
	return strings.TrimPrefix(s, "b/"), nil
}

// hunkHeader parses "@@ -a[,b] +c[,d] @@ ..." into b, c and d.
func hunkHeader(s string) (oldCount, newStart, newCount int, err error) {
	fields := strings.Fields(s)
	if len(fields) < 4 || fields[3] != "@@" || !strings.HasPrefix(fields[1], "-") || !strings.HasPrefix(fields[2], "+") {
		return 0, 0, 0, fmt.Errorf("malformed hunk header %q", s)
	}
	if _, oldCount, err = hunkRange(fields[1][1:]); err != nil {
		return 0, 0, 0, fmt.Errorf("hunk header %q: %w", s, err)
	}
	if newStart, newCount, err = hunkRange(fields[2][1:]); err != nil {
		return 0, 0, 0, fmt.Errorf("hunk header %q: %w", s, err)
	}
	return oldCount, newStart, newCount, nil
}

// hunkRange parses one side of a hunk header, "start[,count]"; a missing
// count is 1.
func hunkRange(s string) (start, count int, err error) {
	startText, countText, hasCount := strings.Cut(s, ",")
	if start, err = strconv.Atoi(startText); err != nil {
		return 0, 0, err
	}
	count = 1
	if hasCount {
		if count, err = strconv.Atoi(countText); err != nil {
			return 0, 0, err
		}
	}
	return start, count, nil
}

// gateInput is what the rules read besides the profile.
type gateInput struct {
	config  config
	changes map[string][]lineRange // nil when no diff was given
}

// changedFile is the diff rule's view of one changed file.
type changedFile struct {
	name             string
	changed, covered int
	uncovered        []lineRange // blocks touched by the change that no test ran
}

// gateResult is what evaluate measured, from which the verdicts and the
// report follow.
type gateResult struct {
	in gateInput

	total, totalCovered int               // statements outside the exclusions
	packages            []packageCoverage // least covered first

	diffChecked              bool
	diffChanged, diffCovered int
	diffFiles                []changedFile
	diffIgnored              int // changed Go files out of the rule: tests and exclusions
}

// totalPercent is the coverage of the statements outside the exclusions.
func (r gateResult) totalPercent() float64 { return percent(r.totalCovered, r.total) }

// diffPercent is the covered share of the statements the change touches.
func (r gateResult) diffPercent() float64 { return percent(r.diffCovered, r.diffChanged) }

// diffOK is the diff rule. It holds when no diff was given or when the
// change touches no statement, since there is then nothing to measure.
func (r gateResult) diffOK() bool {
	return !r.diffChecked || r.diffChanged == 0 || r.diffPercent() >= r.in.config.DiffMin
}

// totalOK is the total rule. It needs no slack for float error: percent
// rounds the exact share once, as decoding total_target rounds the decimal,
// so a total exactly at the target is equal to it.
func (r gateResult) totalOK() bool { return r.totalPercent() >= r.in.config.TotalTarget }

// passed reports whether every rule holds.
func (r gateResult) passed() bool { return r.diffOK() && r.totalOK() && r.packagesOK() }

// evaluate applies the rules.
func evaluate(in gateInput, files map[string]*fileCoverage, trim string) gateResult {
	cfg := in.config
	r := gateResult{in: in}
	for _, fc := range files {
		if cfg.excluded(strings.TrimPrefix(fc.Name, trim)) {
			continue
		}
		r.total += fc.Total
		r.totalCovered += fc.Covered
	}
	r.packages = packagesOf(files, trim, cfg.excluded)
	if in.changes != nil {
		r.evaluateDiff(files, trim)
	}
	return r
}

// evaluateDiff applies the diff rule to the changed Go files, tests and
// exclusions aside, in path order.
func (r *gateResult) evaluateDiff(files map[string]*fileCoverage, trim string) {
	r.diffChecked = true
	for _, name := range slices.Sorted(maps.Keys(r.in.changes)) {
		if !strings.HasSuffix(name, ".go") {
			continue
		}
		if strings.HasSuffix(name, "_test.go") || r.in.config.excluded(name) {
			r.diffIgnored++
			continue
		}
		cf := changedBlocks(name, files[trim+name], r.in.changes[name])
		if cf.changed == 0 {
			continue
		}
		r.diffChanged += cf.changed
		r.diffCovered += cf.covered
		r.diffFiles = append(r.diffFiles, cf)
	}
}

// changedBlocks counts the statements of the blocks of fc that the changed
// ranges touch, listing the uncovered ones by line. fc is nil for a file the
// profile does not list, which has no statement.
func changedBlocks(name string, fc *fileCoverage, ranges []lineRange) changedFile {
	cf := changedFile{name: name}
	if fc == nil {
		return cf
	}
	for _, b := range fc.counts {
		if !touches(ranges, b) {
			continue
		}
		cf.changed += b.stmts
		if b.covered {
			cf.covered += b.stmts
		} else {
			cf.uncovered = append(cf.uncovered, lineRange{b.startLine, b.endLine})
		}
	}
	slices.SortFunc(cf.uncovered, func(a, b lineRange) int { return a.from - b.from })
	return cf
}

// touches reports whether one of the changed ranges overlaps the block.
func touches(ranges []lineRange, b blockCount) bool {
	for _, rg := range ranges {
		if rg.from <= b.endLine && b.startLine <= rg.to {
			return true
		}
	}
	return false
}

// verdict is the Result cell of a rule that holds or not.
func verdict(ok bool) string {
	if ok {
		return "pass"
	}
	return "**FAIL**"
}

// writeGate prints the rules, their verdicts and what the author can do.
func writeGate(w io.Writer, r gateResult) error {
	cfg := r.in.config
	var b strings.Builder
	writeRules(&b, r)
	writeTotalAdvice(&b, r)
	writeChangedFiles(&b, r)
	writePackages(&b, r)

	excluded := "nothing"
	if len(cfg.Exclude) > 0 {
		excluded = "`" + strings.Join(cfg.Exclude, "`, `") + "`"
	}
	fmt.Fprintf(&b, "Excluded from every rule: %s.\n\n", excluded)
	_, err := io.WriteString(w, b.String())
	return err
}

// writeRules prints the verdict and the table of the rules.
func writeRules(b *strings.Builder, r gateResult) {
	cfg := r.in.config
	b.WriteString("## Coverage gate\n\n")
	if r.passed() {
		b.WriteString("All rules pass.\n\n")
	} else {
		b.WriteString("**The gate fails**: see the rules marked FAIL.\n\n")
	}
	b.WriteString("| Rule | Measured | Required | Result |\n|---|---|---|---|\n")

	switch {
	case !r.diffChecked:
		fmt.Fprintf(b, "| Diff: covered share of the statements the change touches | not measured (no diff given) | at least %g%% | skipped |\n", cfg.DiffMin)
	case r.diffChanged == 0:
		fmt.Fprintf(b, "| Diff: covered share of the statements the change touches | no executable statement changed | at least %g%% | pass |\n", cfg.DiffMin)
	default:
		fmt.Fprintf(b, "| Diff: covered share of the statements the change touches | %.1f%% (%d of %d) | at least %g%% | %s |\n",
			r.diffPercent(), r.diffCovered, r.diffChanged, cfg.DiffMin, verdict(r.diffOK()))
	}

	// Two decimals where the other rows have one: a change moves the total by
	// hundredths of a point, and near the target they decide the verdict.
	fmt.Fprintf(b, "| Total: coverage outside the exclusions | %.2f%% (%d of %d) | at least %g%% | %s |\n",
		r.totalPercent(), r.totalCovered, r.total, cfg.TotalTarget, verdict(r.totalOK()))
	writePackageRule(b, r)
	b.WriteString("\n")
}

// writeTotalAdvice says what to do when the total rule fails.
func writeTotalAdvice(b *strings.Builder, r gateResult) {
	if r.totalOK() {
		return
	}
	fmt.Fprintf(b, "The total is under the %g%% of `total_target`: add tests, starting with the least covered packages and files, which the tables below list first.\n\n", r.in.config.TotalTarget)
}

// writeChangedFiles lists the changed files of the diff rule, with the lines
// of their uncovered blocks.
func writeChangedFiles(b *strings.Builder, r gateResult) {
	if len(r.diffFiles) == 0 {
		return
	}
	b.WriteString("### Statements the change touches\n\nA coverage block the diff touches counts with all its statements. Test files and the exclusions of `.coverage.json` are left out")
	if r.diffIgnored > 0 {
		fmt.Fprintf(b, " (%d changed Go files)", r.diffIgnored)
	}
	b.WriteString(".\n\n| File | Changed statements | Covered | Not covered (lines) |\n|---|---:|---:|---|\n")
	for _, f := range r.diffFiles {
		lines := make([]string, 0, len(f.uncovered))
		for _, rg := range f.uncovered {
			lines = append(lines, rg.String())
		}
		fmt.Fprintf(b, "| `%s` | %d | %d | %s |\n", f.name, f.changed, f.covered, strings.Join(lines, ", "))
	}
	b.WriteString("\n")
}

// String returns "7" for a single line, "7-9" for a range.
func (rg lineRange) String() string {
	if rg.from == rg.to {
		return strconv.Itoa(rg.from)
	}
	return fmt.Sprintf("%d-%d", rg.from, rg.to)
}
