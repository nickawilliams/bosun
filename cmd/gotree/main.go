// gotree renders `go test -json` output as a hierarchical tree.
//
// Reads test event JSON from stdin (or a file with -in) and prints
// one ui.Tree per package after EOF. Each leaf is a test case
// (TestName, TestName/sub, TestName/cat/scenario, ...) with a
// pass/fail/skip glyph and elapsed time. Parent nodes aggregate
// status from their children: any fail → fail, all skip → skip,
// otherwise pass.
//
// Usage:
//
//	go test -json ./... | go run ./cmd/gotree
//	go test -json ./... > out.json && go run ./cmd/gotree -in out.json
//
// Exit code is 0 when every test passed, 1 when any test failed or
// any package didn't compile.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"image/color"
	"io"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/nickawilliams/bosun/internal/ui"
)

// event is the subset of go test -json fields gotree consumes.
// Reference: https://pkg.go.dev/cmd/test2json
type event struct {
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Elapsed float64 `json:"Elapsed"`
	Output  string  `json:"Output"`
}

// node is one entry in the test hierarchy: either a top-level test,
// a subtest, or a synthetic package root. Children are kept in
// insertion order so the tree mirrors source order rather than
// alphabetical.
type node struct {
	label    string
	status   string // "pass", "fail", "skip", or "" when running/unknown
	elapsed  float64
	coverage string // "88.0%" when go test -cover was used; package-level only
	children map[string]*node
	order    []string
}

// coverageLine matches the per-package coverage summary `go test -cover`
// emits as an output event, e.g. "coverage: 88.0% of statements".
var coverageLine = regexp.MustCompile(`coverage: ([\d.]+)% of statements`)

// profileDriven is set when -coverprofile is supplied. Rendering paths
// consult it to decide between "show 0.0% as the placeholder for
// packages with no tests" (default mode) and "leave per-pkg coverage
// empty until the profile is parsed" (profile mode — accurate numbers
// land on the batch render or in the overall summary line).
var profileDriven bool

// valueColumn is the 1-indexed terminal column where leaf elapsed
// times and package coverage stats start. Picked to leave plenty of
// room for nested subtest names while keeping the right-hand stats
// readable as their own column.
const valueColumn = 80

// valueWidth is the fixed character width values are padded into so
// elapsed times and coverage percentages right-align (units line up
// vertically at the rightmost position). "100.0%" and "1234ms" both
// fit; anything wider just pushes past it without truncation.
const valueWidth = 6

// Thresholds for color-coding the elapsed-time column. Sub-second
// tests are quiet (muted); anything past a second gets warning
// (yellow) and >3s gets error (red) so slow tests stand out.
const (
	slowTestSeconds     = 1.0
	verySlowTestSeconds = 3.0
)

// Coverage thresholds match common industry conventions: 80% is the
// "good" line, 50% is the "needs attention" line, below is "at risk".
const (
	goodCoveragePct = 80.0
	weakCoveragePct = 50.0
)

// elapsedColor maps a test's wall-clock seconds to a palette color.
func elapsedColor(seconds float64) color.Color {
	switch {
	case seconds >= verySlowTestSeconds:
		return ui.Palette.Error
	case seconds >= slowTestSeconds:
		return ui.Palette.Warning
	default:
		return ui.Palette.Muted
	}
}

// coverageColor maps a "XX.X%" string to a palette color. Returns
// muted on parse failure so a malformed value can't crash the render.
func coverageColor(pct string) color.Color {
	n, err := strconv.ParseFloat(strings.TrimSuffix(pct, "%"), 64)
	if err != nil {
		return ui.Palette.Muted
	}
	switch {
	case n >= goodCoveragePct:
		return ui.Palette.Success
	case n >= weakCoveragePct:
		return ui.Palette.Warning
	default:
		return ui.Palette.Error
	}
}

func (n *node) child(seg string) *node {
	if n.children == nil {
		n.children = map[string]*node{}
	}
	if c, ok := n.children[seg]; ok {
		return c
	}
	c := &node{label: seg}
	n.children[seg] = c
	n.order = append(n.order, seg)
	return c
}

// aggregateStatus returns this node's effective status. Leaves
// return their own status verbatim; groups derive theirs from
// children: any fail wins, otherwise any pass wins, else skip.
func (n *node) aggregateStatus() string {
	if len(n.order) == 0 {
		return n.status
	}
	var anyFail, anyPass bool
	allSkip := true
	for _, key := range n.order {
		switch n.children[key].aggregateStatus() {
		case "fail":
			anyFail = true
			allSkip = false
		case "pass":
			anyPass = true
			allSkip = false
		case "skip":
			// keep allSkip
		default:
			allSkip = false
		}
	}
	switch {
	case anyFail:
		return "fail"
	case anyPass:
		return "pass"
	case allSkip:
		return "skip"
	default:
		return ""
	}
}

func main() {
	in := flag.String("in", "", "read events from file instead of stdin")
	stream := flag.Bool("stream", false, "render each package as soon as it finishes (parallel test runs render in completion order, not source order)")
	coverprofile := flag.String("coverprofile", "", "read per-package coverage from this Go cover profile (overrides inline coverage events; produces meaningful cross-package attribution when paired with -coverpkg)")
	flag.Parse()
	profileDriven = *coverprofile != ""

	var r io.Reader = os.Stdin
	if *in != "" {
		f, err := os.Open(*in)
		if err != nil {
			fmt.Fprintln(os.Stderr, "gotree:", err)
			os.Exit(2)
		}
		defer func() { _ = f.Close() }()
		r = f
	}

	packages := map[string]*node{}
	var pkgOrder []string

	// Shared rendering state used by both stream and batch paths so
	// the blank-line-between-packages logic and exit-code accounting
	// have one source of truth.
	anyFail := false
	streamedFirst := false
	emit := func(name string, pkg *node) {
		if streamedFirst {
			fmt.Println()
			ui.ClearSpacer()
		}
		streamedFirst = true
		if pkg.aggregateStatus() == "fail" || pkg.status == "fail" {
			anyFail = true
		}
		if len(pkg.order) == 0 {
			renderEmptyPackage(name, pkg)
			return
		}
		renderPackage(name, pkg)
	}

	sc := bufio.NewScanner(r)
	// Test output lines can be large; lift the default 64KiB cap.
	sc.Buffer(make([]byte, 0, 1<<16), 1<<22)
	for sc.Scan() {
		var e event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			// Non-JSON line — likely a stray writer to stdout (go test
			// pre-test build output, etc.). Pass through to stderr so
			// users see compile errors and the like.
			fmt.Fprintln(os.Stderr, sc.Text())
			continue
		}
		if e.Package == "" {
			continue
		}
		pkg, ok := packages[e.Package]
		if !ok {
			pkg = &node{label: e.Package}
			packages[e.Package] = pkg
			pkgOrder = append(pkgOrder, e.Package)
		}
		if e.Test == "" {
			// Package-level event: pass/fail/skip apply to the whole pkg;
			// output events can carry the `coverage: X% of statements`
			// line that go test -cover prints once per package. The
			// coverage line arrives *before* the terminal pass/fail
			// event, so by the time we render in stream mode the
			// coverage field is already populated.
			if e.Action == "output" && *coverprofile == "" {
				// Inline coverage events only carry per-package numbers
				// when -coverpkg isn't set; with a profile to read, the
				// inline number is the misleading cross-pkg aggregate.
				if m := coverageLine.FindStringSubmatch(e.Output); m != nil {
					pkg.coverage = m[1] + "%"
				}
			}
			if isTerminalAction(e.Action) {
				pkg.status = e.Action
				pkg.elapsed = e.Elapsed
				if *stream {
					emit(e.Package, pkg)
				}
			}
			continue
		}
		// Walk the slash-delimited subtest path.
		n := pkg
		for seg := range strings.SplitSeq(e.Test, "/") {
			n = n.child(seg)
		}
		if isTerminalAction(e.Action) {
			n.status = e.Action
			n.elapsed = e.Elapsed
		}
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "gotree: scan:", err)
		os.Exit(2)
	}

	// Profile post-process: override pkg.coverage with the
	// profile-derived per-package numbers (which correctly attribute
	// cross-package test usage), and compute the overall percentage
	// to print as a final summary line. Done before the batch render
	// so per-pkg numbers land in the rendered tree.
	var overallCoverage string
	if *coverprofile != "" {
		perPkg, overall, perr := parseCoverProfile(*coverprofile)
		if perr != nil {
			fmt.Fprintln(os.Stderr, "gotree: read coverage:", perr)
		} else {
			overallCoverage = overall
			for name, cov := range perPkg {
				if pkg, ok := packages[name]; ok {
					pkg.coverage = cov
				}
			}
		}
	}

	// Batch render: replay packages in source order. Skipped in
	// stream mode since each package already rendered as it completed.
	if !*stream {
		for _, name := range pkgOrder {
			emit(name, packages[name])
		}
	}

	if overallCoverage != "" {
		renderOverallCoverage(overallCoverage)
	}

	if anyFail {
		os.Exit(1)
	}
}

// isTerminalAction reports whether an event finalizes a test or
// package. Other actions (run, output, pause, cont, bench) don't
// change the recorded status.
func isTerminalAction(a string) bool {
	return a == "pass" || a == "fail" || a == "skip"
}

// renderPackage prints a Card header for the package and a tree of
// its tests below.
func renderPackage(name string, pkg *node) {
	// Hand-rolled header (rather than ui.Card.Print) so we can stamp
	// the coverage stat next to the name without the `title:` colon
	// Card.Value would force, and pad it to valueColumn so it lines
	// up with the tree's leaf elapsed-time column below.
	glyph, col := statusGlyph(pkg.aggregateStatus())
	glyphStyled := lipgloss.NewStyle().Foreground(col).Render(glyph)
	nameStyled := lipgloss.NewStyle().Bold(true).Foreground(ui.Palette.Primary).Render(name)
	fmt.Println(" " + glyphStyled + "  " + nameStyled + formatCoverage(pkg.coverage, name))

	// Suppress the leading │ the tree's spacerPrefix would emit —
	// the header line above is already the package's "card row".
	ui.ClearSpacer()
	ui.NewTree().ValueColumn(valueColumn).Add(toTreeNodes(pkg)...).Print()
}

// renderEmptyPackage prints a package with no matching tests as
// two lines: glyph + name + coverage%, then an indented "no tests".
// Coverage comes from pkg.coverage when set (e.g. profile-derived
// cross-package coverage); otherwise defaults to "0.0%". Hand-rolled
// to match Card's column alignment without the timeline connector
// the Card body path would otherwise draw — there's no tree below
// for the │ to lead into.
func renderEmptyPackage(name string, pkg *node) {
	// Always render with the warning glyph regardless of go test's
	// reported status: uncovered packages are something the developer
	// might want to act on, and the louder signal makes them easier
	// to scan for than the muted dash a "skip" status would produce.
	glyphStyled := lipgloss.NewStyle().Foreground(ui.Palette.Warning).Render("▲")
	nameStyled := lipgloss.NewStyle().Bold(true).Foreground(ui.Palette.Primary).Render(name)
	mutedStyled := lipgloss.NewStyle().Foreground(ui.Palette.Muted).Render("no tests")
	// Default to "0.0%" only when not using a coverage profile —
	// otherwise the profile will supply (or has supplied) the real
	// cross-package number, and showing a placeholder would be wrong.
	cov := pkg.coverage
	if cov == "" && !profileDriven {
		cov = "0.0%"
	}
	fmt.Println(" " + glyphStyled + "  " + nameStyled + formatCoverage(cov, name))
	fmt.Println("    " + mutedStyled)
}

// formatCoverage returns a padded + styled coverage stat suffix for
// a package header line, or "" when coverage is empty. The padding
// pushes the stat to valueColumn so it lines up with the tree's
// elapsed-time column; the style is bold + tier-colored to match
// the bold package name beside it.
func formatCoverage(coverage, name string) string {
	if coverage == "" {
		return ""
	}
	// Visible chars so far in the header: 1 space + 1 glyph + 2 spaces + name.
	// Pad with at least 2 spaces so long package names don't crowd
	// the coverage stat when they push past valueColumn.
	pad := max(valueColumn-1-(4+len(name)), 2)
	styled := lipgloss.NewStyle().
		Bold(true).
		Foreground(coverageColor(coverage)).
		Render(fmt.Sprintf("%*s", valueWidth, coverage))
	return strings.Repeat(" ", pad) + styled
}

// toTreeNodes converts a node's children into the ui.TreeNode shape
// expected by ui.Tree. Leaf tests show their elapsed time as the
// value; group nodes carry the aggregate-status glyph.
func toTreeNodes(n *node) []*ui.TreeNode {
	out := make([]*ui.TreeNode, 0, len(n.order))
	for _, key := range n.order {
		c := n.children[key]
		glyph, col := statusGlyph(c.aggregateStatus())
		if len(c.order) == 0 {
			leaf := ui.Leaf(glyph, col, c.label, formatElapsed(c.elapsed))
			leaf.ValueColor = elapsedColor(c.elapsed)
			out = append(out, leaf)
			continue
		}
		tn := ui.Group(c.label, toTreeNodes(c)...)
		tn.Glyph = glyph
		tn.GlyphColor = col
		out = append(out, tn)
	}
	return out
}

// statusGlyph maps a test status to a glyph + color drawn from the
// shared palette. Unknown status falls back to a muted dot.
func statusGlyph(s string) (string, color.Color) {
	switch s {
	case "pass":
		return ui.Palette.Check, ui.Palette.Success
	case "fail":
		return ui.Palette.Cross, ui.Palette.Error
	case "skip":
		return "–", ui.Palette.Muted
	default:
		return ui.Palette.Dot, ui.Palette.Muted
	}
}

// parseCoverProfile reads a Go cover profile (written by go test
// -coverprofile=...) and returns per-package coverage stamps plus the
// overall percentage. Package keys are import paths (e.g.
// "github.com/owner/proj/internal/cli"). Statements from blocks with
// count > 0 are treated as covered.
//
// Blocks are deduped by file:range key because with `-coverpkg=./...`
// each per-package test invocation emits its own (often identical)
// block list into the merged profile; counting them naively inflates
// totals by the number of test packages. We OR-merge the hit bit so
// any test exercising a block marks it covered.
func parseCoverProfile(profilePath string) (perPkg map[string]string, overall string, err error) {
	f, openErr := os.Open(profilePath)
	if openErr != nil {
		return nil, "", openErr
	}
	defer func() { _ = f.Close() }()

	type block struct {
		pkg     string
		numStmt int
		hit     bool
	}
	blocks := map[string]*block{}

	sc := bufio.NewScanner(f)
	first := true
	for sc.Scan() {
		line := sc.Text()
		if first {
			first = false
			// Skip the leading "mode: set|count|atomic" header line.
			continue
		}
		// Block line format:
		//   <file>:<startLine>.<col>,<endLine>.<col> <numStmt> <count>
		// Split from the right since the file path is forward-slash
		// import-style and never contains spaces in practice.
		sp1 := strings.LastIndex(line, " ")
		if sp1 < 0 {
			continue
		}
		countStr := line[sp1+1:]
		rest := line[:sp1]
		sp2 := strings.LastIndex(rest, " ")
		if sp2 < 0 {
			continue
		}
		numStr := rest[sp2+1:]
		key := rest[:sp2] // "<file>:<startLine>.<col>,<endLine>.<col>"
		colon := strings.LastIndex(key, ":")
		if colon < 0 {
			continue
		}

		num, e1 := strconv.Atoi(numStr)
		cnt, e2 := strconv.Atoi(countStr)
		if e1 != nil || e2 != nil {
			continue
		}

		if b, ok := blocks[key]; ok {
			if cnt > 0 {
				b.hit = true
			}
			continue
		}
		blocks[key] = &block{
			pkg:     path.Dir(key[:colon]),
			numStmt: num,
			hit:     cnt > 0,
		}
	}
	if err := sc.Err(); err != nil {
		return nil, "", err
	}

	type stats struct{ total, covered int }
	pkgStats := map[string]*stats{}
	var totalAll, coveredAll int
	for _, b := range blocks {
		s, ok := pkgStats[b.pkg]
		if !ok {
			s = &stats{}
			pkgStats[b.pkg] = s
		}
		s.total += b.numStmt
		totalAll += b.numStmt
		if b.hit {
			s.covered += b.numStmt
			coveredAll += b.numStmt
		}
	}

	perPkg = make(map[string]string, len(pkgStats))
	for pkg, s := range pkgStats {
		perPkg[pkg] = formatPercent(s.covered, s.total)
	}
	if totalAll > 0 {
		overall = formatPercent(coveredAll, totalAll)
	}
	return perPkg, overall, nil
}

// formatPercent returns "X.X%" for a covered/total ratio. Returns
// "0.0%" when total is 0 so empty packages render consistently.
func formatPercent(covered, total int) string {
	if total == 0 {
		return "0.0%"
	}
	return fmt.Sprintf("%.1f%%", float64(covered)/float64(total)*100)
}

// renderOverallCoverage prints a summary line below the package
// trees: a muted "overall:" label on the left and the right-aligned,
// bold + tier-colored total on the right. Matches the per-package
// coverage column alignment for visual continuity.
func renderOverallCoverage(pct string) {
	fmt.Println()
	label := lipgloss.NewStyle().Foreground(ui.Palette.Muted).Render("overall:")
	// Visible chars so far: 1 space + len("overall:") = 9.
	pad := max(valueColumn-1-(1+len("overall:")), 2)
	value := fmt.Sprintf("%*s", valueWidth, pct)
	styled := lipgloss.NewStyle().Bold(true).Foreground(coverageColor(pct)).Render(value)
	fmt.Println(" " + label + strings.Repeat(" ", pad) + styled)
}

// formatElapsed renders the elapsed time, right-aligned in valueWidth
// columns so all leaf times line up at their unit suffix. Tests under
// a second show milliseconds; longer ones show seconds with two
// decimals.
func formatElapsed(s float64) string {
	var raw string
	if s < 1 {
		raw = fmt.Sprintf("%dms", int(s*1000))
	} else {
		raw = fmt.Sprintf("%.2fs", s)
	}
	return fmt.Sprintf("%*s", valueWidth, raw)
}
