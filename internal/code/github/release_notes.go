package github

import "regexp"

// PrettifyReleaseNotes rewrites GitHub's auto-generated release-notes
// markdown so that PR/issue URLs, compare URLs, and bare `@username`
// mentions render as the display-friendly link text GitHub uses on its
// own rendered release page (`#42`, `v1.0.0...v1.1.0`, `@alice`) rather
// than as bare URLs. The output is still standard Markdown — any
// downstream consumer that renders Markdown gets the prettified form
// automatically.
//
// All GitHub-specific knowledge (URL patterns, username syntax, what
// `@user` means) lives in this package. By the time the prettified body
// crosses the code.Host abstraction boundary on code.Release.Body,
// nothing above it needs to know GitHub exists.
//
// Limitations:
//   - Patterns aren't aware of code spans, so “ `@alice` “ inside
//     backticks would still be rewritten. GitHub's auto-generated notes
//     don't wrap mentions in code, so this is fine in practice.
//   - URLs already wrapped in markdown link syntax (`[text](url)`) are
//     left alone because the URL is not preceded by a word-boundary the
//     bare-URL anchors expect — but the safer behavior also relies on
//     each pattern's anchor (start-of-string / whitespace / `(`).
func PrettifyReleaseNotes(body string) string {
	body = prettifyPRIssueURLs(body)
	body = prettifyCompareURLs(body)
	body = prettifyMentions(body)
	body = stripHeadingMarkers(body)
	return body
}

var (
	// Issue + PR URLs: https://github.com/<owner>/<repo>/(pull|issues)/<n>
	// Anchored at start-of-string or a leading whitespace ONLY — `(` is
	// deliberately excluded so a URL already inside a markdown link
	// target (`[#42](https://github.com/...)`) doesn't get re-wrapped.
	// `\w-.` covers GitHub's allowed owner/repo characters.
	prettyPRIssueRe = regexp.MustCompile(
		`(^|\s)(https://github\.com/[\w.-]+/[\w.-]+/(?:pull|issues)/(\d+))`,
	)

	// Compare URLs: https://github.com/<owner>/<repo>/compare/<spec>
	// Same leading-anchor rule as the PR/issue regex so already-wrapped
	// URLs are left alone.
	prettyCompareRe = regexp.MustCompile(
		`(^|\s)(https://github\.com/[\w.-]+/[\w.-]+/compare/([^\s)]+))`,
	)

	// User mentions: `@<username>`. GitHub usernames are 1–39 chars,
	// alphanumeric or hyphens, no leading/trailing hyphen. Anchored on a
	// preceding whitespace/`(` so we don't match `@` inside emails
	// (`user@example.com`) or URLs (`api.github.com/users/x@y`). The
	// trailing assertion isn't expressible in Go's regexp engine; the
	// character-class is permissive but the username regex's tight
	// allowed-set keeps over-matching contained.
	prettyMentionRe = regexp.MustCompile(
		`(^|[\s(])@([A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?)`,
	)

	// Leading heading markers: `# ` through `###### ` at line start.
	// Slack's markdown block does render these as actual heading-styled
	// text, but the #release_coordination convention shows the heading
	// as plain text on its own line. Strip the marker, keep the heading
	// text. Line-anchored so an inline `#42` reference (not a heading) is
	// left alone.
	prettyHeadingRe = regexp.MustCompile(`(?m)^#{1,6}\s+`)
)

func prettifyPRIssueURLs(body string) string {
	return prettyPRIssueRe.ReplaceAllString(body, "${1}[#${3}](${2})")
}

func prettifyCompareURLs(body string) string {
	return prettyCompareRe.ReplaceAllString(body, "${1}[${3}](${2})")
}

func prettifyMentions(body string) string {
	return prettyMentionRe.ReplaceAllString(body, "${1}[@${2}](https://github.com/${2})")
}

func stripHeadingMarkers(body string) string {
	return prettyHeadingRe.ReplaceAllString(body, "")
}
