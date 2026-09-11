package cli

import (
	"strings"
	"testing"

	issuepkg "github.com/nickawilliams/bosun/internal/issue"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// TestResolveWorkspaceQuery locks the filter-flag resolution contract:
// commands without the flags get the match-everything zero query, and
// --status values are validated against the lifecycle vocabulary up
// front rather than silently matching nothing later.
func TestResolveWorkspaceQuery(t *testing.T) {
	t.Run("no filter flags registered resolves the zero query", func(t *testing.T) {
		cmd := &cobra.Command{Use: "t"}
		q, err := resolveWorkspaceQuery(cmd)
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if q.active() {
			t.Errorf("query = %+v, want inactive zero query", q)
		}
	})

	t.Run("valid lifecycle keys resolve", func(t *testing.T) {
		cmd := &cobra.Command{Use: "t"}
		addWorkspaceFilterFlags(cmd)
		if err := cmd.Flags().Set("status", "done,review"); err != nil {
			t.Fatalf("set flag: %v", err)
		}
		q, err := resolveWorkspaceQuery(cmd)
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if len(q.statuses) != 2 || q.statuses[0] != "done" || q.statuses[1] != "review" {
			t.Errorf("statuses = %v, want [done review]", q.statuses)
		}
	})

	t.Run("unknown key errors and names the vocabulary", func(t *testing.T) {
		cmd := &cobra.Command{Use: "t"}
		addWorkspaceFilterFlags(cmd)
		if err := cmd.Flags().Set("status", "Done"); err != nil {
			t.Fatalf("set flag: %v", err)
		}
		_, err := resolveWorkspaceQuery(cmd)
		if err == nil {
			t.Fatal("err = nil, want the unknown-key rejection (raw tracker names aren't the vocabulary)")
		}
		if !strings.Contains(err.Error(), `"Done"`) || !strings.Contains(err.Error(), "done") {
			t.Errorf("err = %v, want it to name the bad key and the valid vocabulary", err)
		}
	})
}

// TestWorkspaceQueryMatch locks the evaluation semantics — in
// particular the no-silent-drop contract: a workspace the query
// cannot judge is a non-match WITH a reason, distinct from an
// evaluated non-match, so callers report it instead of dropping it.
func TestWorkspaceQueryMatch(t *testing.T) {
	// Status names resolve through config with schema defaults as
	// fallback; pin the mappings explicitly so the assertions don't
	// depend on defaults staying stable.
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("issue_tracker.statuses.done", "Done")
	viper.Set("issue_tracker.statuses.in_progress", "In Progress")

	done := workspaceQuery{statuses: []string{"done"}}

	tests := []struct {
		name       string
		query      workspaceQuery
		ws         workspaceState
		want       bool
		wantReason string // "" asserts an empty reason
	}{
		{
			name:  "inactive query matches anything",
			query: workspaceQuery{},
			ws:    workspaceState{name: "whatever"},
			want:  true,
		},
		{
			name:  "status match",
			query: done,
			ws:    workspaceState{issueKey: "EX-1", issue: issuepkg.Issue{Status: "Done"}},
			want:  true,
		},
		{
			name:  "status match is case-insensitive",
			query: done,
			ws:    workspaceState{issueKey: "EX-1", issue: issuepkg.Issue{Status: "done"}},
			want:  true,
		},
		{
			name:  "evaluated non-match carries no reason",
			query: done,
			ws:    workspaceState{issueKey: "EX-1", issue: issuepkg.Issue{Status: "In Progress"}},
			want:  false,
		},
		{
			name:       "no issue key is unevaluable",
			query:      done,
			ws:         workspaceState{name: "scratch"},
			want:       false,
			wantReason: "no issue key",
		},
		{
			name:       "missing status is unevaluable",
			query:      done,
			ws:         workspaceState{issueKey: "EX-1"},
			want:       false,
			wantReason: "status unknown",
		},
		{
			name:       "unmapped status is unevaluable",
			query:      done,
			ws:         workspaceState{issueKey: "EX-1", issue: issuepkg.Issue{Status: "Weird Custom"}},
			want:       false,
			wantReason: "not mapped",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, reason := tt.query.match(tt.ws)
			if got != tt.want {
				t.Errorf("match = %v, want %v (reason %q)", got, tt.want, reason)
			}
			if tt.wantReason == "" && reason != "" {
				t.Errorf("reason = %q, want none", reason)
			}
			if tt.wantReason != "" && !strings.Contains(reason, tt.wantReason) {
				t.Errorf("reason = %q, want it to contain %q", reason, tt.wantReason)
			}
		})
	}
}

// TestResolveWorkspaceSelection locks the unified selection pipeline
// (#120): the positional pattern is the one selection grammar, the
// deprecated flags map onto it, and the unspecified-selection default
// varies only by command class. The interactive bare-picker branch
// and the issue→workspace mapping need a project on disk and are
// covered end-to-end in cleanup_test.go.
func TestResolveWorkspaceSelection(t *testing.T) {
	newCmd := func(flagValues map[string]string) *cobra.Command {
		cmd := &cobra.Command{Use: "t"}
		addWorkspaceFlag(cmd)
		addIssueFlag(cmd)
		addAllFlag(cmd)
		addWorkspaceFilterFlags(cmd)
		for name, v := range flagValues {
			if err := cmd.Flags().Set(name, v); err != nil {
				t.Fatalf("set --%s: %v", name, err)
			}
		}
		return cmd
	}

	t.Run("exact name is single mode", func(t *testing.T) {
		sel, err := resolveWorkspaceSelection(newCmd(nil), []string{"feature/EX-1_slug"}, workspaceQuery{}, selectionDestructive)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if sel.batch || sel.exact != "feature/EX-1_slug" {
			t.Errorf("sel = %+v, want single mode targeting the exact name", sel)
		}
	})

	t.Run("glob is batch mode", func(t *testing.T) {
		sel, err := resolveWorkspaceSelection(newCmd(nil), []string{"feature/*"}, workspaceQuery{}, selectionDestructive)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if !sel.batch || sel.pattern != "feature/*" {
			t.Errorf("sel = %+v, want batch over the glob", sel)
		}
	})

	t.Run("deprecated all aliases the everything pattern", func(t *testing.T) {
		sel, err := resolveWorkspaceSelection(newCmd(map[string]string{"all": "true"}), nil, workspaceQuery{}, selectionDestructive)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if !sel.batch || sel.pattern != "**" {
			t.Errorf("sel = %+v, want batch over '**'", sel)
		}
	})

	t.Run("all conflicts with a pattern", func(t *testing.T) {
		_, err := resolveWorkspaceSelection(newCmd(map[string]string{"all": "true"}), []string{"**"}, workspaceQuery{}, selectionDestructive)
		if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
			t.Errorf("err = %v, want the mutual-exclusion refusal", err)
		}
	})

	t.Run("pattern conflicts with workspace flag", func(t *testing.T) {
		_, err := resolveWorkspaceSelection(newCmd(map[string]string{"workspace": "ws"}), []string{"**"}, workspaceQuery{}, selectionDestructive)
		if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
			t.Errorf("err = %v, want the mutual-exclusion refusal", err)
		}
	})

	t.Run("pattern conflicts with issue flag", func(t *testing.T) {
		_, err := resolveWorkspaceSelection(newCmd(map[string]string{"issue": "EX-1"}), []string{"**"}, workspaceQuery{}, selectionDestructive)
		if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
			t.Errorf("err = %v, want the mutual-exclusion refusal", err)
		}
	})

	t.Run("filter with no pattern implies batch scope", func(t *testing.T) {
		sel, err := resolveWorkspaceSelection(newCmd(nil), nil, workspaceQuery{statuses: []string{"done"}}, selectionDestructive)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if !sel.batch || sel.pattern != "**" {
			t.Errorf("sel = %+v, want batch over '**'", sel)
		}
	})

	t.Run("exact name with a filter routes through batch", func(t *testing.T) {
		// The filter must still apply to an exact-name selection
		// (pattern selects the namespace — here a namespace of one —
		// filter selects by lifecycle), so the exact name becomes a
		// batch pattern instead of a filter-dropping single target.
		sel, err := resolveWorkspaceSelection(newCmd(nil), []string{"feature/EX-1_slug"}, workspaceQuery{statuses: []string{"done"}}, selectionDestructive)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if !sel.batch || sel.pattern != "feature/EX-1_slug" {
			t.Errorf("sel = %+v, want batch over the exact name", sel)
		}
	})

	t.Run("filter conflicts with issue flag", func(t *testing.T) {
		// An explicit single-target flag combined with a population
		// filter is refused: silently sweeping the project past an
		// explicitly named target would be a destructive surprise.
		_, err := resolveWorkspaceSelection(newCmd(map[string]string{"issue": "EX-1"}), nil, workspaceQuery{statuses: []string{"done"}}, selectionDestructive)
		if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
			t.Errorf("err = %v, want the mutual-exclusion refusal", err)
		}
	})

	t.Run("filter conflicts with workspace flag", func(t *testing.T) {
		_, err := resolveWorkspaceSelection(newCmd(map[string]string{"workspace": "ws"}), nil, workspaceQuery{statuses: []string{"done"}}, selectionDestructive)
		if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
			t.Errorf("err = %v, want the mutual-exclusion refusal", err)
		}
	})

	t.Run("all alias conflicts name the alias", func(t *testing.T) {
		// The conflict message must blame --all, not a pattern the
		// user never typed.
		_, err := resolveWorkspaceSelection(newCmd(map[string]string{"all": "true", "workspace": "ws"}), nil, workspaceQuery{}, selectionDestructive)
		if err == nil || !strings.Contains(err.Error(), "--all") {
			t.Errorf("err = %v, want it to name --all as the conflicting selection", err)
		}
	})

	t.Run("bare destructive errors non-interactively", func(t *testing.T) {
		// go test's stdin is not a TTY, so this exercises the
		// non-interactive default: destructive commands refuse to
		// guess a selection.
		_, err := resolveWorkspaceSelection(newCmd(nil), nil, workspaceQuery{}, selectionDestructive)
		if err == nil || !strings.Contains(err.Error(), "pattern") {
			t.Errorf("err = %v, want the specify-a-pattern refusal", err)
		}
	})

	t.Run("bare read-only selects everything", func(t *testing.T) {
		sel, err := resolveWorkspaceSelection(newCmd(nil), nil, workspaceQuery{}, selectionReadOnly)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if !sel.batch || sel.pattern != "**" {
			t.Errorf("sel = %+v, want batch over '**'", sel)
		}
	})

	t.Run("empty pattern is refused", func(t *testing.T) {
		_, err := resolveWorkspaceSelection(newCmd(nil), []string{""}, workspaceQuery{}, selectionDestructive)
		if err == nil || !strings.Contains(err.Error(), "empty workspace pattern") {
			t.Errorf("err = %v, want the empty-pattern refusal", err)
		}
	})
}
