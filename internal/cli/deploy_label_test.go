package cli

import (
	"context"
	"os/exec"
	"strings"
	"testing"
)

// gitRepoWithRemote creates a git repository whose origin is the given
// GitHub-shaped URL, which is what code.ParseRemote reads.
func gitRepoWithRemote(t *testing.T, origin string) Repository {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"remote", "add", "origin", origin},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return Repository{Name: "api", Path: dir}
}

// TestDeployLabel covers the plan row's name for a deploy target.
//
// The label is the ONLY part of a DeployTarget the plan renders, and
// the plan is the approval gate for a production deploy. Since
// cicd.workflows.release.target became repo-scoped, an absolute
// workflow path can arrive from a file committed to the repository
// rather than from the central config the operator wrote — so a row
// reading `api` must not be able to mean "dispatch into someone else's
// repository".
func TestDeployLabel(t *testing.T) {
	ctx := context.Background()

	t.Run("a workflow in the repo's own repo is not annotated", func(t *testing.T) {
		repo := gitRepoWithRemote(t, "git@github.com:acme/api.git")
		wt := WorkflowTarget{Owner: "acme", Repo: "api", Workflow: "deploy.yaml"}

		if got := deployLabel(ctx, repo, wt, "api"); got != "api" {
			t.Errorf("label = %q, want the bare local name for a local deploy", got)
		}
	})

	t.Run("owner and name compare case-insensitively", func(t *testing.T) {
		// GitHub treats them that way, so a remote spelled Acme/API and
		// a target spelled acme/api are the same repository. Reporting
		// that as cross-repo would cry wolf on every such project and
		// teach the operator to ignore the annotation.
		repo := gitRepoWithRemote(t, "git@github.com:Acme/API.git")
		wt := WorkflowTarget{Owner: "acme", Repo: "api", Workflow: "deploy.yaml"}

		if got := deployLabel(ctx, repo, wt, "api"); got != "api" {
			t.Errorf("label = %q, want no annotation for a case-different spelling", got)
		}
	})

	t.Run("a workflow in another repo is named", func(t *testing.T) {
		repo := gitRepoWithRemote(t, "git@github.com:acme/api.git")
		wt := WorkflowTarget{Owner: "elsewhere", Repo: "infra", Workflow: "deploy.yaml"}

		got := deployLabel(ctx, repo, wt, "api")
		if !strings.Contains(got, "elsewhere/infra") {
			t.Errorf("label = %q, want it to disclose the dispatch destination", got)
		}
		if !strings.HasPrefix(got, "api") {
			t.Errorf("label = %q, want the local name kept as the row's identity", got)
		}
	})

	t.Run("the per-service label keeps its service", func(t *testing.T) {
		repo := gitRepoWithRemote(t, "git@github.com:acme/api.git")
		wt := WorkflowTarget{Owner: "elsewhere", Repo: "infra", Workflow: "deploy.yaml"}

		got := deployLabel(ctx, repo, wt, "api · billing")
		if !strings.HasPrefix(got, "api · billing") || !strings.Contains(got, "elsewhere/infra") {
			t.Errorf("label = %q, want both the service and the destination", got)
		}
	})

	// Only a PROVEN mismatch annotates. Firing on "couldn't tell" would
	// decorate every row in a repo whose remote won't parse — most of
	// them pointing at that same repo — and a warning that goes off on
	// ordinary configuration is one operators learn to read past.
	//
	// It costs nothing against the case that motivates the annotation: a
	// descriptor redirecting a dispatch lives in a normal repository
	// with a working remote, while a repo whose origin can't be read has
	// no pushed branch, no merged PR and no release tag, so it never
	// reaches the deploy plan.
	t.Run("an unreadable remote does not annotate", func(t *testing.T) {
		repo := Repository{Name: "api", Path: t.TempDir()} // not a git repo
		wt := WorkflowTarget{Owner: "acme", Repo: "api", Workflow: "deploy.yaml"}

		if got := deployLabel(ctx, repo, wt, "api"); got != "api" {
			t.Errorf("label = %q, want the bare local name when the remote can't be read", got)
		}
	})
}
