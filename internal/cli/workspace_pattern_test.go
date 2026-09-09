package cli

import (
	"slices"
	"testing"
)

// TestPatternHasGlob pins the exact-name boundary: only * and ? make
// a pattern a glob; everything else is a literal workspace name.
func TestPatternHasGlob(t *testing.T) {
	tests := []struct {
		pattern string
		want    bool
	}{
		{"feature/EX-123_slug", false},
		{"EX-1-feature", false},
		{"feature/*", true},
		{"**", true},
		{"EX-?", true},
		{"epic/one", false},
	}
	for _, tt := range tests {
		if got := patternHasGlob(tt.pattern); got != tt.want {
			t.Errorf("patternHasGlob(%q) = %v, want %v", tt.pattern, got, tt.want)
		}
	}
}

// TestMatchWorkspaceNames locks the path-aware glob semantics:
// `*` stays within a path segment, `**` crosses segments, and
// literal characters (including regexp metacharacters like `.` and
// `+`) match themselves.
func TestMatchWorkspaceNames(t *testing.T) {
	names := []string{
		"EX-1-feature",
		"feature/EX-2_api",
		"feature/EX-3_web",
		"feature/deep/EX-4_x",
		"epic/one",
		"scratch",
	}

	tests := []struct {
		pattern string
		want    []string
	}{
		{"**", names},
		{"*", []string{"EX-1-feature", "scratch"}},
		{"feature/*", []string{"feature/EX-2_api", "feature/EX-3_web"}},
		{"feature/**", []string{"feature/EX-2_api", "feature/EX-3_web", "feature/deep/EX-4_x"}},
		{"feature/EX-2_api", []string{"feature/EX-2_api"}},
		{"EX-?-feature", []string{"EX-1-feature"}},
		{"feature/EX-*", []string{"feature/EX-2_api", "feature/EX-3_web"}},
		{"*/one", []string{"epic/one"}},
		{"nothing-matches-*", nil},
	}

	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			got, err := matchWorkspaceNames(tt.pattern, names)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("matchWorkspaceNames(%q) = %v, want %v", tt.pattern, got, tt.want)
			}
		})
	}
}

// TestMatchWorkspaceNamesLiteralMetacharacters guards the regexp
// escaping: a name containing regexp-special characters is matched
// literally, and a `.` in a pattern doesn't become a wildcard.
func TestMatchWorkspaceNamesLiteralMetacharacters(t *testing.T) {
	names := []string{"release/v1.2", "release/v1x2"}
	got, err := matchWorkspaceNames("release/v1.2", names)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if !slices.Equal(got, []string{"release/v1.2"}) {
		t.Errorf("got %v, want the dot matched literally", got)
	}
}
