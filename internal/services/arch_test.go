package services

import (
	"errors"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// This file guards the dependency direction the package doc promises:
// services is the only package that imports a provider adapter. It is
// the import-axis sibling of internal/cli's
// TestSchemaCarriesNoProviderSpecificKeys — that test keeps provider
// strings out of the schema; this one keeps provider imports out of
// every package but this one, whose registries construct them.
//
// The import axis is the one that regresses silently: a stray
// `import ".../internal/code/github"` in internal/cli compiles, passes
// tests, and reads as normal code. Only the import graph shows it.

// capabilityRoots are the capability packages of the provider
// architecture. A subpackage of one is a provider adapter
// (internal/issue/jira, internal/code/github, …), so the rules below
// cover future adapters without being edited.
var capabilityRoots = []string{
	"internal/issue",
	"internal/code",
	"internal/notify",
	"internal/cicd",
	"internal/preview",
}

// adapterImportExceptions are adapter imports that are deliberate,
// keyed by {importer, imported}. Named exactly — not pattern-matched —
// so that removing a coupling later fails this test and prompts
// deleting its entry here.
var adapterImportExceptions = map[[2]string]string{
	// GitHub Actions authenticates with the code host's credentials
	// rather than asking for a second token; documented on
	// githubactions.Descriptor.
	{"internal/cicd/githubactions", "internal/code/github"}: "shares the code host's token discovery",
}

// TestProviderImportBoundaries asserts the provider boundary rules over
// the module's import graph:
//
//  1. Only internal/services may import a provider adapter, so that the
//     CLI (and everything else) reaches providers through a capability
//     interface. The exceptions above are the complete list.
//  2. Adapters and capability packages must not import internal/cli —
//     dependencies point from the CLI down, never back up.
//  3. internal/provider is the leaf of the provider architecture and
//     imports nothing else internal.
//
// Two scope limits, both deliberate: test-only imports are not checked
// (the rules are about the production dependency direction, and go
// list's .Imports carries exactly that), and the graph is the host
// build context's, so an import behind another platform's build tag is
// not seen.
func TestProviderImportBoundaries(t *testing.T) {
	module := strings.TrimSpace(goList(t, "-m"))
	out := goList(t, "-f", "{{.ImportPath}}\t{{.Dir}}\t{{join .Imports \" \"}}", module+"/...")

	used := make(map[[2]string]bool)
	for line := range strings.Lines(strings.TrimSuffix(out, "\n")) {
		pkgPath, dir, imports, ok := splitListLine(strings.TrimSuffix(line, "\n"))
		if !ok {
			t.Fatalf("unexpected go list output line: %q", line)
		}
		rel := strings.TrimPrefix(pkgPath, module+"/")
		for imp := range strings.FieldsSeq(imports) {
			target, internal := strings.CutPrefix(imp, module+"/")
			if !internal {
				continue
			}
			pair := [2]string{rel, target}
			if _, ok := adapterImportExceptions[pair]; ok {
				used[pair] = true
			}
			if rule := boundaryViolation(rel, target); rule != "" {
				t.Errorf("%s imports %s\n  rule: %s",
					importSites(t, rel, dir, imp), target, rule)
			}
		}
	}

	// An exception that exempts nothing is stale: the coupling it
	// justified is gone, and leaving the entry would silently bless the
	// import if it ever came back.
	for pair := range adapterImportExceptions {
		if !used[pair] {
			t.Errorf("adapterImportExceptions names %s importing %s, but no such import exists; delete the entry",
				pair[0], pair[1])
		}
	}
}

// TestBoundaryViolationRules pins each rule's verdict against a table.
// Several of the violating imports can never appear in the real module
// graph — they would be import cycles and fail to compile — so this is
// the only place those branches are exercised.
func TestBoundaryViolationRules(t *testing.T) {
	cases := []struct {
		name        string
		rel, target string
		violates    bool
	}{
		{"services may import an adapter", "internal/services", "internal/issue/jira", false},
		{"named exception is allowed", "internal/cicd/githubactions", "internal/code/github", false},
		{"cli must not import an adapter", "internal/cli", "internal/code/github", true},
		{"adapters must not import each other", "internal/issue/jira", "internal/notify/slack", true},
		{"adapters must not import cli", "internal/issue/jira", "internal/cli", true},
		{"capabilities must not import cli", "internal/issue", "internal/cli", true},
		{"capabilities may import provider", "internal/issue", "internal/provider", false},
		{"provider must not import internal packages", "internal/provider", "internal/config", true},
		{"cli may import a capability", "internal/cli", "internal/issue", false},
		{"packages outside the architecture are unconstrained", "internal/ui", "internal/config", false},
		{"adapters must not import cli subpackages", "internal/issue/jira", "internal/cli/foo", true},
		{"provider may import within its own subtree", "internal/provider", "internal/provider/foo", false},
		{"provider subpackages are leaves too", "internal/provider/foo", "internal/config", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rule := boundaryViolation(tc.rel, tc.target)
			if got := rule != ""; got != tc.violates {
				t.Errorf("boundaryViolation(%q, %q) = %q; want violation: %v",
					tc.rel, tc.target, rule, tc.violates)
			}
		})
	}
}

// boundaryViolation returns the rule an import breaks, or "" when the
// import is allowed. rel and target are module-relative package paths.
func boundaryViolation(rel, target string) string {
	switch {
	case within(rel, "internal/provider") && !within(target, "internal/provider"):
		return "internal/provider is the leaf of the provider architecture; it imports nothing else internal"
	case isAdapter(target):
		if rel == "internal/services" {
			return ""
		}
		if _, ok := adapterImportExceptions[[2]string{rel, target}]; ok {
			return ""
		}
		return "only internal/services may import a provider adapter; ask for the capability interface instead (see the services package doc)"
	case within(target, "internal/cli") && (isAdapter(rel) || slices.Contains(capabilityRoots, rel)):
		return "provider packages must not import internal/cli; dependencies point from the CLI down"
	}
	return ""
}

// within reports whether the module-relative package path is root or a
// subpackage of it, so the rules survive internal/cli or
// internal/provider growing subpackages.
func within(rel, root string) bool {
	return rel == root || strings.HasPrefix(rel, root+"/")
}

// isAdapter reports whether the module-relative package path is a
// provider adapter — a subpackage of a capability root.
func isAdapter(rel string) bool {
	for _, root := range capabilityRoots {
		if strings.HasPrefix(rel, root+"/") {
			return true
		}
	}
	return false
}

// TestImportSitesNamesTheFile pins the failure-reporting path, which a
// clean module graph otherwise never runs: the file carrying the import
// is named, test files are ignored, and an import parsing can't locate
// falls back to the package path.
func TestImportSitesNamesTheFile(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"a.go":      "package x\n\nimport _ \"example.com/dep\"\n",
		"b.go":      "package x\n",
		"c_test.go": "package x\n\nimport _ \"example.com/dep\"\n",
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if got, want := importSites(t, "internal/x", dir, "example.com/dep"), "internal/x/a.go"; got != want {
		t.Errorf("importSites named %q, want %q", got, want)
	}
	if got, want := importSites(t, "internal/x", dir, "example.com/other"), "internal/x"; got != want {
		t.Errorf("importSites fallback returned %q, want the package path %q", got, want)
	}
}

// TestSplitListLine pins the go list line parsing, including the
// malformed-line branch and a package with no imports.
func TestSplitListLine(t *testing.T) {
	if _, _, _, ok := splitListLine("no tabs here"); ok {
		t.Error("splitListLine accepted a line without three fields")
	}
	pkgPath, dir, imports, ok := splitListLine("example.com/m/a\t/src/a\t")
	if !ok || pkgPath != "example.com/m/a" || dir != "/src/a" || imports != "" {
		t.Errorf("splitListLine(import-free line) = %q, %q, %q, %v", pkgPath, dir, imports, ok)
	}
	if _, _, imports, _ = splitListLine("p\td\tx y"); imports != "x y" {
		t.Errorf("splitListLine imports field = %q, want %q", imports, "x y")
	}
}

// splitListLine splits one line of the go list output into its three
// tab-separated fields. The imports field is empty for a package with
// no imports.
func splitListLine(line string) (pkgPath, dir, imports string, ok bool) {
	parts := strings.SplitN(line, "\t", 3)
	if len(parts) != 3 {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}

// importSites names the files in the package at dir that import imp, so
// a failure points at the offending line's file rather than just the
// package. Falls back to the package path if parsing finds nothing
// (cgo-generated imports, for instance).
func importSites(t *testing.T, rel, dir, imp string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("reading package dir: %v", err)
	}
	fset := token.NewFileSet()
	var files []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ImportsOnly)
		if err != nil {
			continue
		}
		for _, spec := range f.Imports {
			if strings.Trim(spec.Path.Value, `"`) == imp {
				files = append(files, rel+"/"+name)
				break
			}
		}
	}
	if len(files) == 0 {
		return rel
	}
	return strings.Join(files, ", ")
}

// goList runs go list with the given arguments and returns its stdout.
func goList(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("go", append([]string{"list"}, args...)...).Output()
	if err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			t.Fatalf("go list %s: %v\n%s", strings.Join(args, " "), err, exitErr.Stderr)
		}
		t.Fatalf("go list %s: %v", strings.Join(args, " "), err)
	}
	return string(out)
}
