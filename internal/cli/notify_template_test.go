package cli

import (
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/notify"
	"github.com/spf13/viper"
)

// TestReleaseDefaultTextTemplate locks the rendered shape of the default
// release notification — matches the #release_coordination convention:
// `going out `<repo>`: <url>` + the host-generated notes inline, one
// block per item separated by a blank line.
func TestReleaseDefaultTextTemplate(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	// GitHub-style body (standard Markdown) — emitted verbatim. The
	// provider adapter is responsible for rendering it (the Slack
	// adapter wraps the text in a markdown block).
	body := "## What's Changed\n* PR title by @alice in https://example.com/pull/1"
	c := buildNotifyContent("release", notifyTemplateData{
		IssueKey: "PROJ-1",
		Items: []notify.Item{
			{Label: "host-ui", URL: "https://example.com/host-ui/releases/tag/v1.0.0", Detail: "v1.0.0", Body: body},
		},
	})

	if c.HasBlocks() {
		t.Fatalf("HasBlocks() = true, want false (text default should be flat)")
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

func TestReleaseDefaultTextTemplateMultipleItems(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	c := buildNotifyContent("release", notifyTemplateData{
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

// TestReleaseStringConfigOverridesDefault confirms a user-supplied string
// template at notification.templates.release wins over the built-in
// default — the existing string-config path is preserved.
func TestReleaseStringConfigOverridesDefault(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("notification.templates.release", "custom: {{.IssueKey}}")

	c := buildNotifyContent("release", notifyTemplateData{IssueKey: "PROJ-1"})
	if c.Text != "custom: PROJ-1" {
		t.Errorf("got %q, want %q", c.Text, "custom: PROJ-1")
	}
}

// TestSlackIconURL confirms a Jira issue-type icon URL is normalized for
// Slack's image proxy: universal_avatar URLs (SVG by default) get
// format=png plus the shared card size; legacy SVG system icons are
// swapped for the PNG sibling Jira serves; empty/unusable inputs return
// "" so the card falls back to the :jira: glyph.
func TestSlackIconURL(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{
			"universal_avatar forces png and size",
			"https://x.atlassian.net/rest/api/2/universal_avatar/view/type/issuetype/avatar/10315?size=medium",
			"https://x.atlassian.net/rest/api/2/universal_avatar/view/type/issuetype/avatar/10315?format=png&size=" + cardIconJiraSize,
		},
		{
			"legacy svg system icon swapped to png",
			"https://x.atlassian.net/images/icons/issuetypes/epic.svg",
			"https://x.atlassian.net/images/icons/issuetypes/epic.png",
		},
		{
			"plain raster passes through",
			"https://x.atlassian.net/images/icons/issuetypes/story.png",
			"https://x.atlassian.net/images/icons/issuetypes/story.png",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := slackIconURL(tc.in); got != tc.want {
				t.Errorf("slackIconURL(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestReviewJiraCardIconNormalized confirms the Jira card's icon is
// emitted as a Slack-renderable PNG at the shared card size on the block
// path, with no :jira: glyph when a real icon is present.
func TestReviewJiraCardIconNormalized(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	c := buildNotifyContent("review", notifyTemplateData{
		IssueKey:     "PROJ-1",
		IssueTitle:   "Add widget",
		IssueIconURL: "https://x.atlassian.net/rest/api/2/universal_avatar/view/type/issuetype/avatar/10315?size=medium",
	})
	if len(c.Sections) == 0 {
		t.Fatal("no sections rendered")
	}
	card := c.Sections[0]
	wantIcon := "https://x.atlassian.net/rest/api/2/universal_avatar/view/type/issuetype/avatar/10315?format=png&size=" + cardIconJiraSize
	if card.IconURL != wantIcon {
		t.Errorf("IconURL = %q, want %q", card.IconURL, wantIcon)
	}
	if strings.Contains(card.Text, ":jira:") {
		t.Errorf("Text = %q, want no :jira: glyph when an icon is present", card.Text)
	}
}

// TestReleaseMapConfigEntersBlockPath confirms a map config on
// notification.templates.release reopens the structured-block escape
// hatch even though the type now defaults to text.
func TestReleaseMapConfigEntersBlockPath(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("notification.templates.release.header", "Custom Header")

	c := buildNotifyContent("release", notifyTemplateData{IssueKey: "PROJ-1"})
	if !c.HasBlocks() {
		t.Fatalf("HasBlocks() = false, want true (map config should enter block path)")
	}
	if c.Header != "Custom Header" {
		t.Errorf("Header = %q, want %q", c.Header, "Custom Header")
	}
}
