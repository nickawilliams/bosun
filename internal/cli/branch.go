package cli

import (
	"regexp"
	"strings"
	"text/template"

	"github.com/spf13/viper"
)

// branchData holds the template variables for branch name generation.
type branchData struct {
	Category    string // e.g., "feature", "fix", "chore"
	IssueNumber string // e.g., "PROJ-123"
	IssueSlug   string // e.g., "add-widget-endpoint"
	IssueTitle  string // e.g., "Add widget endpoint"
}

var (
	slugRe      = regexp.MustCompile(`[^a-z0-9]+`)
	defaultPattern = "{{.Category}}/{{.IssueNumber}}_{{.IssueSlug}}"
)

// buildBranchName generates a branch name from the configured pattern,
// the issue type (from the tracker), and the issue title. When slug is
// non-empty it is used directly; otherwise one is derived from the title.
func buildBranchName(issueKey, issueType, issueTitle, slug string) (string, error) {
	pattern := viper.GetString("branch.template")
	if pattern == "" {
		pattern = defaultPattern
	}

	category := resolveCategory(issueType)
	if slug == "" {
		slug = slugify(issueTitle)
	}

	tmpl, err := template.New("branch").Parse(pattern)
	if err != nil {
		return "", err
	}

	var buf strings.Builder
	err = tmpl.Execute(&buf, branchData{
		Category:    category,
		IssueNumber: issueKey,
		IssueSlug:   slug,
		IssueTitle:  issueTitle,
	})
	if err != nil {
		return "", err
	}

	return buf.String(), nil
}

// resolveCategory maps an issue type name (from the tracker) to a branch
// category using the branch.categories config. Falls back to lowercase
// issue type if no mapping is found.
func resolveCategory(issueType string) string {
	key := "branch.categories." + strings.ToLower(issueType)
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
