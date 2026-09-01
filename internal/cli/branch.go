package cli

import (
	"fmt"
	"regexp"
	"strings"
	"text/template"

	"github.com/nickawilliams/bosun/internal/issue"
	"github.com/spf13/viper"
)

// branchData holds the template variables for branch name generation.
// Category is the branch's own; everything about the issue comes from
// the shared vocabulary (see template.go).
type branchData struct {
	Category string // e.g., "feature", "fix", "chore"
	Issue    issueRef
}

var (
	slugRe         = regexp.MustCompile(`[^a-z0-9]+`)
	defaultPattern = "{{.Issue.Key}}-{{.Issue.Slug}}"
)

// buildBranchName generates a branch name from the configured pattern
// and the fetched issue. When slug is non-empty it is used directly;
// otherwise one is derived from the issue title.
//
// It takes the whole issue rather than the three fields it needs
// directly, because the template context advertises all of them. A
// field left unpopulated does not fail — Go renders a known-but-empty
// field as "" — so {{.Issue.Type}} in a branch template would have
// produced a silent "/PROJ-1" rather than an error. Passing the issue
// whole is what makes the advertised vocabulary the real one.
func buildBranchName(detail issue.Issue, slug string) (string, error) {
	pattern := viper.GetString("vcs.branch.template")
	if pattern == "" {
		pattern = defaultPattern
	}

	category := resolveCategory(detail.Type)
	if slug == "" {
		slug = slugify(detail.Title)
	}

	tmpl, err := template.New("branch").Parse(pattern)
	if err != nil {
		return "", err
	}

	ref := issueRefFrom(detail.Key, detail)
	ref.Slug = slug

	var buf strings.Builder
	err = tmpl.Execute(&buf, branchData{Category: category, Issue: ref})
	if err != nil {
		// The only render path that aborts rather than degrading: a
		// branch name is the run's identity, and half a name is worse
		// than a refusal. The hint still travels — this error reaches
		// the user verbatim.
		if h := templateMigrationHint(pattern, nil); h != "" {
			return "", fmt.Errorf("vcs.branch.template: %w — %s", err, h)
		}
		return "", err
	}

	return buf.String(), nil
}

// resolveCategory maps an issue type name (from the tracker) to a branch
// category using the vcs.branch.categories config. Falls back to lowercase
// issue type if no mapping is found.
func resolveCategory(issueType string) string {
	key := "vcs.branch.categories." + strings.ToLower(issueType)
	if cat := viper.GetString(key); cat != "" {
		return cat
	}
	return strings.ToLower(issueType)
}

// slugify converts a title into a URL/branch-safe slug.
func slugify(s string) string {
	s = strings.ToLower(s)
	s = slugRe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}
