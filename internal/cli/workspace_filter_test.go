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

// TestResolveWorkspaceScope locks the shared scope grammar: --all is
// mutually exclusive with single-workspace targeting, and filters
// require project scope — implicit (status outside a workspace) or
// explicit (--all).
func TestResolveWorkspaceScope(t *testing.T) {
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

	t.Run("all conflicts with workspace", func(t *testing.T) {
		cmd := newCmd(map[string]string{"all": "true", "workspace": "ws"})
		_, err := resolveWorkspaceScope(cmd, false, workspaceQuery{})
		if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
			t.Errorf("err = %v, want the mutual-exclusion refusal", err)
		}
	})

	t.Run("all conflicts with issue", func(t *testing.T) {
		cmd := newCmd(map[string]string{"all": "true", "issue": "EX-1"})
		_, err := resolveWorkspaceScope(cmd, false, workspaceQuery{})
		if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
			t.Errorf("err = %v, want the mutual-exclusion refusal", err)
		}
	})

	t.Run("filters without project scope error toward --all", func(t *testing.T) {
		cmd := newCmd(nil)
		_, err := resolveWorkspaceScope(cmd, false, workspaceQuery{statuses: []string{"done"}})
		if err == nil || !strings.Contains(err.Error(), "--all") {
			t.Errorf("err = %v, want the pass---all guidance", err)
		}
	})

	t.Run("filters ride implicit project scope", func(t *testing.T) {
		cmd := newCmd(nil)
		project, err := resolveWorkspaceScope(cmd, true, workspaceQuery{statuses: []string{"done"}})
		if err != nil || !project {
			t.Errorf("(project, err) = (%v, %v), want (true, nil)", project, err)
		}
	})

	t.Run("all alone is project scope", func(t *testing.T) {
		cmd := newCmd(map[string]string{"all": "true"})
		project, err := resolveWorkspaceScope(cmd, false, workspaceQuery{})
		if err != nil || !project {
			t.Errorf("(project, err) = (%v, %v), want (true, nil)", project, err)
		}
	})

	t.Run("no flags passes the implicit scope through", func(t *testing.T) {
		for _, implicit := range []bool{true, false} {
			cmd := newCmd(nil)
			project, err := resolveWorkspaceScope(cmd, implicit, workspaceQuery{})
			if err != nil || project != implicit {
				t.Errorf("implicit=%v: (project, err) = (%v, %v), want (%v, nil)", implicit, project, err, implicit)
			}
		}
	})
}
