// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Command coversummary turns a Go coverage profile into a Markdown summary
// and, with -config, enforces the coverage gate of the CI coverage job.
//
// The summary is the total, then one row per file matching -match (by default
// every resource and data source file), least covered first.
//
// The gate (gate.go) reads its thresholds and exclusions from .coverage.json.
// It has three rules, each against a fixed threshold:
//
//   - diff: at least diff_min % of the executable statements a change touches
//     (non-test Go files, outside the exclusions) are covered; it needs the
//     unified diff of the change without context lines (-diff), which `make
//     cover` and the CI coverage job write with flags that fix its format
//     whatever the git configuration;
//   - total: the total outside the exclusions is at total_target % or more;
//   - packages: every package outside the exclusions is at package_min % or
//     more. While package_min is null, the rule is not enforced: the summary
//     still lists every package with its distance to total_target, least
//     covered first.
//
// No rule compares the total with a previous one. Near full coverage, such a
// ratchet failed changes on statements no test can reach (the framework's
// defensive returns), and pushed authors to write tests for the number; the
// diff rule already refuses untested new code, and the packages rule catches
// a real drop.
//
// It prints the summary on stdout and exits 1 when a rule fails, 2 on a usage
// or input error.
//
//	make cover COVER_BASE=origin/main
//
// runs the tests, then the git diff of GNUmakefile's cover target and this
// command:
//
//	go run -C tools ./coversummary -config ../.coverage.json -diff ../coverage-diff.out ../coverage.out
//
// It uses the standard library only, so it adds nothing to the tools module.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
)

// errGateFailed reports that a rule of the gate failed; the summary says
// which.
var errGateFailed = errors.New("coverage gate failed")

// options are the flags of the command, as run reads them.
type options struct {
	match      *regexp.Regexp
	trim       string
	configPath string
	diffPath   string
}

// main parses the flags, calls run and turns its error into the exit status.
func main() {
	match := flag.String("match", `_(resource|data_source)\.go$`, "regular expression selecting the files listed one by one")
	trim := flag.String("trim", "github.com/ippontech/terraform-provider-mistral/", "module path prefix removed from the file paths of the profile")
	configPath := flag.String("config", "", "coverage gate configuration (.coverage.json); without it, only the summary is printed")
	diffPath := flag.String("diff", "", "git diff of <base>...HEAD for the diff rule, as the cover target of GNUmakefile writes it; - for stdin")
	flag.Usage = func() {
		_, _ = fmt.Fprintln(flag.CommandLine.Output(), "usage: coversummary [-match regexp] [-trim prefix] [-config .coverage.json [-diff file]] coverage.out")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}
	re, err := regexp.Compile(*match)
	if err != nil {
		fmt.Fprintf(os.Stderr, "coversummary: invalid -match: %v\n", err)
		os.Exit(2)
	}
	opts := options{match: re, trim: *trim, configPath: *configPath, diffPath: *diffPath}
	switch err := run(os.Stdout, os.Stdin, flag.Arg(0), opts); {
	case errors.Is(err, errGateFailed):
		fmt.Fprintf(os.Stderr, "coversummary: %v (details in the summary)\n", err)
		os.Exit(1)
	case err != nil:
		fmt.Fprintf(os.Stderr, "coversummary: %v\n", err)
		os.Exit(2)
	}
}

// run reads the profile at profilePath and writes the summary to w. With a
// configuration, it applies the gate first, reading the diff from stdin when
// diffPath is "-", writes the gate's report before the summary, and returns
// errGateFailed when a rule fails.
func run(w io.Writer, stdin io.Reader, profilePath string, opts options) error {
	f, err := os.Open(filepath.Clean(profilePath))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	files, err := parseProfile(f)
	if err != nil {
		return fmt.Errorf("%s: %w", profilePath, err)
	}
	if opts.configPath == "" {
		return writeSummary(w, files, opts.match, opts.trim, nil)
	}

	cfg, err := loadConfig(opts.configPath)
	if err != nil {
		return err
	}
	in := gateInput{config: cfg}
	if opts.diffPath != "" {
		r := stdin
		if opts.diffPath != "-" {
			df, err := os.Open(filepath.Clean(opts.diffPath))
			if err != nil {
				return err
			}
			defer func() { _ = df.Close() }()
			r = df
		}
		if in.changes, err = parseDiff(r); err != nil {
			return fmt.Errorf("%s: %w", opts.diffPath, err)
		}
	}

	result := evaluate(in, files, opts.trim)
	if err := writeGate(w, result); err != nil {
		return err
	}
	if err := writeSummary(w, files, opts.match, opts.trim, cfg.excluded); err != nil {
		return err
	}
	if !result.passed() {
		return errGateFailed
	}
	return nil
}
