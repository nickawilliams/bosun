package cli_test

// End-to-end scenarios for `bosun release` through the test harness.
// The pure classification logic (deploy state, tag choice, status
// advance, target parsing) is unit-tested in release_gate_test.go and
// release_seams_test.go (package cli); this file exercises the full
// command path: the migration gate, per-service deploy classification,
// the selection form, plan confirmation, the workflow dispatch, and the
// issue-status transition the run leaves behind.
//
// The tree adapts the planned scenarios to the command as built.
// `bosun release` posts NO notifications — it dispatches deploy
// workflows and moves the issue to done — so the planned
// notification/{start,success,failure} scenarios have no seam to assert
// on. Their subject matter is covered by what the command actually
// produces: the dispatch itself (dispatch/), the status transition
// (status/), and the apply-stage dispatch failure
// (errors/dispatch_failure_surfaces). The planned
// migration_gate/blocks_without_flag likewise asserts on the answer
// rather than the flag's absence: the harness's injected stdin counts
// as interactive, so an unflagged run reaches the confirmation dialog
// instead of the non-interactive "use --migrations-done" error.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/cicd"
	"github.com/nickawilliams/bosun/internal/code"
	"github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/testharness"
	"github.com/nickawilliams/bosun/internal/ui"
)

const releaseBranch = "story/EX-1_feature"

// releaseConfigf renders a scenario's project config around its
// cicd.workflows.release.target block (the one %s), which is what
// distinguishes a single-service repo from a monorepo.
//
// Two deliberate non-defaults:
//   - workspace.root is "trees", not the "workspaces" default
//     Harness.WorktreePath falls back to — so a config-loading
//     regression can't leave the path assertions still passing.
//   - the version input is named "release-version" rather than the
//     built-in "version" default, so the dispatch assertions pin that
//     the command reads cicd.workflows.release.inputs.version instead
//     of hardcoding a name.
const releaseConfigf = `
repositories:
  - "repos/*"
workspace:
  root: "trees"
issue_tracker:
  project: "EX"
  statuses:
    in_progress: "In Progress"
    done: "Done"
cicd:
  provider: "github_actions"
  workflows:
    release:
      inputs:
        version: "release-version"
%s
`

// releaseSingleTarget is a one-service repo: `api` deploys through one
// workflow into the "production" environment.
const releaseSingleTarget = `      target:
        api: ".github/workflows/deploy.yaml"`

// releaseMonorepoTarget is a two-service repo: `api` hosts `web` and
// `worker`, each with its own workflow and (by default) its own
// "<service>-production" environment.
const releaseMonorepoTarget = `      target:
        api:
          web: ".github/workflows/deploy-web.yaml"
          worker: ".github/workflows/deploy-worker.yaml"`

// releaseNoTarget configures the release stage with no deploy targets
// at all — a project that uses `release` purely for the status
// transition.
const releaseNoTarget = `      # no deploy targets configured`

// releaseMalformedTarget names a workflow that is neither a
// repo-relative ".github/..." path nor a full owner/repo/path — the
// misconfiguration release refuses to guess at.
const releaseMalformedTarget = `      target:
        api: "deploy.yaml"`

// releaseFixture is one scenario's assembled world: a started
// workspace whose work is merged and released, plus the harness the
// command runs through.
type releaseFixture struct {
	h   *testharness.Harness
	api *testharness.Repo

	// workSHA is the merge commit the workspace's PR landed on main —
	// the commit whose containing release tag gates the deploy.
	workSHA string
	// baseSHA is main's commit before the work landed, carrying the
	// previous release tag.
	baseSHA string
}

// setupRelease builds the deployable baseline every scenario starts
// from: a workspace started on releaseBranch, its work merged to main
// and recorded as a merged PR, and a v1.2.4 release tag containing that
// work (what `prerelease` would have cut). Nothing is deployed yet, so
// every configured target classifies as a first deploy.
//
// target is the cicd.workflows.release.target block — releaseSingleTarget,
// releaseMonorepoTarget, or releaseNoTarget.
func setupRelease(t *testing.T, target string) *releaseFixture {
	t.Helper()

	h := testharness.New(t)
	h.InstallCICD()
	h.Workspace.WriteConfig(fmt.Sprintf(releaseConfigf, target))
	api := h.Workspace.AddRepo("api")
	h.Tracker.SeedIssue(issue.Issue{
		Key: "EX-1", Title: "Add feature", Type: "Story",
	})
	if err := h.Run("start", "--issue", "EX-1", "--slug", "feature", "--approve"); err != nil {
		t.Fatalf("start: %v", err)
	}

	f := &releaseFixture{h: h, api: api}
	f.baseSHA = api.RemoteRef("refs/heads/main")

	// Land the work on main the way a squash/FF merge does, then record
	// the PR as merged so the resolver's work-SHA probe finds the merge
	// commit rather than falling back to local HEAD.
	wt := h.WorktreePath(releaseBranch, api.Name)
	if err := os.WriteFile(filepath.Join(wt, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatalf("write feature file: %v", err)
	}
	testharness.Git(t, wt, "add", "feature.txt")
	testharness.Git(t, wt, "commit", "-m", "add feature")
	testharness.Git(t, wt, "push", "origin", releaseBranch)
	f.workSHA = testharness.Git(t, wt, "rev-parse", "HEAD")
	testharness.Git(t, api.RemotePath, "update-ref", "refs/heads/main", f.workSHA)
	h.Host.SeedPR(api.Owner, api.Name, releaseBranch, code.PullRequest{
		Number: 7, State: "merged", MergeCommitSHA: f.workSHA,
	})

	// The release containing the work — the gate release deploys.
	api.Tag("v1.2.3", f.baseSHA)
	api.Tag("v1.2.4", f.workSHA)

	return f
}

// run invokes release against the started workspace with any extra
// flags appended.
func (f *releaseFixture) run(extra ...string) error {
	args := append([]string{
		"release", "--workspace", releaseBranch, "--issue", "EX-1",
	}, extra...)
	return f.h.Run(args...)
}

// triggers is the dispatches the run made, in order.
func (f *releaseFixture) triggers() []cicd.TriggerRequest {
	return f.h.CICD.Triggers()
}

// status is the issue's status after the run — "In Progress" while
// the work isn't in production, "Done" once release advanced it.
func (f *releaseFixture) status(t *testing.T) string {
	t.Helper()
	got, ok := f.h.Tracker.Issue("EX-1")
	if !ok {
		t.Fatalf("issue EX-1 missing from tracker")
	}
	return got.Status
}

// reportedSkip returns the first reported skip whose label contains
// substr, for the branches whose only effect is a Reporter call.
func reportedSkip(t *testing.T, h *testharness.Harness, substr string) (ui.CaptureEvent, bool) {
	t.Helper()
	for _, ev := range h.Reporter.OfKind(ui.CaptureSkip) {
		if strings.Contains(ev.Label, substr) {
			return ev, true
		}
	}
	return ui.CaptureEvent{}, false
}

// TestRelease exercises the bosun release command end-to-end through
// the test harness. Each sub-test puts the workspace, the deployed
// state, and the flags in a particular position, runs release, and
// asserts on the workflow dispatches, the issue status, and what the
// command reported.
func TestRelease(t *testing.T) {
	t.Run("dispatch/triggers_deploy_workflow", func(t *testing.T) {
		// The service has never been deployed and v1.2.4 contains the
		// work: one dispatch of the configured workflow, ON the release
		// tag, carrying that tag as the version input.
		f := setupRelease(t, releaseSingleTarget)

		if err := f.run("--migrations-done", "--service", "api", "--approve"); err != nil {
			t.Fatalf("release: %v", err)
		}

		trs := f.triggers()
		if len(trs) != 1 {
			t.Fatalf("dispatches = %d, want 1 (%+v)", len(trs), trs)
		}
		tr := trs[0]
		if tr.Owner != "acme" || tr.Repository != "api" {
			t.Errorf("dispatch target = %s/%s, want acme/api", tr.Owner, tr.Repository)
		}
		if tr.Workflow != "deploy.yaml" {
			t.Errorf("workflow = %q, want %q", tr.Workflow, "deploy.yaml")
		}
		if tr.Ref != "v1.2.4" {
			t.Errorf("ref = %q, want the release tag %q", tr.Ref, "v1.2.4")
		}
		if got := tr.Inputs["release-version"]; got != "v1.2.4" {
			t.Errorf("release-version input = %q, want %q (inputs=%v)", got, "v1.2.4", tr.Inputs)
		}
		if got := f.status(t); got != "Done" {
			t.Errorf("issue status = %q, want %q", got, "Done")
		}
	})

	t.Run("dispatch/services_filter_from_flag", func(t *testing.T) {
		// A monorepo with two deployable services; --service names one.
		// Only that service's workflow is dispatched — the sibling is a
		// no-op, not a second deploy.
		f := setupRelease(t, releaseMonorepoTarget)

		if err := f.run("--migrations-done", "--service", "web", "--approve"); err != nil {
			t.Fatalf("release: %v", err)
		}

		trs := f.triggers()
		if len(trs) != 1 {
			t.Fatalf("dispatches = %d, want 1 (%+v)", len(trs), trs)
		}
		if trs[0].Workflow != "deploy-web.yaml" {
			t.Errorf("workflow = %q, want %q", trs[0].Workflow, "deploy-web.yaml")
		}
	})

	t.Run("dispatch/tag_override_deploys_named_release", func(t *testing.T) {
		// --tag names an older release: the work is still gated on
		// v1.2.4 containing it, but what ships is exactly what the
		// operator named.
		f := setupRelease(t, releaseSingleTarget)

		if err := f.run("--migrations-done", "--service", "api", "--tag", "v1.2.3", "--approve"); err != nil {
			t.Fatalf("release: %v", err)
		}

		trs := f.triggers()
		if len(trs) != 1 {
			t.Fatalf("dispatches = %d, want 1 (%+v)", len(trs), trs)
		}
		if trs[0].Ref != "v1.2.3" {
			t.Errorf("ref = %q, want the --tag override %q", trs[0].Ref, "v1.2.3")
		}
		if got := trs[0].Inputs["release-version"]; got != "v1.2.3" {
			t.Errorf("release-version input = %q, want %q", got, "v1.2.3")
		}
	})

	t.Run("dispatch/no_service_flag_deploys_nothing", func(t *testing.T) {
		// Services are opt-IN: a non-prompting run that names no
		// service deploys nothing, and the issue is NOT called done —
		// deselecting every deploy holds the status.
		f := setupRelease(t, releaseSingleTarget)

		if err := f.run("--migrations-done", "--approve"); err != nil {
			t.Fatalf("release: %v", err)
		}

		if n := len(f.triggers()); n != 0 {
			t.Errorf("dispatches = %d, want 0 (nothing was selected)", n)
		}
		if got := f.status(t); got != "In Progress" {
			t.Errorf("issue status = %q, want %q (nothing shipped)", got, "In Progress")
		}
	})

	t.Run("dispatch/interactive_selection_deploys_picked", func(t *testing.T) {
		// No --service, so both of the monorepo's services arrive at
		// the selection form unchecked. Space checks the first (web),
		// enter submits, and "y" approves the plan: exactly the picked
		// service deploys and its unpicked sibling doesn't.
		f := setupRelease(t, releaseMonorepoTarget)
		f.h.Type(" \r") // multi-select: toggle the focused row, submit
		f.h.Type("y")   // plan gate: approve

		if err := f.run("--migrations-done"); err != nil {
			t.Fatalf("release: %v", err)
		}

		trs := f.triggers()
		if len(trs) != 1 {
			t.Fatalf("dispatches = %d, want 1 (%+v)", len(trs), trs)
		}
		if trs[0].Workflow != "deploy-web.yaml" {
			t.Errorf("workflow = %q, want the picked service's %q", trs[0].Workflow, "deploy-web.yaml")
		}
		if trs[0].Ref != "v1.2.4" {
			t.Errorf("ref = %q, want %q", trs[0].Ref, "v1.2.4")
		}
		if got := f.status(t); got != "Done" {
			t.Errorf("issue status = %q, want %q", got, "Done")
		}
	})

	t.Run("dispatch/no_targets_configured", func(t *testing.T) {
		// No deploy targets at all: the command says so and still
		// advances the issue — a project with nothing to deploy uses
		// release purely as the status transition.
		f := setupRelease(t, releaseNoTarget)

		if err := f.run("--migrations-done", "--approve"); err != nil {
			t.Fatalf("release: %v", err)
		}

		if n := len(f.triggers()); n != 0 {
			t.Errorf("dispatches = %d, want 0", n)
		}
		if _, ok := reportedSkip(t, f.h, "no production deploy targets configured"); !ok {
			t.Errorf("no deploy-targets skip reported; got:\n%s", f.h.Reporter.Dump())
		}
		if got := f.status(t); got != "Done" {
			t.Errorf("issue status = %q, want %q", got, "Done")
		}
	})

	t.Run("idempotency/already_deployed_no_dispatch", func(t *testing.T) {
		// Production already runs v1.2.4 — the very release being
		// deployed. Nothing is dispatched, and the issue still advances
		// because nothing NEEDED to ship.
		f := setupRelease(t, releaseSingleTarget)
		f.h.Host.SeedDeployment(f.api.Owner, f.api.Name, "production", code.Deployment{
			Ref: "v1.2.4", SHA: f.workSHA, State: "success",
		})

		if err := f.run("--migrations-done", "--service", "api", "--approve"); err != nil {
			t.Fatalf("release: %v", err)
		}

		if n := len(f.triggers()); n != 0 {
			t.Errorf("dispatches = %d, want 0 (already live at v1.2.4)", n)
		}
		if !slices.Contains(f.h.Host.Calls(), "GetLatestDeployment") {
			t.Errorf("deployed state never queried; calls=%v", f.h.Host.Calls())
		}
		if got := f.status(t); got != "Done" {
			t.Errorf("issue status = %q, want %q (already live counts as shipped)", got, "Done")
		}
	})

	t.Run("idempotency/deployed_state_is_per_service", func(t *testing.T) {
		// A monorepo's services each read their own
		// "<service>-production" environment. Seeding only web's leaves
		// worker untouched, so the same workspace release skips for one
		// and deploys for the other — which is only true if the lookup
		// is keyed on the target's environment. A constant "production"
		// (or the sibling's env) would miss the seed, classify web as a
		// first deploy, and dispatch.
		f := setupRelease(t, releaseMonorepoTarget)
		f.h.Host.SeedDeployment(f.api.Owner, f.api.Name, "web-production", code.Deployment{
			Ref: "v1.2.4", SHA: f.workSHA, State: "success",
		})

		if err := f.run("--migrations-done", "--service", "web", "--approve"); err != nil {
			t.Fatalf("release web: %v", err)
		}
		if n := len(f.triggers()); n != 0 {
			t.Fatalf("web dispatches = %d, want 0 (already live at v1.2.4)", n)
		}

		if err := f.run("--migrations-done", "--service", "worker", "--approve"); err != nil {
			t.Fatalf("release worker: %v", err)
		}
		trs := f.triggers()
		if len(trs) != 1 {
			t.Fatalf("worker dispatches = %d, want 1 (%+v)", len(trs), trs)
		}
		if trs[0].Workflow != "deploy-worker.yaml" {
			t.Errorf("workflow = %q, want %q", trs[0].Workflow, "deploy-worker.yaml")
		}
	})

	t.Run("idempotency/failed_deployment_is_not_live", func(t *testing.T) {
		// The environment's latest deployment of v1.2.4 FAILED. That is
		// not what's live, so the release still ships — the host only
		// reports successful deployments, and a failed one must not
		// read as "already deployed".
		f := setupRelease(t, releaseSingleTarget)
		f.h.Host.SeedDeployment(f.api.Owner, f.api.Name, "production", code.Deployment{
			Ref: "v1.2.4", SHA: f.workSHA, State: "failure",
		})

		if err := f.run("--migrations-done", "--service", "api", "--approve"); err != nil {
			t.Fatalf("release: %v", err)
		}

		trs := f.triggers()
		if len(trs) != 1 {
			t.Fatalf("dispatches = %d, want 1 (a failed deploy isn't live)", len(trs))
		}
		if trs[0].Ref != "v1.2.4" {
			t.Errorf("ref = %q, want %q", trs[0].Ref, "v1.2.4")
		}
	})

	t.Run("gate/unreleased_work_blocks_deploy", func(t *testing.T) {
		// The workspace's work is merged but no release contains it:
		// the deploy is blocked (run prerelease first), nothing is
		// dispatched, and the issue is NOT called done.
		f := setupRelease(t, releaseSingleTarget)
		// Move main past the release tags so the merge commit is no
		// longer inside any of them.
		wt := f.h.WorktreePath(releaseBranch, f.api.Name)
		if err := os.WriteFile(filepath.Join(wt, "later.txt"), []byte("later\n"), 0o644); err != nil {
			t.Fatalf("write later file: %v", err)
		}
		testharness.Git(t, wt, "add", "later.txt")
		testharness.Git(t, wt, "commit", "-m", "later work")
		testharness.Git(t, wt, "push", "origin", releaseBranch)
		later := testharness.Git(t, wt, "rev-parse", "HEAD")
		testharness.Git(t, f.api.RemotePath, "update-ref", "refs/heads/main", later)
		f.h.Host.SeedPR(f.api.Owner, f.api.Name, releaseBranch, code.PullRequest{
			Number: 7, State: "merged", MergeCommitSHA: later,
		})

		if err := f.run("--migrations-done", "--service", "api", "--approve"); err != nil {
			t.Fatalf("release: %v", err)
		}

		if n := len(f.triggers()); n != 0 {
			t.Errorf("dispatches = %d, want 0 (no release contains the work)", n)
		}
		if got := f.status(t); got != "In Progress" {
			t.Errorf("issue status = %q, want %q (blocked work isn't done)", got, "In Progress")
		}
		// A gate BLOCK and a resolution ERROR both dispatch nothing and
		// both hold the status, so neither assertion above tells them
		// apart. The nil return does: an errored target assesses into a
		// ✗ plan row, which runActions surfaces as the command's error.
		// Reaching here means the target was classified and blocked.
	})

	t.Run("migration_gate/blocks_without_flag", func(t *testing.T) {
		// No --migrations-done, and the confirmation dialog is
		// declined: the run stops before it ever looks at production,
		// telling the user what to do next.
		f := setupRelease(t, releaseSingleTarget)
		f.h.Type("n")

		if err := f.run("--service", "api", "--approve"); err != nil {
			t.Fatalf("release: %v", err)
		}

		if n := len(f.triggers()); n != 0 {
			t.Errorf("dispatches = %d, want 0 (migrations not confirmed)", n)
		}
		if slices.Contains(f.h.Host.Calls(), "GetLatestDeployment") {
			t.Errorf("declined run still classified deploys; calls=%v", f.h.Host.Calls())
		}
		if _, ok := reportedSkip(t, f.h, "run migrations first"); !ok {
			t.Errorf("no migrations skip reported; got:\n%s", f.h.Reporter.Dump())
		}
		if got := f.status(t); got != "In Progress" {
			t.Errorf("issue status = %q, want %q", got, "In Progress")
		}
	})

	t.Run("migration_gate/passes_with_flag", func(t *testing.T) {
		// --migrations-done records the confirmation as coming from the
		// flag (no dialog) and the deploy proceeds.
		f := setupRelease(t, releaseSingleTarget)

		if err := f.run("--migrations-done", "--service", "api", "--approve"); err != nil {
			t.Fatalf("release: %v", err)
		}

		ev, ok := f.h.Reporter.Find("migrations confirmed")
		if !ok {
			t.Fatalf("no migrations confirmation reported; got:\n%s", f.h.Reporter.Dump())
		}
		if ev.Kind != ui.CaptureSaved || ev.Value != "--migrations-done" {
			t.Errorf("confirmation = %s/%q, want saved/%q", ev.Kind, ev.Value, "--migrations-done")
		}
		if n := len(f.triggers()); n != 1 {
			t.Errorf("dispatches = %d, want 1", n)
		}
	})

	t.Run("migration_gate/interactive_confirm", func(t *testing.T) {
		// No flag: the dialog opens and "y" confirms. The confirmation
		// is reported as an answered step (not a flag) and the deploy
		// proceeds.
		f := setupRelease(t, releaseSingleTarget)
		f.h.Type("y")

		if err := f.run("--service", "api", "--approve"); err != nil {
			t.Fatalf("release: %v", err)
		}

		ev, ok := f.h.Reporter.Find("migrations confirmed")
		if !ok {
			t.Fatalf("no migrations confirmation reported; got:\n%s", f.h.Reporter.Dump())
		}
		if ev.Kind != ui.CaptureComplete {
			t.Errorf("confirmation kind = %s, want %s (answered, not flagged)", ev.Kind, ui.CaptureComplete)
		}
		if n := len(f.triggers()); n != 1 {
			t.Errorf("dispatches = %d, want 1", n)
		}
	})

	t.Run("plan_confirmation/yes_flag_skips_prompt", func(t *testing.T) {
		// --approve suppresses both the selection form and the plan
		// gate. The queued keys make a regression fail fast instead of
		// hanging: whichever prompt stopped being suppressed reads them
		// and cancels rather than blocking on an empty stdin.
		f := setupRelease(t, releaseSingleTarget)
		f.h.Type("\r")
		f.h.Type("n")

		if err := f.run("--migrations-done", "--service", "api", "--approve"); err != nil {
			t.Fatalf("release: %v", err)
		}

		if n := len(f.triggers()); n != 1 {
			t.Fatalf("dispatches = %d, want 1", n)
		}
		if got := f.status(t); got != "Done" {
			t.Errorf("issue status = %q, want %q", got, "Done")
		}
	})

	t.Run("plan_confirmation/dry_run_skips_apply", func(t *testing.T) {
		// Dry-run still walks the selection form (enter keeps the
		// --service pre-check) but renders the plan without applying:
		// ErrCancelled, no dispatch, no status change.
		//
		// The trailing "n" is a fail-fast guard, not part of the flow:
		// a --dry-run that stopped suppressing apply would fall through
		// to the plan gate and block forever on an empty stdin, burning
		// the package timeout. With the key queued it reads a Cancel
		// and the scenario fails on the assertions below instead.
		f := setupRelease(t, releaseSingleTarget)
		f.h.Type("\r")
		f.h.Type("n")

		err := f.run("--migrations-done", "--service", "api", "--dry-run")
		if err == nil {
			t.Fatalf("dry-run should return ErrCancelled; got nil")
		}
		if !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("dry-run error = %v, want contains \"cancelled\"", err)
		}
		// --dry-run returns the bare ErrCancelled ("cancelled"); an
		// answered Cancel returns errPlanCancelled ("plan cancelled").
		// Without this line the guard key above hides the regression it
		// exists to expose: a run that ignored --dry-run would reach
		// the gate, read the "n", and cancel — passing every other
		// assertion here.
		if strings.Contains(err.Error(), "plan cancelled") {
			t.Fatalf("error = %v, want dry-run's own cancellation, not an answered gate", err)
		}

		if n := len(f.triggers()); n != 0 {
			t.Errorf("dry-run dispatched %d workflow(s); want 0", n)
		}
		if got := f.status(t); got != "In Progress" {
			t.Errorf("dry-run advanced issue status to %q", got)
		}
	})

	t.Run("plan_confirmation/cancelled_aborts", func(t *testing.T) {
		// Enter submits the selection form with its pre-checked target,
		// then "n" selects Cancel at the plan gate: ErrCancelled with no
		// dispatch and no status change.
		f := setupRelease(t, releaseSingleTarget)
		f.h.Type("\r")
		f.h.Type("n")

		// The setup's `start` run already called SetStatus (→ In
		// Progress); only calls this run makes matter below.
		before := len(f.h.Tracker.Calls())

		err := f.run("--migrations-done", "--service", "api")
		if err == nil {
			t.Fatalf("expected ErrCancelled; got nil")
		}
		// Pin the genuine decline: runPlanCard maps ANY confirm-form
		// error to bare ErrCancelled ("cancelled"), but only an answered
		// Cancel returns errPlanCancelled ("plan cancelled"). A confirm
		// form that died on a read error would fail this.
		if !strings.Contains(err.Error(), "plan cancelled") {
			t.Fatalf("error = %v, want the answered-decline \"plan cancelled\"", err)
		}

		if n := len(f.triggers()); n != 0 {
			t.Errorf("cancelled run dispatched %d workflow(s); want 0", n)
		}
		if newCalls := f.h.Tracker.Calls()[before:]; slices.Contains(newCalls, "SetStatus") {
			t.Errorf("cancelled run called SetStatus; calls=%v", newCalls)
		}
	})

	t.Run("errors/cicd_unconfigured", func(t *testing.T) {
		// The CI/CD provider fails to construct. Release reports the
		// reason and skips deploy classification entirely — and because
		// it never looked, it must NOT call the work done.
		f := setupRelease(t, releaseSingleTarget)
		f.h.CICD.NewErr = errors.New("no GITHUB_TOKEN configured")

		if err := f.run("--migrations-done", "--service", "api", "--approve"); err != nil {
			t.Fatalf("release: %v", err)
		}

		ev, ok := reportedSkip(t, f.h, "CI/CD:")
		if !ok {
			t.Fatalf("no CI/CD skip reported; got:\n%s", f.h.Reporter.Dump())
		}
		if !strings.Contains(ev.Label, "no GITHUB_TOKEN configured") {
			t.Errorf("skip = %q, does not carry the factory's reason", ev.Label)
		}
		if n := len(f.triggers()); n != 0 {
			t.Errorf("dispatches = %d, want 0", n)
		}
		if got := f.status(t); got != "In Progress" {
			t.Errorf("issue status = %q, want %q (couldn't look ≠ nothing to do)", got, "In Progress")
		}
	})

	t.Run("errors/code_host_unconfigured", func(t *testing.T) {
		// The other half of "couldn't look": the code host fails to
		// construct, so no target is ever classified. Same posture as
		// the CI/CD failure — report the reason, dispatch nothing, and
		// leave the issue where it was.
		f := setupRelease(t, releaseSingleTarget)
		f.h.Host.NewErr = errors.New("gh auth token missing")

		if err := f.run("--migrations-done", "--service", "api", "--approve"); err != nil {
			t.Fatalf("release: %v", err)
		}

		ev, ok := reportedSkip(t, f.h, "code host:")
		if !ok {
			t.Fatalf("no code-host skip reported; got:\n%s", f.h.Reporter.Dump())
		}
		if !strings.Contains(ev.Label, "gh auth token missing") {
			t.Errorf("skip = %q, does not carry the factory's reason", ev.Label)
		}
		if n := len(f.triggers()); n != 0 {
			t.Errorf("dispatches = %d, want 0", n)
		}
		if got := f.status(t); got != "In Progress" {
			t.Errorf("issue status = %q, want %q (couldn't look ≠ nothing to do)", got, "In Progress")
		}
	})

	t.Run("errors/unknown_service_flag", func(t *testing.T) {
		// --service naming a service no target configures is a hard
		// error: filtering it out silently would read as "nothing to
		// deploy" for what is actually a typo. Repeated here alongside
		// a service that DOES match, so the error can't come from
		// "nothing matched" — it has to name the one that didn't.
		f := setupRelease(t, releaseMonorepoTarget)

		err := f.run("--migrations-done", "--service", "web", "--service", "wbe", "--approve")
		if err == nil {
			t.Fatalf("expected an error for an unknown --service; got nil")
		}
		if !strings.Contains(err.Error(), "wbe") ||
			!strings.Contains(err.Error(), "doesn't match any configured deploy target") {
			t.Errorf("error = %v, want it to name the unmatched service", err)
		}
		if strings.Contains(err.Error(), "web,") || strings.Contains(err.Error(), " web") {
			t.Errorf("error = %v, names the service that DID match", err)
		}
		if n := len(f.triggers()); n != 0 {
			t.Errorf("dispatches = %d, want 0", n)
		}
		if got := f.status(t); got != "In Progress" {
			t.Errorf("issue status = %q, want %q", got, "In Progress")
		}
	})

	t.Run("errors/malformed_deploy_target", func(t *testing.T) {
		// A deploy target that names neither a repo-relative workflow
		// nor an owner/repo path aborts the run with the offending
		// value, rather than resolving to a target that dispatches
		// somewhere unintended.
		f := setupRelease(t, releaseMalformedTarget)

		err := f.run("--migrations-done", "--approve")
		if err == nil {
			t.Fatalf("expected a config error; got nil")
		}
		if !strings.Contains(err.Error(), "invalid workflow path") ||
			!strings.Contains(err.Error(), "deploy.yaml") {
			t.Errorf("error = %v, want it to name the invalid workflow path", err)
		}
		if n := len(f.triggers()); n != 0 {
			t.Errorf("dispatches = %d, want 0", n)
		}
		if got := f.status(t); got != "In Progress" {
			t.Errorf("issue status = %q, want %q", got, "In Progress")
		}
	})

	t.Run("errors/dispatch_failure_surfaces", func(t *testing.T) {
		// The dispatch is rejected at apply time: the pipeline's error
		// propagates out of the plan apply as the command's exit.
		f := setupRelease(t, releaseSingleTarget)
		f.h.CICD.TriggerErr = errors.New("workflow dispatch 422: no ref v1.2.4")

		err := f.run("--migrations-done", "--service", "api", "--approve")
		if err == nil {
			t.Fatalf("expected the dispatch failure to surface; got nil")
		}
		if !strings.Contains(err.Error(), "no ref v1.2.4") {
			t.Errorf("error %q does not carry the pipeline's message", err)
		}

		if n := len(f.triggers()); n != 0 {
			t.Errorf("failed dispatch was recorded as %d trigger(s); want 0", n)
		}
		if !slices.Contains(f.h.CICD.Calls(), "TriggerWorkflow") {
			t.Errorf("dispatch never attempted; calls=%v", f.h.CICD.Calls())
		}
		// The status transition is queued behind the deploy and gated
		// on it (Action.RequiresPriorSuccess), so a failed dispatch
		// holds the issue where it was. Moving it to Done here would
		// tell everyone reading the board that a deploy that never
		// reached production had shipped.
		if got := f.status(t); got != "In Progress" {
			t.Errorf("issue status = %q, want %q — the deploy failed, so nothing is Done", got, "In Progress")
		}
	})

	t.Run("errors/deployed_state_lookup_fails", func(t *testing.T) {
		// Reading production's current version fails. Release fails
		// closed — an ✗ row rather than a permissive deploy — so
		// nothing is dispatched and the issue is not advanced.
		f := setupRelease(t, releaseSingleTarget)
		f.h.Host.GetLatestDeploymentErr = errors.New("deployments API 500")

		err := f.run("--migrations-done", "--service", "api", "--approve")
		if err == nil {
			t.Fatalf("expected the deployed-state failure to surface; got nil")
		}
		if !strings.Contains(err.Error(), "deployments API 500") {
			t.Errorf("error %q does not carry the host's message", err)
		}
		if n := len(f.triggers()); n != 0 {
			t.Errorf("dispatches = %d, want 0", n)
		}
		if got := f.status(t); got != "In Progress" {
			t.Errorf("issue status = %q, want %q", got, "In Progress")
		}
	})
}
