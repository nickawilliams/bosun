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
