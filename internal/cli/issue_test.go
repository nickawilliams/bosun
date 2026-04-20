package cli

import (
	"os"
	"testing"

	"github.com/nickawilliams/bosun/internal/issue"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newTestCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use: "test",
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}
	addIssueFlag(cmd)
	return cmd
}

func TestResolveIssue(t *testing.T) {
	t.Run("from flag", func(t *testing.T) {
		cmd := newTestCmd()
		cmd.SetArgs([]string{"--issue", "PROJ-123"})
		cmd.Execute()

		got, err := resolveIssue(cmd)
		if err != nil {
			t.Fatalf("resolveIssue() error: %v", err)
		}
		if got != "PROJ-123" {
			t.Errorf("resolveIssue() = %q, want %q", got, "PROJ-123")
		}
	})

	t.Run("from short flag", func(t *testing.T) {
		cmd := newTestCmd()
		cmd.SetArgs([]string{"-i", "PROJ-456"})
		cmd.Execute()

		got, err := resolveIssue(cmd)
		if err != nil {
			t.Fatalf("resolveIssue() error: %v", err)
		}
		if got != "PROJ-456" {
			t.Errorf("resolveIssue() = %q, want %q", got, "PROJ-456")
		}
	})

	t.Run("from env var", func(t *testing.T) {
		viper.Reset()
		viper.SetEnvPrefix("BOSUN")
		viper.AutomaticEnv()

		os.Setenv("BOSUN_ISSUE", "PROJ-789")
		t.Cleanup(func() {
			os.Unsetenv("BOSUN_ISSUE")
			viper.Reset()
		})

		cmd := newTestCmd()
		cmd.SetArgs([]string{})
		cmd.Execute()

		got, err := resolveIssue(cmd)
		if err != nil {
			t.Fatalf("resolveIssue() error: %v", err)
		}
		if got != "PROJ-789" {
			t.Errorf("resolveIssue() = %q, want %q", got, "PROJ-789")
		}
	})

	t.Run("flag takes precedence over env", func(t *testing.T) {
		os.Setenv("BOSUN_ISSUE", "PROJ-ENV")
		t.Cleanup(func() { os.Unsetenv("BOSUN_ISSUE") })

		cmd := newTestCmd()
		cmd.SetArgs([]string{"--issue", "PROJ-FLAG"})
		cmd.Execute()

		got, err := resolveIssue(cmd)
		if err != nil {
			t.Fatalf("resolveIssue() error: %v", err)
		}
		if got != "PROJ-FLAG" {
			t.Errorf("resolveIssue() = %q, want %q", got, "PROJ-FLAG")
		}
	})

	t.Run("error when not specified", func(t *testing.T) {
		os.Unsetenv("BOSUN_ISSUE")

		cmd := newTestCmd()
		cmd.SetArgs([]string{})
		cmd.Execute()

		_, err := resolveIssue(cmd)
		if err == nil {
			t.Error("resolveIssue() expected error, got nil")
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
			{Key: "P-1", Status: "done"},
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
		{"A-1", ""},  // single letter prefix — not a match
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
