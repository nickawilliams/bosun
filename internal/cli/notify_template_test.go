package cli

import (
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/notify"
	"github.com/spf13/viper"
)

// TestPrereleaseDefaultTextTemplate locks the rendered shape of the default
// prerelease notification — matches the #release_coordination convention:
// `going out `<repo>`: <url>` + the host-generated notes inline, one
// block per item separated by a blank line.
func TestPrereleaseDefaultTextTemplate(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	// GitHub-style body (standard Markdown) — emitted verbatim. The
	// provider adapter is responsible for rendering it (the Slack
	// adapter wraps the text in a markdown block).
	body := "## What's Changed\n* PR title by @alice in https://example.com/pull/1"
	c := buildNotifyContent("prerelease", notifyTemplateData{
		Issue: issueRef{Key: "PROJ-1"},
		Items: []notify.Item{
			{Label: "host-ui", URL: "https://example.com/host-ui/releases/tag/v1.0.0", Detail: "v1.0.0", Body: body},
		},
	})

	if c.Structured() {
		t.Fatalf("Structured() = true, want false (text default should be flat)")
	}
	got := c.Text
	wantPrefix := "going out `host-ui`: https://example.com/host-ui/releases/tag/v1.0.0\n"
	if !strings.HasPrefix(got, wantPrefix) {
		t.Errorf("missing prefix.\n got: %q\nwant: %q", got, wantPrefix)
	}
	if !strings.Contains(got, body) {
		t.Errorf("body not inline verbatim.\n got: %q", got)
	}
}

func TestPrereleaseDefaultTextTemplateMultipleItems(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	c := buildNotifyContent("prerelease", notifyTemplateData{
		Items: []notify.Item{
			{Label: "host-ui", URL: "u1", Body: "b1"},
			{Label: "legacy-api", URL: "u2", Body: "b2"},
		},
	})

	want := "going out `host-ui`: u1\nb1\n\ngoing out `legacy-api`: u2\nb2"
	if c.Text != want {
		t.Errorf("got %q\nwant %q", c.Text, want)
	}
}

// TestPrereleaseStringConfigOverridesDefault confirms a user-supplied
// string template at notification.templates.prerelease wins over the
// built-in default — the existing string-config path is preserved.
func TestPrereleaseStringConfigOverridesDefault(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("notification.templates.prerelease", "custom: {{.Issue.Key}}")

	c := buildNotifyContent("prerelease", notifyTemplateData{Issue: issueRef{Key: "PROJ-1"}})
	if c.Text != "custom: PROJ-1" {
		t.Errorf("got %q, want %q", c.Text, "custom: PROJ-1")
	}
}

// TestReviewIssueDataPopulated confirms the review type builds structured
// Content carrying the raw issue data — including the raw (un-normalized)
// issue-type icon URL, which the provider adapter normalizes for its own
// image proxy. Card assembly and icon normalization are the adapter's
// concern and are tested in the slack package.
func TestReviewIssueDataPopulated(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	rawIcon := "https://x.atlassian.net/rest/api/2/universal_avatar/view/type/issuetype/avatar/10315?size=medium"
	c := buildNotifyContent("review", notifyTemplateData{
		Issue: issueRef{
			Key:     "PROJ-1",
			Title:   "Add widget",
			Type:    "Story",
			IconURL: rawIcon,
		},
	})
	if !c.Structured() {
		t.Fatal("Structured() = false, want true (review builds structured content)")
	}
	if c.Issue == nil {
		t.Fatal("Issue = nil, want populated issue data")
	}
	if c.Issue.Key != "PROJ-1" || c.Issue.Title != "Add widget" || c.Issue.Type != "Story" {
		t.Errorf("Issue = %+v, want Key/Title/Type populated", c.Issue)
	}
	if c.Issue.IconURL != rawIcon {
		t.Errorf("Issue.IconURL = %q, want the raw URL %q (adapter normalizes)", c.Issue.IconURL, rawIcon)
	}
}

// TestPrereleaseMapConfigEntersStructuredPath confirms a map config on
// notification.templates.prerelease reopens the structured escape hatch
// even though the type now defaults to flat text.
func TestPrereleaseMapConfigEntersStructuredPath(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("notification.templates.prerelease.header", "Custom Header")

	c := buildNotifyContent("prerelease", notifyTemplateData{Issue: issueRef{Key: "PROJ-1"}})
	if !c.Structured() {
		t.Fatalf("Structured() = false, want true (map config should enter structured path)")
	}
	if c.Header != "Custom Header" {
		t.Errorf("Header = %q, want %q", c.Header, "Custom Header")
	}
}
