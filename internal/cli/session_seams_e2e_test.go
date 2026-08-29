package cli_test

// End-to-end coverage for two seams the session-shell port touched
// that no existing suite reached: the workspace guard every lifecycle
// command opens with, and the cancel path of the gather-to-form
// selection seam each of them now embeds in the shell.

import (
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/testharness"
)

// guardConfig is the minimum project config for a command to reach its
// own body: repositories and a workspace root, and nothing else — the
// workspace guard fires before any provider, host, or tracker is built.
const guardConfig = `
workspace:
  repositories:
    - "repos/*"
  root: "workspaces"
issue_tracker:
  project: "EX"
`

// TestLifecycleCommandsRequireWorkspace locks the guard every
// workspace-scoped lifecycle command opens with. With no workspace to
// resolve, RequireWorkspace falls back to a prompt; an empty submit
// leaves it unresolved and the command must abort with the guard's
// message rather than proceeding on an empty workspace.
//
// Each command reaches this through its own body — via the session
// shell for the ported commands — so the loop also proves RunSession
// propagates an early body error unchanged.
func TestLifecycleCommandsRequireWorkspace(t *testing.T) {
	for _, name := range []string{"preview", "release", "prerelease", "review"} {
		t.Run(name, func(t *testing.T) {
			h := testharness.New(t)
			h.Workspace.WriteConfig(guardConfig)
			h.Workspace.AddRepo("api")

			// Empty submit at the fallback Workspace prompt.
			h.Type("\r")

			err := h.Run(name)
			if err == nil {
				t.Fatalf("%s with no workspace returned nil, want the guard error", name)
			}
			if !strings.Contains(err.Error(), "workspace not specified") {
				t.Fatalf("%s error = %q, want the workspace guard error", name, err)
			}
		})
	}
}

// TestSelectionFormCancelAborts locks the cancel path of the
// gather-to-form seam the port rewrote. Ctrl+c at the multi-select
// must surface as ErrCancelled and leave nothing mutated — the
// selection form is the last point a user can back out before the
// plan gate, and under the shell the form is embedded rather than a
// separate program, so its abort travels a different path than it did
// before this port.
func TestSelectionFormCancelAborts(t *testing.T) {
	t.Run("prerelease", func(t *testing.T) {
		h, api := setupReleasable(t)
		h.Type("\x03") // ctrl+c at the release-target selection form

		err := runPrerelease(h)
		if err == nil {
			t.Fatal("cancelled selection returned nil, want ErrCancelled")
		}
		if !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("error = %v, want a cancellation", err)
		}

		if n := len(h.Host.Releases()); n != 0 {
			t.Errorf("cancelled run created %d release(s), want 0", n)
		}
		if api.HasTag("v1.2.4") {
			t.Error("cancelled run created tag v1.2.4")
		}
		if n := len(h.Notifier.Messages()); n != 0 {
			t.Errorf("cancelled run sent %d notification(s), want 0", n)
		}
	})

	t.Run("release", func(t *testing.T) {
		f := setupRelease(t, releaseMonorepoTarget)
		f.h.Type("\x03") // ctrl+c at the deploy-target selection form

		err := f.run("--migrations-done")
		if err == nil {
			t.Fatal("cancelled selection returned nil, want ErrCancelled")
		}
		if !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("error = %v, want a cancellation", err)
		}

		if n := len(f.triggers()); n != 0 {
			t.Errorf("cancelled run dispatched %d deploy(s), want 0", n)
		}
		if got := f.status(t); got != "In Progress" {
			t.Errorf("issue status = %q, want it held at %q", got, "In Progress")
		}
	})
}
