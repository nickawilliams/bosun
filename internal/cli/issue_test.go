package cli

import (
	"os"
	"testing"

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
