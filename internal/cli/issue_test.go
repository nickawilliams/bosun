package cli

import (
	"testing"

	"github.com/nickawilliams/bosun/internal/issue"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newTestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:  "test",
		RunE: func(cmd *cobra.Command, args []string) error { return nil },
	}
	addIssueFlag(cmd)
	return cmd
}

func TestResolveIssueSilent(t *testing.T) {
	t.Run("from flag", func(t *testing.T) {
		cmd := newTestCmd()
		cmd.SetArgs([]string{"--issue", "PROJ-123"})
		_ = cmd.Execute()

		got := resolveIssueSilent(cmd, "")
		if got != "PROJ-123" {
			t.Errorf("resolveIssueSilent() = %q, want %q", got, "PROJ-123")
		}
	})

	t.Run("from short flag", func(t *testing.T) {
		cmd := newTestCmd()
		cmd.SetArgs([]string{"-i", "PROJ-456"})
		_ = cmd.Execute()

		got := resolveIssueSilent(cmd, "")
		if got != "PROJ-456" {
			t.Errorf("resolveIssueSilent() = %q, want %q", got, "PROJ-456")
		}
	})

	// No viper env layer is configured here, matching production:
	// config.Load enables none (#110). The chain reads BOSUN_ISSUE
	// with os.Getenv, so this passes on the strength of that read
	// alone — and would fail if someone replaced it with a viper
	// lookup.
	t.Run("from env var", func(t *testing.T) {
		viper.Reset()

		t.Setenv("BOSUN_ISSUE", "PROJ-789")
		t.Cleanup(func() {
			viper.Reset()
		})

		cmd := newTestCmd()
		cmd.SetArgs([]string{})
		_ = cmd.Execute()

		got := resolveIssueSilent(cmd, "")
		if got != "PROJ-789" {
			t.Errorf("resolveIssueSilent() = %q, want %q", got, "PROJ-789")
		}
	})

	t.Run("flag takes precedence over env", func(t *testing.T) {
		t.Setenv("BOSUN_ISSUE", "PROJ-ENV")

		cmd := newTestCmd()
		cmd.SetArgs([]string{"--issue", "PROJ-FLAG"})
		_ = cmd.Execute()

		got := resolveIssueSilent(cmd, "")
		if got != "PROJ-FLAG" {
			t.Errorf("resolveIssueSilent() = %q, want %q", got, "PROJ-FLAG")
		}
	})

	t.Run("empty when not specified", func(t *testing.T) {
		t.Setenv("BOSUN_ISSUE", "")

		cmd := newTestCmd()
		cmd.SetArgs([]string{})
		_ = cmd.Execute()

		got := resolveIssueSilent(cmd, "")
		if got != "" {
			t.Errorf("resolveIssueSilent() = %q, want empty", got)
		}
	})

	t.Run("derived from workspace name", func(t *testing.T) {
		cmd := newTestCmd()
		cmd.SetArgs([]string{})
		_ = cmd.Execute()

		got := resolveIssueSilent(cmd, "feature/PROJ-999_some-slug")
		if got != "PROJ-999" {
			t.Errorf("resolveIssueSilent() = %q, want %q", got, "PROJ-999")
		}
	})

	t.Run("flag takes precedence over workspace derivation", func(t *testing.T) {
		cmd := newTestCmd()
		cmd.SetArgs([]string{"--issue", "PROJ-FLAG"})
		_ = cmd.Execute()

		got := resolveIssueSilent(cmd, "feature/PROJ-999_some-slug")
		if got != "PROJ-FLAG" {
			t.Errorf("resolveIssueSilent() = %q, want %q", got, "PROJ-FLAG")
		}
	})
}

func TestSortIssuesByStatus(t *testing.T) {
	viper.Reset()
	t.Cleanup(func() { viper.Reset() })

	t.Run("sorts by lifecycle sequence", func(t *testing.T) {
		issues := []issue.Issue{
			{Key: "P-1", Status: "Done"},
			{Key: "P-2", Status: "In Progress"},
			{Key: "P-3", Status: "Ready"},
			{Key: "P-4", Status: "Review"},
			{Key: "P-5", Status: "Ready for Release"},
			{Key: "P-6", Status: "In Preview Env"},
		}
		sortIssuesByStatus(issues)

		want := []string{"P-3", "P-2", "P-4", "P-6", "P-5", "P-1"}
		got := make([]string, len(issues))
		for i, iss := range issues {
			got[i] = iss.Key
		}
		if !equalSlices(got, want) {
			t.Errorf("sort order = %v, want %v", got, want)
		}
	})

	t.Run("unknown statuses sort to end", func(t *testing.T) {
		issues := []issue.Issue{
			{Key: "P-1", Status: "Custom Status"},
			{Key: "P-2", Status: "In Progress"},
			{Key: "P-3", Status: "Another Custom"},
		}
		sortIssuesByStatus(issues)

		want := []string{"P-2", "P-1", "P-3"}
		got := make([]string, len(issues))
		for i, iss := range issues {
			got[i] = iss.Key
		}
		if !equalSlices(got, want) {
			t.Errorf("sort order = %v, want %v", got, want)
		}
	})

	t.Run("case insensitive", func(t *testing.T) {
		issues := []issue.Issue{
			{Key: "P-1", Status: "review"},
			{Key: "P-2", Status: "in progress"},
		}
		sortIssuesByStatus(issues)

		want := []string{"P-2", "P-1"}
		got := make([]string, len(issues))
		for i, iss := range issues {
			got[i] = iss.Key
		}
		if !equalSlices(got, want) {
			t.Errorf("sort order = %v, want %v", got, want)
		}
	})

	t.Run("stable sort preserves order within same status", func(t *testing.T) {
		issues := []issue.Issue{
			{Key: "P-1", Status: "In Progress"},
			{Key: "P-2", Status: "In Progress"},
			{Key: "P-3", Status: "In Progress"},
		}
		sortIssuesByStatus(issues)

		want := []string{"P-1", "P-2", "P-3"}
		got := make([]string, len(issues))
		for i, iss := range issues {
			got[i] = iss.Key
		}
		if !equalSlices(got, want) {
			t.Errorf("sort order = %v, want %v", got, want)
		}
	})

	t.Run("empty slice", func(t *testing.T) {
		var issues []issue.Issue
		sortIssuesByStatus(issues) // should not panic
	})
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSortIssuesByBoard(t *testing.T) {
	columns := []issue.BoardColumn{
		{Name: "Ready", StatusIDs: []string{"10219", "10210"}},
		{Name: "In Progress", StatusIDs: []string{"3"}},
		{Name: "Review", StatusIDs: []string{"10003"}},
		{Name: "Done", StatusIDs: []string{"10002"}},
	}

	t.Run("sorts by column order", func(t *testing.T) {
		issues := []issue.Issue{
			{Key: "P-1", StatusID: "10002"}, // Done
			{Key: "P-2", StatusID: "3"},     // In Progress
			{Key: "P-3", StatusID: "10219"}, // Ready
			{Key: "P-4", StatusID: "10210"}, // Ready (Product Backlog)
			{Key: "P-5", StatusID: "10003"}, // Review
		}
		sortIssuesByBoard(issues, columns)

		want := []string{"P-3", "P-4", "P-2", "P-5", "P-1"}
		got := make([]string, len(issues))
		for i, iss := range issues {
			got[i] = iss.Key
		}
		if !equalSlices(got, want) {
			t.Errorf("sort order = %v, want %v", got, want)
		}
	})

	t.Run("unknown status IDs sort to end", func(t *testing.T) {
		issues := []issue.Issue{
			{Key: "P-1", StatusID: "9999"}, // unknown
			{Key: "P-2", StatusID: "3"},    // In Progress
		}
		sortIssuesByBoard(issues, columns)

		want := []string{"P-2", "P-1"}
		got := make([]string, len(issues))
		for i, iss := range issues {
			got[i] = iss.Key
		}
		if !equalSlices(got, want) {
			t.Errorf("sort order = %v, want %v", got, want)
		}
	})

	t.Run("stable sort preserves order within same column", func(t *testing.T) {
		issues := []issue.Issue{
			{Key: "P-1", StatusID: "10219"}, // Ready
			{Key: "P-2", StatusID: "10210"}, // Ready (Product Backlog)
			{Key: "P-3", StatusID: "10219"}, // Ready
		}
		sortIssuesByBoard(issues, columns)

		want := []string{"P-1", "P-3", "P-2"}
		got := make([]string, len(issues))
		for i, iss := range issues {
			got[i] = iss.Key
		}
		if !equalSlices(got, want) {
			t.Errorf("sort order = %v, want %v", got, want)
		}
	})
}

func TestBuildColumnNameIndex(t *testing.T) {
	t.Run("maps status IDs to column names", func(t *testing.T) {
		columns := []issue.BoardColumn{
			{Name: "Ready", StatusIDs: []string{"10219", "10210"}},
			{Name: "In Progress", StatusIDs: []string{"3"}},
		}
		idx := buildColumnNameIndex(columns)

		if idx["10219"] != "Ready" {
			t.Errorf("idx[10219] = %q, want %q", idx["10219"], "Ready")
		}
		if idx["10210"] != "Ready" {
			t.Errorf("idx[10210] = %q, want %q", idx["10210"], "Ready")
		}
		if idx["3"] != "In Progress" {
			t.Errorf("idx[3] = %q, want %q", idx["3"], "In Progress")
		}
	})

	t.Run("nil for empty columns", func(t *testing.T) {
		idx := buildColumnNameIndex(nil)
		if idx != nil {
			t.Errorf("expected nil, got %v", idx)
		}
	})
}

func TestDisplayStatus(t *testing.T) {
	colNames := map[string]string{
		"10219": "Ready",
		"10210": "Ready",
	}

	t.Run("returns column name when mapped", func(t *testing.T) {
		iss := issue.Issue{Status: "Product Backlog", StatusID: "10210"}
		got := displayStatus(iss, colNames)
		if got != "Ready" {
			t.Errorf("displayStatus() = %q, want %q", got, "Ready")
		}
	})

	t.Run("falls back to status name", func(t *testing.T) {
		iss := issue.Issue{Status: "In Progress", StatusID: "3"}
		got := displayStatus(iss, colNames)
		if got != "In Progress" {
			t.Errorf("displayStatus() = %q, want %q", got, "In Progress")
		}
	})

	t.Run("falls back when colNames is nil", func(t *testing.T) {
		iss := issue.Issue{Status: "Ready", StatusID: "10219"}
		got := displayStatus(iss, nil)
		if got != "Ready" {
			t.Errorf("displayStatus() = %q, want %q", got, "Ready")
		}
	})
}

func TestExtractIssue(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"PROJ-123", "PROJ-123"},
		{"feature/PROJ-123_add-widget", "PROJ-123"},
		{"fix/CS-42_broken-auth", "CS-42"},
		{"main", ""},
		{"develop", ""},
		{"", ""},
		{"feature/no-ticket-here", ""},
		{"ABC-1", "ABC-1"},
		{"A-1", ""}, // single letter prefix — not a match
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := extractIssue(tt.input)
			if got != tt.want {
				t.Errorf("extractIssue(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestExtractIssueCustomPattern covers the two-step resolution:
// issue_tracker.issue_pattern when set, the configured tracker provider's own
// grammar otherwise. bosun holds no key-shape default of its own — the
// "falls back" cases below land on the provider's grammar, which for the
// configured Jira tracker is the PROJ-123 shape.
func TestExtractIssueCustomPattern(t *testing.T) {
	t.Run("custom pattern overrides the provider grammar", func(t *testing.T) {
		viper.Reset()
		viper.Set("issue_tracker.issue_pattern", `(#\d+)`)
		t.Cleanup(func() { viper.Reset() })

		got := extractIssue("feature/#42_add-widget")
		if got != "#42" {
			t.Errorf("extractIssue() = %q, want %q", got, "#42")
		}

		// The provider's grammar must not also apply once overridden.
		got = extractIssue("PROJ-123")
		if got != "" {
			t.Errorf("extractIssue() = %q, want %q", got, "")
		}
	})

	t.Run("unregistered provider parses nothing", func(t *testing.T) {
		// No override and no grammar to fall back on: extraction yields
		// nothing rather than guessing a key shape.
		viper.Reset()
		viper.Set("issue_tracker.provider", "linear")
		t.Cleanup(func() { viper.Reset() })

		if got := extractIssue("feature/PROJ-123_x"); got != "" {
			t.Errorf("extractIssue() = %q, want empty", got)
		}
	})

	t.Run("invalid pattern falls back to the provider grammar", func(t *testing.T) {
		viper.Reset()
		viper.Set("issue_tracker.issue_pattern", `([invalid`)
		t.Cleanup(func() { viper.Reset() })

		got := extractIssue("PROJ-123")
		if got != "PROJ-123" {
			t.Errorf("extractIssue() = %q, want %q", got, "PROJ-123")
		}
	})

	t.Run("no pattern uses the provider grammar", func(t *testing.T) {
		viper.Reset()
		t.Cleanup(func() { viper.Reset() })

		got := extractIssue("feature/PROJ-123_foo")
		if got != "PROJ-123" {
			t.Errorf("extractIssue() = %q, want %q", got, "PROJ-123")
		}
	})

	t.Run("lowercase pattern", func(t *testing.T) {
		viper.Reset()
		viper.Set("issue_tracker.issue_pattern", `(proj-\d+)`)
		t.Cleanup(func() { viper.Reset() })

		got := extractIssue("feature/proj-99_stuff")
		if got != "proj-99" {
			t.Errorf("extractIssue() = %q, want %q", got, "proj-99")
		}
	})
}

// sortIssues only picks between the two strategies, both of which are
// covered directly above. What is untested is the choice itself.
//
// Three issues, in an input order that matches neither the board order nor
// the lifecycle order — so every subtest fails if the wrong strategy runs,
// and also if nothing sorts at all. Two issues would not do: the orderings
// here are exact reverses, so a two-element input necessarily equals one of
// them and that subtest passes without anything having to sort.
func TestSortIssuesDispatch(t *testing.T) {
	viper.Reset()
	t.Cleanup(func() { viper.Reset() })

	newIssues := func() []issue.Issue {
		return []issue.Issue{
			{Key: "P-A", Status: "In Progress", StatusID: "inprog"},
			{Key: "P-B", Status: "Done", StatusID: "done"},
			{Key: "P-C", Status: "Ready", StatusID: "ready"},
		}
	}
	// Board order deliberately contradicts the lifecycle sequence.
	columns := []issue.BoardColumn{
		{Name: "Done", StatusIDs: []string{"done"}},
		{Name: "Ready", StatusIDs: []string{"ready"}},
		{Name: "In Progress", StatusIDs: []string{"inprog"}},
	}
	var (
		inputOrder     = []string{"P-A", "P-B", "P-C"}
		boardOrder     = []string{"P-B", "P-C", "P-A"}
		lifecycleOrder = []string{"P-C", "P-A", "P-B"}
	)
	keys := func(issues []issue.Issue) []string {
		out := make([]string, len(issues))
		for i, iss := range issues {
			out[i] = iss.Key
		}
		return out
	}

	// Guard the premise: if these ever coincide, the subtests below stop
	// discriminating and would pass for the wrong reason.
	if equalSlices(boardOrder, lifecycleOrder) ||
		equalSlices(inputOrder, boardOrder) ||
		equalSlices(inputOrder, lifecycleOrder) {
		t.Fatalf("fixture no longer distinguishes the strategies: input=%v board=%v lifecycle=%v",
			inputOrder, boardOrder, lifecycleOrder)
	}

	t.Run("board columns present take precedence", func(t *testing.T) {
		issues := newIssues()
		sortIssues(issues, columns)
		if !equalSlices(keys(issues), boardOrder) {
			t.Errorf("order = %v, want %v (board order)", keys(issues), boardOrder)
		}
	})

	t.Run("no columns falls back to lifecycle status", func(t *testing.T) {
		issues := newIssues()
		sortIssues(issues, nil)
		if !equalSlices(keys(issues), lifecycleOrder) {
			t.Errorf("order = %v, want %v (lifecycle order)", keys(issues), lifecycleOrder)
		}
	})

	t.Run("empty column slice falls back too", func(t *testing.T) {
		issues := newIssues()
		sortIssues(issues, []issue.BoardColumn{})
		if !equalSlices(keys(issues), lifecycleOrder) {
			t.Errorf("order = %v, want %v (lifecycle order)", keys(issues), lifecycleOrder)
		}
	})
}
