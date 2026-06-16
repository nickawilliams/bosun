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
