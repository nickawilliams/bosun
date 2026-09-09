package cli

import (
	"fmt"
	"regexp"
	"strings"
)

// This file holds the positional workspace-pattern grammar shared by
// the set-oriented commands (cleanup, status): a path-aware glob over
// workspace names. Workspace names are path-shaped ("feature/EX-123_
// slug"), so the grammar mirrors filesystem globbing:
//
//   - `*` matches within one path segment (does not cross `/`)
//   - `**` crosses segments
//   - `?` matches one non-separator character
//   - anything else matches literally
//
// A pattern with no glob metacharacters is an exact name — the
// single-mode form of the same argument (see
// resolveWorkspaceSelection).

// patternHasGlob reports whether pattern contains glob
// metacharacters. Without them the pattern is an exact workspace
// name.
func patternHasGlob(pattern string) bool {
	return strings.ContainsAny(pattern, "*?")
}

// compileWorkspacePattern translates the glob into an anchored
// regexp. Errors are effectively unreachable for the character set
// the translation emits, but the seam stays honest about them.
func compileWorkspacePattern(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	runes := []rune(pattern)
	for i := 0; i < len(runes); i++ {
		switch runes[i] {
		case '*':
			if i+1 < len(runes) && runes[i+1] == '*' {
				b.WriteString(".*")
				i++
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		default:
			b.WriteString(regexp.QuoteMeta(string(runes[i])))
		}
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil, fmt.Errorf("invalid workspace pattern %q: %w", pattern, err)
	}
	return re, nil
}

// matchWorkspaceNames narrows names to those the pattern matches,
// preserving order.
func matchWorkspaceNames(pattern string, names []string) ([]string, error) {
	re, err := compileWorkspacePattern(pattern)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, n := range names {
		if re.MatchString(n) {
			out = append(out, n)
		}
	}
	return out, nil
}
