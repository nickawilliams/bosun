package cli

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func newContextTestCmd(flags ...func(*cobra.Command)) *cobra.Command {
	cmd := &cobra.Command{
		Use:  "test",
		RunE: func(cmd *cobra.Command, args []string) error { return nil },
	}
	for _, f := range flags {
		f(cmd)
	}
	return cmd
}

func TestResolveCommandContext(t *testing.T) {
	t.Run("skips stages without flags", func(t *testing.T) {
		cmd := newContextTestCmd() // no flags
		cmd.SetArgs([]string{})
		_ = cmd.Execute()

		cc, err := resolveCommandContext(cmd)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cc.Project != "" || cc.Workspace != "" || cc.Issue != "" {
			t.Errorf("expected all empty, got %+v", cc)
		}
	})

	t.Run("issue derived from workspace name", func(t *testing.T) {
		cmd := newContextTestCmd(addWorkspaceFlag, addIssueFlag)
		cmd.SetArgs([]string{"--workspace", "feature/EX-31432_put-provider"})
		_ = cmd.Execute()

		cc, err := resolveCommandContext(cmd)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cc.Workspace != "feature/EX-31432_put-provider" {
			t.Errorf("Workspace = %q, want %q", cc.Workspace, "feature/EX-31432_put-provider")
		}
		if cc.Issue != "EX-31432" {
			t.Errorf("Issue = %q, want %q (derived from workspace)", cc.Issue, "EX-31432")
		}
	})

	t.Run("issue flag takes precedence over workspace derivation", func(t *testing.T) {
		cmd := newContextTestCmd(addWorkspaceFlag, addIssueFlag)
		cmd.SetArgs([]string{"--workspace", "feature/EX-31432_foo", "--issue", "PROJ-999"})
		_ = cmd.Execute()

		cc, err := resolveCommandContext(cmd)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cc.Issue != "PROJ-999" {
			t.Errorf("Issue = %q, want %q (flag should beat derivation)", cc.Issue, "PROJ-999")
		}
	})

	t.Run("issue from env takes precedence over workspace derivation", func(t *testing.T) {
		viper.Reset()
		viper.SetEnvPrefix("BOSUN")
		viper.AutomaticEnv()
		t.Setenv("BOSUN_ISSUE", "ENV-123")
		t.Cleanup(viper.Reset)

		cmd := newContextTestCmd(addWorkspaceFlag, addIssueFlag)
		cmd.SetArgs([]string{"--workspace", "feature/EX-31432_foo"})
		_ = cmd.Execute()

		cc, err := resolveCommandContext(cmd)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cc.Issue != "ENV-123" {
			t.Errorf("Issue = %q, want %q (env should beat derivation)", cc.Issue, "ENV-123")
		}
	})

	t.Run("workspace without issue key leaves issue empty", func(t *testing.T) {
		cmd := newContextTestCmd(addWorkspaceFlag, addIssueFlag)
		cmd.SetArgs([]string{"--workspace", "feature/no-ticket-here"})
		_ = cmd.Execute()

		cc, err := resolveCommandContext(cmd)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cc.Issue != "" {
			t.Errorf("Issue = %q, want empty (no issue key in workspace name)", cc.Issue)
		}
	})

	t.Run("issue stage skipped without flag", func(t *testing.T) {
		cmd := newContextTestCmd(addWorkspaceFlag) // workspace but no issue flag
		cmd.SetArgs([]string{"--workspace", "feature/EX-31432_foo"})
		_ = cmd.Execute()

		cc, err := resolveCommandContext(cmd)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cc.Workspace != "feature/EX-31432_foo" {
			t.Errorf("Workspace = %q, want %q", cc.Workspace, "feature/EX-31432_foo")
		}
		if cc.Issue != "" {
			t.Errorf("Issue = %q, want empty (no issue flag registered)", cc.Issue)
		}
	})
}

func TestRequireIssue(t *testing.T) {
	t.Run("no-op when issue present", func(t *testing.T) {
		cc := CommandContext{Issue: "PROJ-123"}
		if err := cc.RequireIssue(); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("errors when empty in non-interactive", func(t *testing.T) {
		cc := CommandContext{}
		// pickOrPromptIssue returns "" in non-interactive mode.
		err := cc.RequireIssue()
		if err == nil {
			t.Error("expected error, got nil")
		}
	})
}
