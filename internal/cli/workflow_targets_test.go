package cli

// Unit scenarios for resolveWorkflowTargets' two config shapes, and in
// particular for the one answer it gives only at project scope.
//
// The per-repo shape means "the workflow for each of THIS workspace's
// repos", so it has no project-scoped answer — the set of repositories
// it covers differs between workspaces. `bosun doctor` runs at project
// scope by design and needs that stated as its own condition rather
// than as a workspace-manager error, which is what workspaceRequiredError
// carries. These tests pin the seam; doctor's own scenarios cover how it
// renders.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/preview"
	"github.com/spf13/viper"
)

// setWorkflowConfig points viper at a single sub-stage's workflow value,
// the way a parsed config file would.
func setWorkflowConfig(t *testing.T, value any) {
	t.Helper()
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("preview.up.workflow", value)
}

func TestResolveWorkflowTargetsGlobalModeNeedsNoWorkspace(t *testing.T) {
	// A single workflow path covers the whole project, so resolving it
	// is string parsing. This is the shape doctor can settle
	// completely, and the reason the per-repo refusal below is scoped
	// to the other one rather than to project scope generally.
	setWorkflowConfig(t, "acme/infra/.github/workflows/up.yml")

	targets, err := resolveWorkflowTargets(context.Background(), "", "preview.up")
	if err != nil {
		t.Fatalf("global mode at project scope: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("targets = %d, want 1", len(targets))
	}
	if targets[0].Owner != "acme" || targets[0].Repo != "infra" ||
		targets[0].Workflow != "up.yml" {
		t.Errorf("target = %+v, want acme/infra up.yml", targets[0])
	}
}

func TestResolveWorkflowTargetsPerRepoRefusesProjectScope(t *testing.T) {
	setWorkflowConfig(t, map[string]any{
		"web": "acme/infra/.github/workflows/web.yml",
		"api": "acme/infra/.github/workflows/api.yml",
	})

	targets, err := resolveWorkflowTargets(context.Background(), "", "preview.up")
	if targets != nil {
		t.Errorf("targets = %v, want none alongside the refusal", targets)
	}

	var needsWorkspace *workspaceRequiredError
	if !errors.As(err, &needsWorkspace) {
		t.Fatalf("err = %v, want a *workspaceRequiredError", err)
	}

	// The key is what the caller reports and the repos are what a
	// project-scoped caller checks in place of the intersection, so
	// both have to survive the trip.
	if needsWorkspace.Key != "preview.up.workflow" {
		t.Errorf("Key = %q, want preview.up.workflow", needsWorkspace.Key)
	}
	// Sorted, not map order: these names reach rendered output, and Go
	// randomizes map iteration.
	if got := strings.Join(needsWorkspace.Repos, ","); got != "api,web" {
		t.Errorf("Repos = %q, want %q", got, "api,web")
	}

	// The message stands on its own for any caller that renders it
	// without unwrapping — it names the key and says what is missing,
	// rather than blaming a workspace the caller never asked about.
	msg := needsWorkspace.Error()
	if !strings.Contains(msg, "preview.up.workflow") || !strings.Contains(msg, "workspace") {
		t.Errorf("Error() = %q, want it to name the key and the missing workspace", msg)
	}
}

// TestPreviewPerRepoNoteRefusesAnEmptyMap covers the guard a config
// file cannot currently reach: viper drops an empty YAML mapping, so
// `workflow: {}` arrives as an absent key and takes the unwired arm
// instead (pinned in doctor's own scenarios). The guard stays because
// the shape is legal for the sentinel to carry — a non-YAML config
// source or a programmatic Set produces it — and without it a stage
// that dispatches nowhere would render "per-repo · 0 repos" and pass
// as healthy.
func TestPreviewPerRepoNoteRefusesAnEmptyMap(t *testing.T) {
	setWorkflowConfig(t, map[string]any{})

	_, err := resolveWorkflowTargets(context.Background(), "", "preview.up")

	var needsWorkspace *workspaceRequiredError
	if !errors.As(err, &needsWorkspace) {
		t.Fatalf("err = %v, want a *workspaceRequiredError", err)
	}
	if len(needsWorkspace.Repos) != 0 {
		t.Fatalf("Repos = %v, want none", needsWorkspace.Repos)
	}

	note, healthy := previewPerRepoNote(needsWorkspace, preview.OpCreate)
	if healthy {
		t.Error("an empty per-repo map reported healthy; it dispatches nowhere")
	}
	if !strings.Contains(note, "no repositories configured") {
		t.Errorf("note = %q, want it to say no repositories are configured", note)
	}
}

func TestResolveWorkflowTargetsUnconfiguredStageIsNotAnError(t *testing.T) {
	// An absent stage is "nothing wired here", which the adapter turns
	// into its own not-configured report. Answering with an error would
	// make an unconfigured half indistinguishable from a broken one.
	setWorkflowConfig(t, nil)

	targets, err := resolveWorkflowTargets(context.Background(), "", "preview.up")
	if err != nil || targets != nil {
		t.Errorf("targets, err = %v, %v; want nil, nil", targets, err)
	}
}
