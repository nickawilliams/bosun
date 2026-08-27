package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/ui"
)

// The template vocabulary.
//
// Four config keys take a Go template — vcs.branch.template,
// code_host.pr.title_template / body_template,
// notification.templates.<type>, and <stage>.url_template — and each
// used to carry its own field names for the same values. The issue key
// was {{.IssueNumber}} in a branch template and {{.IssueKey}}
// everywhere else; the preview environment name was {{.Name}} in a URL
// template and {{.PreviewName}} in a notification.
//
// One vocabulary now serves all of them, namespaced by subject. The
// namespace is what keeps them from drifting apart again: there is a
// single Issue struct, so a field cannot be spelled one way in one
// context and another way in the next. Contexts keep only the fields
// that are genuinely theirs — a branch has a Category, a PR has a
// Branch and BaseBranch, a notification has Items.

// issueRef is the issue half of every template context.
type issueRef struct {
	Key         string // Tracker key, e.g. "PROJ-123".
	Title       string // Issue summary, e.g. "Add widget endpoint".
	Slug        string // Branch-safe title, e.g. "add-widget-endpoint".
	Type        string // Tracker type name, e.g. "Story", "Bug".
	URL         string // Link to the issue in the tracker.
	Description string // Issue body text, plain. Empty when the tracker has none.
	IconURL     string // Issue-type icon. Empty falls back to a glyph.
}

// The preview half is preview.Ref, defined in the domain package
// rather than here: the adapters render <stage>.url_template
// themselves and cannot import the CLI, and a second copy of the
// shape is how {{.Name}} and {{.PreviewName}} came to mean the same
// thing in the first place.

// issueRefFrom builds the issue half of a template context from a
// fetched issue.
//
// Every caller that has an issue.Issue goes through it. review's two
// notification sites are the reason it is a function rather than a
// literal at each site: the assess-time content hash and the
// apply-time send must describe the same issue, and a field added to
// one literal and not the other would make every run report the
// notification as changed.
func issueRefFrom(key string, detail issue.Issue) issueRef {
	return issueRef{
		Key:         key,
		Title:       detail.Title,
		Type:        detail.Type,
		URL:         detail.URL,
		Description: detail.Description,
		IconURL:     detail.TypeIconURL,
	}
}

// legacyTemplateFields maps each template variable retired by the
// vocabulary unification to its replacement.
//
// It exists because three of the four render paths degrade silently on
// a template error: buildPRTitle falls back to a hardcoded title,
// buildPRBody, renderStageURL and renderTemplate return "". Without a
// diagnostic, a config still using the old names would keep working in
// appearance and quietly stop honoring its template — the "absence
// silently changes behavior" case AGENTS.md reserves the breaking-change
// marker for. Naming the replacement is what makes this a warned
// migration instead.
//
// Bare Name is deliberately absent: it was only ever valid in a stage
// URL template, and matching it everywhere would misread {{.Name}} on a
// range variable as a stale field. renderStageURL passes it explicitly.
var legacyTemplateFields = map[string]string{
	"IssueNumber":      "Issue.Key",
	"IssueKey":         "Issue.Key",
	"IssueSlug":        "Issue.Slug",
	"IssueTitle":       "Issue.Title",
	"IssueType":        "Issue.Type",
	"IssueURL":         "Issue.URL",
	"IssueDescription": "Issue.Description",
	"IssueIconURL":     "Issue.IconURL",
	"PreviewName":      "Preview.Name",
	"PreviewURL":       "Preview.URL",
}

// templateMigrationHint reports the retired variables a pattern still
// uses, as a single ready-to-print clause naming each replacement, or
// "" when the pattern is clean. extra carries context-specific
// retirements the shared map cannot claim globally.
//
// Matching is a substring scan rather than a parse of the template's
// action list: the pattern that reaches here has already failed, and a
// pattern that fails to PARSE has no action list to walk. A false
// positive costs a line of advice; a false negative costs the whole
// point of the diagnostic.
func templateMigrationHint(pattern string, extra map[string]string) string {
	var hints []string
	seen := map[string]bool{}

	check := func(field, replacement string) {
		if seen[field] || !strings.Contains(pattern, "."+field) {
			return
		}
		seen[field] = true
		hints = append(hints, fmt.Sprintf("{{.%s}} is now {{.%s}}", field, replacement))
	}
	for field, replacement := range extra {
		check(field, replacement)
	}
	for field, replacement := range legacyTemplateFields {
		check(field, replacement)
	}

	if len(hints) == 0 {
		return ""
	}
	// Map iteration is randomized, and this string reaches the user.
	sort.Strings(hints)
	return strings.Join(hints, "; ")
}

// reportTemplateFailure surfaces a template that would otherwise fail
// in silence, naming the config key that carries it and — when the
// cause is a variable this project retired — the replacement.
//
// The render paths keep their fallbacks. The point is not to abort a
// command over a notification's wording; it is that a template which
// stopped being honored must not look identical to one that was.
func reportTemplateFailure(configKey, pattern string, err error, extra map[string]string) {
	msg := fmt.Sprintf("%s: %v", configKey, err)
	if hint := templateMigrationHint(pattern, extra); hint != "" {
		msg += " — " + hint
	}
	ui.Warning("%s", msg)
}
