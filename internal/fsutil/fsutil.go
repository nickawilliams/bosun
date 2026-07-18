// Package fsutil holds small filesystem helpers shared across bosun.
package fsutil

import (
	"os"
	"strings"
)

// junkNames are machine-generated filesystem artifacts (Finder, Windows
// Explorer, desktop indexers) that no human intentionally creates. bosun
// skips them in its NON-git directory scans — stray-file guards and
// empty-dir checks over the workspace container, which isn't a repo and
// so isn't covered by git's ignore rules.
//
// This set is deliberately narrow and is NOT derived from gitignore.
// bosun's destructive guards (cleanup) exist precisely to surface files
// git would hide — a gitignored .env, key, or dump is exactly what a
// human would want flagged before deletion. Only true noise belongs here.
var junkNames = map[string]bool{
	".DS_Store":               true,
	".localized":              true,
	".Spotlight-V100":         true,
	".Trashes":                true,
	".fseventsd":              true,
	".DocumentRevisions-V100": true,
	".TemporaryItems":         true,
	".apdisk":                 true,
	".directory":              true, // KDE
	"Thumbs.db":               true,
	"ehthumbs.db":             true,
	"desktop.ini":             true,
	"Desktop.ini":             true,
}

// IgnorableName reports whether a directory-entry name is machine-
// generated OS/editor junk safe to skip in bosun's non-git scans. It
// matches the fixed junkNames set plus macOS AppleDouble sidecars
// ("._*"). Intentionally not gitignore-aware — see junkNames.
func IgnorableName(name string) bool {
	if junkNames[name] {
		return true
	}
	// macOS AppleDouble resource-fork sidecars ("._Foo" alongside "Foo").
	return strings.HasPrefix(name, "._")
}

// HasMeaningfulEntries reports whether entries contains anything that
// isn't ignorable OS junk — i.e. whether a directory is "non-empty" for
// bosun's purposes. A directory holding only junk counts as empty.
func HasMeaningfulEntries(entries []os.DirEntry) bool {
	for _, e := range entries {
		if !IgnorableName(e.Name()) {
			return true
		}
	}
	return false
}
