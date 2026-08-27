package cli

import (
	"errors"
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/preview"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/viper"
)

// captureReporter installs a CaptureReporter as the default for the
// test and returns it, so ui.Warning calls are assertable.
func captureReporter(t *testing.T) *ui.CaptureReporter {
	t.Helper()
	prev := ui.Default()
	rep := ui.NewCaptureReporter()
	ui.SetDefault(rep)
	t.Cleanup(func() { ui.SetDefault(prev) })
	return rep
}

// warnings returns every label ui.Warning was called with.
func warnings(rep *ui.CaptureReporter) []string {
	var out []string
	for _, ev := range rep.OfKind(ui.CaptureWarning) {
		out = append(out, ev.Label)
	}
	return out
}

// TestTemplateVocabularyIsShared is the property this whole change
// exists to establish: one spelling per value, across every context a
// user can write a template for. Asserting it as a table rather than
// per-context is deliberate — a future context that reintroduces its
// own name for the issue key has to come past this test.
func TestTemplateVocabularyIsShared(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)

	detail := issue.Issue{Title: "Add widget", Type: "Story", URL: "https://tracker.test/PROJ-1"}
	ref := issueRefFrom("PROJ-1", detail)

	// The issue key renders from the same expression in all three
	// contexts that carry one.
	t.Run("issue key", func(t *testing.T) {
		viper.Set("vcs.branch.template", "{{.Issue.Key}}")
		got, err := buildBranchName("PROJ-1", "Story", "Add widget", "")
		if err != nil || got != "PROJ-1" {
			t.Errorf("branch = (%q, %v), want PROJ-1", got, err)
		}

		viper.Set("code_host.pr.title_template", "{{.Issue.Key}}")
		if got := buildPRTitle(prTemplateData{Issue: ref}); got != "PROJ-1" {
			t.Errorf("pr title = %q, want PROJ-1", got)
		}

		viper.Set("notification.templates.review", "{{.Issue.Key}}")
		if got := buildNotifyContent("review", notifyTemplateData{Issue: ref}).Text; got != "PROJ-1" {
			t.Errorf("notification = %q, want PROJ-1", got)
		}
	})

	// The preview name renders from the same expression in both
	// contexts that carry one — the split that motivated the change.
	t.Run("preview name", func(t *testing.T) {
		viper.Set("preview.url_template", "https://{{.Preview.Name}}.test")
		if got := renderStageURL("preview", "brave-falcon"); got != "https://brave-falcon.test" {
			t.Errorf("stage URL = %q, want the rendered name", got)
		}

		viper.Set("notification.templates.review", "{{.Preview.Name}}")
		content := buildNotifyContent("review", notifyTemplateData{
			Issue:   ref,
			Preview: preview.Ref{Name: "brave-falcon"},
		})
		if content.Text != "brave-falcon" {
			t.Errorf("notification = %q, want brave-falcon", content.Text)
		}
	})
}

// TestIssueRefFromCarriesEveryField guards the helper both review
// notification sites share. A field it drops is a field that silently
// stops being available to templates, and — because the assess-time
// hash and the apply-time send both read it — one that would report
// the notification as unchanged when it changed.
func TestIssueRefFromCarriesEveryField(t *testing.T) {
	got := issueRefFrom("PROJ-1", issue.Issue{
		Title:       "Add widget",
		Type:        "Story",
		URL:         "https://tracker.test/PROJ-1",
		Description: "body text",
		TypeIconURL: "https://tracker.test/icon.png",
	})
	want := issueRef{
		Key:         "PROJ-1",
		Title:       "Add widget",
		Type:        "Story",
		URL:         "https://tracker.test/PROJ-1",
		Description: "body text",
		IconURL:     "https://tracker.test/icon.png",
	}
	if got != want {
		t.Errorf("issueRefFrom() = %+v, want %+v", got, want)
	}
	// Slug is deliberately absent: it is derived per branch template
	// from a possibly user-supplied override, not carried on the issue.
	if got.Slug != "" {
		t.Errorf("Slug = %q, want empty — buildBranchName owns it", got.Slug)
	}
}

func TestTemplateMigrationHint(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		extra   map[string]string
		want    string
	}{
		{
			name:    "clean pattern yields nothing",
			pattern: "{{.Issue.Key}} {{.Issue.Title}}",
		},
		{
			// The renamed-not-just-moved case: IssueNumber never held
			// a number, and the hint has to name Key rather than echo
			// the old word back.
			name:    "IssueNumber points at Issue.Key",
			pattern: "{{.Category}}/{{.IssueNumber}}_{{.IssueSlug}}",
			want:    "{{.IssueNumber}} is now {{.Issue.Key}}; {{.IssueSlug}} is now {{.Issue.Slug}}",
		},
		{
			name:    "preview fields",
			pattern: "{{.PreviewName}} at {{.PreviewURL}}",
			want:    "{{.PreviewName}} is now {{.Preview.Name}}; {{.PreviewURL}} is now {{.Preview.URL}}",
		},
		{
			// IssueIconURL and IconURL differ by a prefix, and only
			// the first was retired. Reporting the survivor would send
			// the reader to change a field that still works.
			name:    "the surviving IconURL is not reported",
			pattern: "{{.IconURL}}",
		},
		{
			name:    "extra carries a context-specific retirement",
			pattern: "https://{{.Name}}.test",
			extra:   map[string]string{"Name": "Preview.Name"},
			want:    "{{.Name}} is now {{.Preview.Name}}",
		},
		{
			// Bare Name is only retired where extra says so. A range
			// variable's .Name in a notification template is live.
			name:    "bare Name is not reported without extra",
			pattern: "{{range .Items}}{{.Name}}{{end}}",
		},
		{
			// Each retired field is named once however often it
			// appears, and the order is stable — map iteration is not.
			name:    "repeats collapse",
			pattern: "{{.IssueKey}} {{.IssueKey}}",
			want:    "{{.IssueKey}} is now {{.Issue.Key}}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := templateMigrationHint(tt.pattern, tt.extra); got != tt.want {
				t.Errorf("templateMigrationHint() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestTemplateMigrationHintIsStable pins the ordering. The hint is
// built by ranging two maps, and Go randomizes that — an unsorted
// result would produce a different sentence on different runs for the
// same config.
func TestTemplateMigrationHintIsStable(t *testing.T) {
	pattern := "{{.IssueKey}} {{.IssueTitle}} {{.IssueURL}} {{.PreviewName}}"
	first := templateMigrationHint(pattern, map[string]string{"Name": "Preview.Name"})
	for range 20 {
		if got := templateMigrationHint(pattern, map[string]string{"Name": "Preview.Name"}); got != first {
			t.Fatalf("hint varies between calls:\n %q\n %q", first, got)
		}
	}
}

// TestReportTemplateFailureNamesKeyAndMigration pins what the user
// actually reads: the config key that carries the broken template, the
// underlying error, and the replacement for the retired variable.
func TestReportTemplateFailureNamesKeyAndMigration(t *testing.T) {
	rep := captureReporter(t)
	reportTemplateFailure(
		"vcs.branch.template",
		"{{.IssueNumber}}",
		errors.New("can't evaluate field IssueNumber"),
		nil,
	)

	got := warnings(rep)
	if len(got) != 1 {
		t.Fatalf("warnings = %v, want exactly one", got)
	}
	for _, want := range []string{"vcs.branch.template", "can't evaluate field IssueNumber", "{{.Issue.Key}}"} {
		if !strings.Contains(got[0], want) {
			t.Errorf("warning %q does not carry %q", got[0], want)
		}
	}
}

// TestLegacyTemplateIsReportedNotSilent is the reason this change is a
// warned migration rather than a silent break. Each of these paths
// falls back on a template error — a hardcoded title, an empty body, no
// URL — so without the report a config still on the old vocabulary
// would look like it was being honored.
func TestLegacyTemplateIsReportedNotSilent(t *testing.T) {
	tests := []struct {
		name      string
		configKey string
		pattern   string
		render    func() string
		wantValue string // "" means the path degrades to empty
	}{
		{
			name:      "pr title falls back but says so",
			configKey: "code_host.pr.title_template",
			pattern:   "{{.IssueType}} {{.IssueKey}}",
			render: func() string {
				return buildPRTitle(prTemplateData{Issue: issueRef{Key: "PROJ-1", Title: "Add widget"}})
			},
			wantValue: "[PROJ-1] Add widget",
		},
		{
			name:      "pr body degrades but says so",
			configKey: "code_host.pr.body_template",
			pattern:   "{{.IssueURL}}",
			render: func() string {
				return buildPRBody(prTemplateData{Issue: issueRef{Key: "PROJ-1"}})
			},
		},
		{
			name:      "notification degrades but says so",
			configKey: "notification.templates.review",
			pattern:   "{{.PreviewName}}",
			render: func() string {
				return buildNotifyContent("review", notifyTemplateData{
					Issue:   issueRef{Key: "PROJ-1"},
					Preview: preview.Ref{Name: "brave-falcon"},
				}).Text
			},
		},
		{
			name:      "stage URL degrades but says so",
			configKey: "preview.url_template",
			pattern:   "https://{{.Name}}.test",
			render:    func() string { return renderStageURL("preview", "brave-falcon") },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			viper.Reset()
			t.Cleanup(viper.Reset)
			viper.Set(tt.configKey, tt.pattern)
			rep := captureReporter(t)

			if got := tt.render(); got != tt.wantValue {
				t.Errorf("render = %q, want %q", got, tt.wantValue)
			}

			got := warnings(rep)
			if len(got) == 0 {
				t.Fatalf("a stale %s was honored silently", tt.configKey)
			}
			if !strings.Contains(got[0], tt.configKey) {
				t.Errorf("warning %q does not name the config key", got[0])
			}
			if !strings.Contains(got[0], " is now ") {
				t.Errorf("warning %q does not name a replacement", got[0])
			}
		})
	}
}

// TestBuildPRTitleDefaultIsSilent guards the other half: the built-in
// default is not user config, so a run with no title_template set must
// not warn about one.
func TestBuildPRTitleDefaultIsSilent(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	rep := captureReporter(t)

	got := buildPRTitle(prTemplateData{Issue: issueRef{Key: "PROJ-1", Title: "Add widget"}})
	if got != "[PROJ-1] Add widget" {
		t.Errorf("title = %q, want the built-in default", got)
	}
	if w := warnings(rep); len(w) != 0 {
		t.Errorf("the built-in default warned: %v", w)
	}
}

// TestMalformedTemplateIsReported covers the parse arm, which is a
// different failure from the execute arm above: a pattern that does not
// parse has no action list at all. templateMigrationHint scans the raw
// string rather than walking parsed actions precisely so it still has
// something to say here — a stale variable inside an unclosed action is
// the likeliest way both faults arrive together.
func TestMalformedTemplateIsReported(t *testing.T) {
	// Unclosed action: fails to parse, and still names a retired field.
	const malformed = "{{.IssueKey"

	tests := []struct {
		name      string
		configKey string
		render    func() string
		wantValue string
	}{
		{
			name:      "pr title",
			configKey: "code_host.pr.title_template",
			render: func() string {
				return buildPRTitle(prTemplateData{Issue: issueRef{Key: "PROJ-1", Title: "Add widget"}})
			},
			wantValue: "[PROJ-1] Add widget",
		},
		{
			name:      "pr body",
			configKey: "code_host.pr.body_template",
			render:    func() string { return buildPRBody(prTemplateData{Issue: issueRef{Key: "PROJ-1"}}) },
		},
		{
			name:      "notification",
			configKey: "notification.templates.review",
			render: func() string {
				return buildNotifyContent("review", notifyTemplateData{Issue: issueRef{Key: "PROJ-1"}}).Text
			},
		},
		{
			name:      "stage URL",
			configKey: "preview.url_template",
			render:    func() string { return renderStageURL("preview", "brave-falcon") },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			viper.Reset()
			t.Cleanup(viper.Reset)
			viper.Set(tt.configKey, malformed)
			rep := captureReporter(t)

			if got := tt.render(); got != tt.wantValue {
				t.Errorf("render = %q, want %q", got, tt.wantValue)
			}
			got := warnings(rep)
			if len(got) == 0 {
				t.Fatalf("a malformed %s failed silently", tt.configKey)
			}
			if !strings.Contains(got[0], tt.configKey) {
				t.Errorf("warning %q does not name the config key", got[0])
			}
			// The migration advice survives an unparseable pattern.
			if !strings.Contains(got[0], "{{.Issue.Key}}") {
				t.Errorf("warning %q lost the migration hint on a parse failure", got[0])
			}
		})
	}
}

// TestBuildBranchNameMalformedTemplate is the branch path's parse arm.
// It returns the parse error bare — there is no rendered name to fall
// back to, and the error text already quotes the offending pattern.
func TestBuildBranchNameMalformedTemplate(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("vcs.branch.template", "{{.Issue.Key")

	got, err := buildBranchName("PROJ-1", "Story", "Add widget", "")
	if err == nil {
		t.Fatalf("buildBranchName() = %q, want a parse error", got)
	}
}

// TestBuildBranchNameNonLegacyFailureHasNoHint guards against advice
// that misleads: a template naming a field that never existed is a
// typo, not a migration, and telling the user to rename something they
// never wrote would send them looking in the wrong place.
func TestBuildBranchNameNonLegacyFailureHasNoHint(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("vcs.branch.template", "{{.Category}}/{{.Nonsense}}")

	_, err := buildBranchName("PROJ-1", "Story", "Add widget", "")
	if err == nil {
		t.Fatal("expected an error for an unknown field")
	}
	if strings.Contains(err.Error(), " is now ") {
		t.Errorf("err %q offers a migration for a field that was never retired", err)
	}
}

// TestBuildBranchNameAbortsOnStaleTemplate is the one path that refuses
// rather than degrading: a branch name is the run's identity, and half
// a name is worse than a refusal. The migration hint still has to reach
// the user, which for this path means riding on the error.
func TestBuildBranchNameAbortsOnStaleTemplate(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("vcs.branch.template", "{{.Category}}/{{.IssueNumber}}")

	got, err := buildBranchName("PROJ-1", "Story", "Add widget", "")
	if err == nil {
		t.Fatalf("buildBranchName() = %q, want an error on a stale template", got)
	}
	for _, want := range []string{"vcs.branch.template", "{{.Issue.Key}}"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err %q does not carry %q", err, want)
		}
	}
}
