package cli_test

// End-to-end scenarios for `bosun preview` through the test harness.
// The pure name-resolution matrix is unit-tested in
// preview_resolve_test.go (package cli); this file exercises the full
// command path: workspace resolution, env probing, the deploy plan,
// plan confirmation, the workflow dispatch, and the env-to-issue
// binding the run leaves behind.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/nickawilliams/bosun/internal/cicd"
	"github.com/nickawilliams/bosun/internal/cli"
	"github.com/nickawilliams/bosun/internal/code"
	"github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/notify"
	"github.com/nickawilliams/bosun/internal/testharness"
	"github.com/nickawilliams/bosun/internal/ui"
)

const previewBranch = "story/EX-1_feature"

// previewConfigf renders the project config for a preview scenario.
// The single %s is the base URL of the scenario's env server, which
// url_template interpolates the env name onto — the adapter probes
// that URL to decide whether an environment is already alive, so
// liveness is expressed by what the server answers rather than by
// seeding a provider fake's Environment struct.
//
// workspace.root is deliberately NOT the "workspaces" default:
// Harness.WorktreePath falls back to that name when viper holds
// nothing, so a config-loading regression would still point every path
// assertion at the right directory and pass.
//
// The workflow targets name a devops repo that is NOT one of the
// workspace's repos — global mode, matching how a shared deploy
// workflow is configured in practice, and keeping the dispatch target
// distinguishable from the repos being deployed.
const previewConfigf = `
repositories:
  - "repos/*"
workspace:
  root: "trees"
issue_tracker:
  project: "EX"
  statuses:
    in_progress: "In Progress"
    preview: "In Preview Env"
services:
  api: "api-svc"
  web: "web-svc"
cicd:
  provider: "github_actions"
  workflows:
    preview:
      url_template: "%s/{{.Name}}"
      up:
        target: "acme/devops/.github/workflows/deploy-preview.yml"
        inputs:
          name: "env-name"
          issue: "issue-key"
          services: "services-to-deploy"
      down:
        target: "acme/devops/.github/workflows/teardown-preview.yml"
        inputs:
          name: "env-name"
notification:
  channel_review: "prs"
`

// envServer stands in for the preview hosting environment the adapter
// probes. Names registered via alive answer 200 (env is up); every
// other name answers 404, which the probe classifies as a definitive
// miss. Names registered via broken answer 500, which stays
// indeterminate across the probe's retry and surfaces as a
// preview.ProbeError — the --force fallback's trigger.
type envServer struct {
	*httptest.Server

	mu     sync.Mutex
	up     map[string]bool
	broken map[string]bool
}

// newEnvServer starts an env server bound to the test's lifetime.
func newEnvServer(t *testing.T) *envServer {
	t.Helper()
	es := &envServer{up: map[string]bool{}, broken: map[string]bool{}}
	es.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		es.mu.Lock()
		defer es.mu.Unlock()
		switch {
		case es.broken[name]:
			w.WriteHeader(http.StatusInternalServerError)
		case es.up[name]:
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(es.Close)
	return es
}

// alive marks name as a running environment (probes answer 200).
func (es *envServer) alive(name string) {
	es.mu.Lock()
	defer es.mu.Unlock()
	es.up[name] = true
}

// unreachable marks name as answering 500 — the indeterminate probe
// the --force fallback exists for.
func (es *envServer) unreachable(name string) {
	es.mu.Lock()
	defer es.mu.Unlock()
	es.broken[name] = true
}

// previewFixture is one scenario's assembled world: a started
// workspace, the env server backing url_template, and the harness the
// command runs through.
type previewFixture struct {
	h    *testharness.Harness
	env  *envServer
	repo map[string]*testharness.Repo
}

// setupPreview builds the baseline every preview scenario starts from:
// a workspace started on previewBranch across the named repos, each
// with a pushed change and an open PR, and CI/CD wired to the fake
// pipeline with the real preview adapter on top of it.
//
// The adapter — not a fake provider — is what runs here. `bosun
// preview`'s whole job is resolving an issue key to an environment
// and dispatching for it, so a fake provider would leave exactly that
// unpinned: seeding an env and reading the seed back proves nothing
// about which key was looked up. Going through the adapter puts the
// issue key in the tracker property and in the dispatch inputs, where
// a test can assert on it.
func setupPreview(t *testing.T, repoNames ...string) *previewFixture {
	t.Helper()

	es := newEnvServer(t)
	h := testharness.New(t)
	h.InstallCICD()
	h.InstallPreviewAdapter()
	h.Workspace.WriteConfig(fmt.Sprintf(previewConfigf, es.URL))

	repos := map[string]*testharness.Repo{}
	for _, name := range repoNames {
		repos[name] = h.Workspace.AddRepo(name)
	}
	h.Tracker.SeedIssue(issue.Issue{
		Key: "EX-1", Title: "Add feature", Type: "Story",
	})
	// --repository names every repo explicitly: with more than one
	// configured, start would otherwise open its interactive
	// multi-select, and the keys for it aren't what these scenarios
	// are about.
	if err := h.Run(
		"start", "--issue", "EX-1", "--slug", "feature",
		"--repository", strings.Join(repoNames, ","), "--approve",
	); err != nil {
		t.Fatalf("start: %v", err)
	}

	f := &previewFixture{h: h, env: es, repo: repos}
	for i, name := range repoNames {
		f.commitAndOpenPR(t, name, 100+i)
	}
	return f
}

// commitAndOpenPR pushes a change on the workspace branch and records
// an open PR for it. Both halves matter to a deploy: the change makes
// the repo's service register as affected, and the PR number becomes
// the pr-N image override the deploy carries.
func (f *previewFixture) commitAndOpenPR(t *testing.T, repoName string, prNumber int) {
	t.Helper()
	r := f.repo[repoName]
	wt := f.h.WorktreePath(previewBranch, repoName)
	if err := os.WriteFile(filepath.Join(wt, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatalf("write feature file: %v", err)
	}
	testharness.Git(t, wt, "add", "feature.txt")
	testharness.Git(t, wt, "commit", "-m", "add feature")
	testharness.Git(t, wt, "push", "origin", previewBranch)
	f.h.Host.SeedPR(r.Owner, r.Name, previewBranch, code.PullRequest{
		Number: prNumber, State: "open",
		URL: fmt.Sprintf("https://github.test/acme/%s/pull/%d", repoName, prNumber),
	})
}

// bindEnv puts the issue in the "already has a preview" position by
// writing the binding the adapter itself would have written, under the
// real issue key. Seeding the registry rather than running a prior
// deploy keeps the scenario's assertions about this run only.
func (f *previewFixture) bindEnv(issueKey, name string) {
	f.h.Tracker.SeedProperty(issueKey, map[string]string{"preview_name": name})
}

// run invokes preview against the started workspace with any extra
// flags appended.
func (f *previewFixture) run(extra ...string) error {
	args := append([]string{
		"preview", "--workspace", previewBranch, "--issue", "EX-1",
	}, extra...)
	return f.h.Run(args...)
}

// deploys returns the dispatches that targeted the preview.up
// workflow — the ones that stand a preview environment up.
func (f *previewFixture) deploys() []cicd.TriggerRequest {
	var out []cicd.TriggerRequest
	for _, tr := range f.h.CICD.Triggers() {
		if tr.Workflow == "deploy-preview.yml" {
			out = append(out, tr)
		}
	}
	return out
}

// teardowns returns the dispatches that targeted the preview.down
// workflow.
func (f *previewFixture) teardowns() []cicd.TriggerRequest {
	var out []cicd.TriggerRequest
	for _, tr := range f.h.CICD.Triggers() {
		if tr.Workflow == "teardown-preview.yml" {
			out = append(out, tr)
		}
	}
	return out
}

// boundName returns the env name recorded against issueKey in the
// tracker registry, or "" when nothing is bound. Reading the property
// under the key the test names — rather than "whatever single property
// exists" — is what pins the key binding: a command that wrote the
// right JSON under the wrong issue key would still leave the registry
// non-empty.
func (f *previewFixture) boundName(t *testing.T, issueKey string) string {
	t.Helper()
	raw, ok := f.h.Tracker.Property(issueKey)
	if !ok {
		return ""
	}
	var entry struct {
		PreviewName string `json:"preview_name"`
	}
	if err := json.Unmarshal(raw, &entry); err != nil {
		t.Fatalf("registry entry for %s is not the documented shape: %v (%s)", issueKey, err, raw)
	}
	return entry.PreviewName
}

// TestPreview exercises the bosun preview command end-to-end through
// the test harness. Each sub-test assembles a workspace, puts the env
// server and the issue registry in a particular position, runs
// preview, and asserts on the workflow dispatches, the recorded
// binding, the issue status, and the notification.
//
// The tree adapts the planned scenarios to the command as built.
// preview.Provider's write path is Create/Adopt/Destroy, not the
// Claim/Cleanup the issue named, and the deploy set comes from
// --service or affected-service detection rather than a claim-time
// filter — so claim/ asserts on what the dispatch carried. The
// planned notification/sent_on_success is really an update: preview
// only touches the review channel when a thread for the issue already
// exists (it announces nothing on its own), so the scenario seeds the
// thread review would have opened.
func TestPreview(t *testing.T) {
	t.Run("claim/triggers_workflow", func(t *testing.T) {
		// Nothing bound and the requested name answers 404, so this is
		// a fresh claim: one dispatch to the configured up-workflow
		// carrying the env name, the issue key, and the affected
		// service — and the binding lands under the issue afterwards.
		f := setupPreview(t, "api")

		if err := f.run("--name", "brave-falcon", "--approve"); err != nil {
			t.Fatalf("preview: %v", err)
		}

		deploys := f.deploys()
		if len(deploys) != 1 {
			t.Fatalf("deploy dispatches = %d, want 1 (%+v)", len(deploys), f.h.CICD.Triggers())
		}
		d := deploys[0]
		if d.Owner != "acme" || d.Repository != "devops" {
			t.Errorf("dispatch target = %s/%s, want acme/devops", d.Owner, d.Repository)
		}
		if d.Ref != "main" {
			t.Errorf("dispatch ref = %q, want %q", d.Ref, "main")
		}
		if got := d.Inputs["env-name"]; got != "brave-falcon" {
			t.Errorf("env-name input = %q, want %q", got, "brave-falcon")
		}
		if got := d.Inputs["issue-key"]; got != "EX-1" {
			t.Errorf("issue-key input = %q, want %q", got, "EX-1")
		}
		if got := d.Inputs["services-to-deploy"]; got != "api-svc" {
			t.Errorf("services input = %q, want %q", got, "api-svc")
		}
		if got := d.Inputs["image-overrides"]; got != `{"api-svc":"pr-100"}` {
			t.Errorf("image-overrides = %q, want the branch's PR tag", got)
		}

		if got := f.boundName(t, "EX-1"); got != "brave-falcon" {
			t.Errorf("env bound to EX-1 = %q, want %q", got, "brave-falcon")
		}
		if got, _ := f.h.Tracker.Issue("EX-1"); got.Status != "In Preview Env" {
			t.Errorf("issue status = %q, want %q", got.Status, "In Preview Env")
		}
		// The registry lookup that opened the run has to have used the
		// issue key too. Nothing was bound here, so a lookup under the
		// WRONG key would also come back empty and every assertion
		// above would still pass — this is the only one that fails.
		keys := f.h.Tracker.GetPropertyKeys()
		if len(keys) == 0 {
			t.Fatal("preview never consulted the env registry")
		}
		for _, k := range keys {
			if k != "EX-1" {
				t.Errorf("registry read under %q, want every read under EX-1 (%v)", k, keys)
			}
		}
	})

	t.Run("claim/name_prompted_when_unset", func(t *testing.T) {
		// Nothing bound and no --name: Row 1 of the resolution matrix
		// prompts, with a generated name as the placeholder. Every
		// other scenario supplies a name or has one bound, so this is
		// the only one that goes through the prompt — and the only one
		// that reaches the validation behind it.
		//
		// The first name typed is invalid (uppercase is not a legal
		// Kubernetes subdomain label), so the prompt has to refuse it
		// and ask again rather than carrying it into a dispatch the
		// workflow would reject.
		f := setupPreview(t, "api")

		f.h.Type("Brave-Falcon\r") // refused
		f.h.Type("brave-falcon\r") // accepted

		if err := f.run("--approve"); err != nil {
			t.Fatalf("preview: %v", err)
		}

		var refused bool
		for _, ev := range f.h.Reporter.OfKind(ui.CaptureFail) {
			if strings.Contains(ev.Label, "Brave-Falcon") {
				refused = true
			}
		}
		if !refused {
			t.Errorf("the invalid name was not reported as refused\n%s", f.h.Reporter.Dump())
		}

		deploys := f.deploys()
		if len(deploys) != 1 {
			t.Fatalf("deploy dispatches = %d, want 1", len(deploys))
		}
		// The retyped name is what deployed, and the refused one never
		// reached a dispatch.
		if got := deploys[0].Inputs["env-name"]; got != "brave-falcon" {
			t.Errorf("env-name input = %q, want the accepted name", got)
		}
		if got := f.boundName(t, "EX-1"); got != "brave-falcon" {
			t.Errorf("env bound to EX-1 = %q, want brave-falcon", got)
		}
	})

	t.Run("errors/malformed_workflow_target_surfaces", func(t *testing.T) {
		// A workflow target that isn't owner/repo/path can't be
		// dispatched to. The resolver reports it, and the report has to
		// travel: swallowing it would leave an empty target set, which
		// reads as "no workflow configured" and skips the deploy
		// quietly — the same outcome as a repo that genuinely has none.
		f := setupPreview(t, "api")
		f.h.Workspace.WriteConfig(strings.Replace(
			fmt.Sprintf(previewConfigf, f.env.URL),
			`target: "acme/devops/.github/workflows/deploy-preview.yml"`,
			`target: "deploy-preview.yml"`,
			1,
		))

		err := f.run("--name", "brave-falcon", "--approve")
		if err == nil {
			t.Fatal("preview succeeded with a malformed up-workflow target")
		}
		if !strings.Contains(err.Error(), "deploy-preview.yml") {
			t.Errorf("err = %v, want it to name the offending target", err)
		}
		if got := f.deploys(); len(got) != 0 {
			t.Errorf("dispatched %d workflows despite the bad target", len(got))
		}
	})

	t.Run("claim/services_filter", func(t *testing.T) {
		// Both repos changed, so detection would deploy both services.
		// --service narrows the deploy set to the one named, and the
		// dispatch's services input is what carries that decision to
		// the workflow.
		f := setupPreview(t, "api", "web")

		if err := f.run("--name", "brave-falcon", "--service", "api-svc", "--approve"); err != nil {
			t.Fatalf("preview: %v", err)
		}

		deploys := f.deploys()
		if len(deploys) != 1 {
			t.Fatalf("deploy dispatches = %d, want 1", len(deploys))
		}
		if got := deploys[0].Inputs["services-to-deploy"]; got != "api-svc" {
			t.Errorf("services input = %q, want only the flagged service", got)
		}
		// Image overrides stay keyed by service across every repo whose
		// PR resolved — an entry for a service the run isn't deploying
		// is an inert lookup-table row, not an extra deploy. Pinned so
		// a future change that starts *deploying* from this map has to
		// come past this assertion.
		var overrides map[string]string
		if err := json.Unmarshal([]byte(deploys[0].Inputs["image-overrides"]), &overrides); err != nil {
			t.Fatalf("image-overrides is not JSON: %v", err)
		}
		if overrides["api-svc"] != "pr-100" {
			t.Errorf("overrides = %v, want the deployed service's PR tag", overrides)
		}
	})

	t.Run("idempotency/already_claimed_no_dispatch", func(t *testing.T) {
		// The issue is already bound to an env that probes alive. There
		// is nothing to claim: no dispatch, and the binding is left
		// exactly as it was rather than rewritten.
		f := setupPreview(t, "api")
		f.bindEnv("EX-1", "brave-falcon")
		f.env.alive("brave-falcon")

		// Snapshot: the setup's `start` run made tracker calls of its
		// own, and only what this run did is in question.
		before := len(f.h.Tracker.Calls())

		if err := f.run("--approve"); err != nil {
			t.Fatalf("preview: %v", err)
		}

		if n := len(f.h.CICD.Triggers()); n != 0 {
			t.Errorf("dispatched %d workflow(s); want 0 (%+v)", n, f.h.CICD.Triggers())
		}
		if got := f.boundName(t, "EX-1"); got != "brave-falcon" {
			t.Errorf("env bound to EX-1 = %q, want the existing %q", got, "brave-falcon")
		}
		// A binding that was already correct must not be rewritten.
		if newCalls := f.h.Tracker.Calls()[before:]; slices.Contains(newCalls, "SetProperty") {
			t.Errorf("rewrote the binding; calls=%v", newCalls)
		}
		// The env is current, but the issue still moves: reaching a
		// live preview is the lifecycle event, not the dispatch.
		if got, _ := f.h.Tracker.Issue("EX-1"); got.Status != "In Preview Env" {
			t.Errorf("issue status = %q, want %q", got.Status, "In Preview Env")
		}
	})

	t.Run("force/redeploys_existing", func(t *testing.T) {
		// Same position as the idempotency scenario — bound and alive —
		// but --force turns the no-op into a redeploy under the same
		// name, so the running env gets the branch's current images.
		f := setupPreview(t, "api")
		f.bindEnv("EX-1", "brave-falcon")
		f.env.alive("brave-falcon")

		// Snapshot: the setup's `start` run made tracker calls of its
		// own, and only what this run did is in question.
		before := len(f.h.Tracker.Calls())

		if err := f.run("--force", "--approve"); err != nil {
			t.Fatalf("preview: %v", err)
		}

		deploys := f.deploys()
		if len(deploys) != 1 {
			t.Fatalf("deploy dispatches = %d, want 1", len(deploys))
		}
		if got := deploys[0].Inputs["env-name"]; got != "brave-falcon" {
			t.Errorf("env-name input = %q, want the existing %q", got, "brave-falcon")
		}
		// A redeploy must not tear the running env down first — that
		// would take the preview offline to put the same name back.
		if n := len(f.teardowns()); n != 0 {
			t.Errorf("redeploy dispatched %d teardown(s); want 0", n)
		}
		// The name is unchanged, so asserting on it alone would pass
		// against the seed even if the redeploy never wrote anything.
		// The rewrite itself is the observable difference from the
		// idempotency scenario, which asserts SetProperty is NOT called.
		if newCalls := f.h.Tracker.Calls()[before:]; !slices.Contains(newCalls, "SetProperty") {
			t.Errorf("redeploy never rewrote the binding; calls=%v", newCalls)
		}
		if got := f.boundName(t, "EX-1"); got != "brave-falcon" {
			t.Errorf("env bound to EX-1 = %q, want %q", got, "brave-falcon")
		}
	})

	t.Run("rename/tears_down_previous_env", func(t *testing.T) {
		// A --name that differs from the bound env is a move: the old
		// env is destroyed and the new one stood up in the same run.
		// This is the only preview path that tears anything down, and
		// the teardown must carry the OLD name — a teardown of the new
		// name would destroy what this run just deployed, and a dropped
		// teardown row leaks the old environment forever.
		f := setupPreview(t, "api")
		f.bindEnv("EX-1", "old-env")
		f.env.alive("old-env")

		if err := f.run("--name", "new-env", "--approve"); err != nil {
			t.Fatalf("preview: %v", err)
		}

		teardowns := f.teardowns()
		if len(teardowns) != 1 {
			t.Fatalf("teardown dispatches = %d, want 1 (%+v)", len(teardowns), f.h.CICD.Triggers())
		}
		if got := teardowns[0].Inputs["env-name"]; got != "old-env" {
			t.Errorf("teardown env-name = %q, want the superseded %q", got, "old-env")
		}
		if got := teardowns[0].Repository; got != "devops" {
			t.Errorf("teardown target repo = %q, want %q", got, "devops")
		}

		deploys := f.deploys()
		if len(deploys) != 1 {
			t.Fatalf("deploy dispatches = %d, want 1", len(deploys))
		}
		if got := deploys[0].Inputs["env-name"]; got != "new-env" {
			t.Errorf("deploy env-name = %q, want %q", got, "new-env")
		}

		// The binding must end up on the new env; leaving it on the
		// destroyed one would point every later run at a dead name.
		if got := f.boundName(t, "EX-1"); got != "new-env" {
			t.Errorf("env bound to EX-1 = %q, want %q", got, "new-env")
		}
	})

	t.Run("force/proceeds_past_failed_probe", func(t *testing.T) {
		// The bound env's URL answers 500, so the probe is
		// indeterminate rather than negative — bosun cannot tell
		// whether the env is up. --force proceeds anyway, and the
		// redeploy targets the STORED name: generating a fresh one
		// would strand a possibly-live env with its binding gone.
		f := setupPreview(t, "api")
		f.bindEnv("EX-1", "brave-falcon")
		f.env.unreachable("brave-falcon")

		if err := f.run("--force", "--approve"); err != nil {
			t.Fatalf("preview --force: %v", err)
		}

		deploys := f.deploys()
		if len(deploys) != 1 {
			t.Fatalf("deploy dispatches = %d, want 1", len(deploys))
		}
		if got := deploys[0].Inputs["env-name"]; got != "brave-falcon" {
			t.Errorf("env-name input = %q, want the stored %q", got, "brave-falcon")
		}
		if n := len(f.teardowns()); n != 0 {
			t.Errorf("dispatched %d teardown(s) for an unverifiable env; want 0", n)
		}
		if got := f.boundName(t, "EX-1"); got != "brave-falcon" {
			t.Errorf("env bound to EX-1 = %q, want %q", got, "brave-falcon")
		}
	})

	t.Run("errors/probe_failure_without_force", func(t *testing.T) {
		// Same indeterminate probe, no --force. bosun refuses to guess:
		// the run fails rather than redeploying over an env whose state
		// it could not establish, and nothing is dispatched.
		f := setupPreview(t, "api")
		f.bindEnv("EX-1", "brave-falcon")
		f.env.unreachable("brave-falcon")

		err := f.run("--approve")
		if err == nil {
			t.Fatalf("expected the indeterminate probe to surface; got nil")
		}
		if !strings.Contains(err.Error(), "brave-falcon") {
			t.Errorf("error %q does not name the env it could not verify", err)
		}

		if n := len(f.h.CICD.Triggers()); n != 0 {
			t.Errorf("dispatched %d workflow(s) on an unresolved probe; want 0", n)
		}
		if got, _ := f.h.Tracker.Issue("EX-1"); got.Status == "In Preview Env" {
			t.Errorf("failed run advanced issue status; should not have")
		}
	})

	t.Run("plan_confirmation/yes_flag_skips_prompt", func(t *testing.T) {
		// A queued "n" is the probe here. --approve must suppress the
		// plan gate outright; if it regressed to merely pre-filling
		// the prompt, the gate would read that "n" and cancel.
		// Verified by dropping --approve from this exact call: the run
		// fails with "plan cancelled", so the key really does reach
		// the gate and the assertion below really does depend on it.
		//
		// Without the queued key this scenario would be a strict
		// subset of claim/triggers_workflow, and the regression it
		// exists to catch would block on the prompt forever rather
		// than fail.
		//
		// --service keeps the service-selection form out of the way
		// (--approve suppresses it too, but then the same regression
		// would feed the "n" to the form instead of the gate).
		f := setupPreview(t, "api")
		f.h.Type("n")

		if err := f.run("--name", "brave-falcon", "--service", "api-svc", "--approve"); err != nil {
			t.Fatalf("preview: %v", err)
		}

		if n := len(f.deploys()); n != 1 {
			t.Fatalf("deploy dispatches = %d, want 1 (a consumed \"n\" would have cancelled)", n)
		}
		if got := f.boundName(t, "EX-1"); got != "brave-falcon" {
			t.Errorf("env bound to EX-1 = %q, want %q", got, "brave-falcon")
		}
	})

	t.Run("plan_confirmation/dry_run_skips_apply", func(t *testing.T) {
		// Dry-run still walks the interactive service-selection form
		// (enter keeps the pre-checked service) but renders the plan
		// without applying: ErrCancelled, and nothing mutates.
		f := setupPreview(t, "api")
		f.h.Type("\r")

		err := f.run("--name", "brave-falcon", "--dry-run")
		if err == nil {
			t.Fatalf("dry-run should return ErrCancelled; got nil")
		}
		if !strings.Contains(err.Error(), "cancelled") {
			t.Fatalf("dry-run error = %v, want contains \"cancelled\"", err)
		}

		if n := len(f.h.CICD.Triggers()); n != 0 {
			t.Errorf("dry-run dispatched %d workflow(s); want 0", n)
		}
		if got := f.boundName(t, "EX-1"); got != "" {
			t.Errorf("dry-run bound env %q; want nothing bound", got)
		}
		if n := len(f.h.Notifier.Messages()); n != 0 {
			t.Errorf("dry-run sent %d notification(s); want 0", n)
		}
		if got, _ := f.h.Tracker.Issue("EX-1"); got.Status == "In Preview Env" {
			t.Errorf("dry-run advanced issue status; should not have")
		}
	})

	t.Run("plan_confirmation/cancelled_aborts", func(t *testing.T) {
		// Enter submits the selection form with its defaults, then "n"
		// selects Cancel at the plan gate: ErrCancelled with no
		// mutations — no dispatch, no binding, no status change.
		f := setupPreview(t, "api")
		f.h.Type("\r")
		f.h.Type("n")

		// Snapshot tracker calls: the setup's `start` run already
		// called SetStatus (→ In Progress); only calls made by the
		// cancelled preview run matter below.
		before := len(f.h.Tracker.Calls())

		err := f.run("--name", "brave-falcon")
		if err == nil {
			t.Fatalf("expected ErrCancelled; got nil")
		}
		// Pin the genuine decline: runPlanCard maps ANY confirm-form
		// error to bare ErrCancelled ("cancelled"), but only an
		// answered Cancel returns errPlanCancelled ("plan cancelled").
		// A confirm form that died on a read error — e.g. the "n"
		// never reaching it — would fail this.
		if !strings.Contains(err.Error(), "plan cancelled") {
			t.Fatalf("error = %v, want the answered-decline \"plan cancelled\"", err)
		}

		if n := len(f.h.CICD.Triggers()); n != 0 {
			t.Errorf("cancelled run dispatched %d workflow(s); want 0", n)
		}
		if got := f.boundName(t, "EX-1"); got != "" {
			t.Errorf("cancelled run bound env %q; want nothing bound", got)
		}
		if newCalls := f.h.Tracker.Calls()[before:]; slices.Contains(newCalls, "SetStatus") {
			t.Errorf("cancelled run called SetStatus; calls=%v", newCalls)
		}
	})

	t.Run("service_selection/cancelled_aborts", func(t *testing.T) {
		// Ctrl+c at the Services picker — the gate BEFORE the plan.
		// Aborting there has to end the run: resolvePreviewInputs
		// treats the form's error as fatal precisely so a cancelled
		// picker can't fall through to deploying whatever detection
		// happened to pick (or nothing at all, silently).
		f := setupPreview(t, "api")
		f.h.Type("\x03")

		before := len(f.h.Tracker.Calls())

		err := f.run("--name", "brave-falcon")
		if err == nil {
			t.Fatalf("expected ErrCancelled; got nil")
		}
		// Bare "cancelled", not "plan cancelled": the two gates abort
		// through different code, and this asserts the run stopped at
		// the picker rather than sailing on to the plan card.
		if !errors.Is(err, cli.ErrCancelled) {
			t.Fatalf("error = %v, want ErrCancelled", err)
		}
		if strings.Contains(err.Error(), "plan cancelled") {
			t.Fatalf("error = %v, want the abort to happen at the picker, before the plan gate", err)
		}
		// Bracket the abort rather than trusting prompt ordering: the
		// readiness gather (which precedes the picker) reported its
		// repo, and the plan never ran. A ctrl+c swallowed by an
		// earlier prompt would fail the first check.
		var readinessRan bool
		for _, ev := range f.h.Reporter.OfKind(ui.CaptureComplete) {
			if strings.Contains(ev.Label, "api") {
				readinessRan = true
			}
		}
		if !readinessRan {
			t.Fatalf("run aborted before the readiness gather, not at the picker\n%s", f.h.Reporter.Dump())
		}

		if n := len(f.h.CICD.Triggers()); n != 0 {
			t.Errorf("cancelled run dispatched %d workflow(s); want 0", n)
		}
		if got := f.boundName(t, "EX-1"); got != "" {
			t.Errorf("cancelled run bound env %q; want nothing bound", got)
		}
		if newCalls := f.h.Tracker.Calls()[before:]; slices.Contains(newCalls, "SetStatus") {
			t.Errorf("cancelled run called SetStatus; calls=%v", newCalls)
		}
	})

	t.Run("notification/sent_on_success", func(t *testing.T) {
		// preview never opens a review thread — it updates the one
		// `bosun review` opened. With that thread seeded, a successful
		// claim posts the preview into it, carrying the env name and
		// the affected repo's PR.
		f := setupPreview(t, "api")
		f.h.Notifier.SeedThread("prs", "EX-1", notify.ThreadRef{
			Channel: "prs", Timestamp: "1700000000.000100",
		})

		if err := f.run("--name", "brave-falcon", "--approve"); err != nil {
			t.Fatalf("preview: %v", err)
		}

		msgs := f.h.Notifier.Messages()
		if len(msgs) != 1 {
			t.Fatalf("messages = %d, want 1", len(msgs))
		}
		msg := msgs[0]
		if msg.Channel != "prs" {
			t.Errorf("channel = %q, want %q", msg.Channel, "prs")
		}
		if msg.IssueKey != "EX-1" {
			t.Errorf("issue key = %q, want %q", msg.IssueKey, "EX-1")
		}
		// The review notification takes the structured path, so the
		// payload rides on Content — Message.Items stays empty here.
		if msg.Content.Preview == nil {
			t.Fatalf("content carries no preview ref: %+v", msg.Content)
		}
		if got := msg.Content.Preview.Name; got != "brave-falcon" {
			t.Errorf("preview name = %q, want %q", got, "brave-falcon")
		}
		if got, want := msg.Content.Preview.URL, f.env.URL+"/brave-falcon"; got != want {
			t.Errorf("preview URL = %q, want the rendered url_template %q", got, want)
		}
		if msg.Content.Issue == nil || msg.Content.Issue.Key != "EX-1" {
			t.Errorf("content issue = %+v, want EX-1", msg.Content.Issue)
		}
		if len(msg.Content.Items) != 1 {
			t.Fatalf("content items = %d, want 1", len(msg.Content.Items))
		}
		item := msg.Content.Items[0]
		if got, want := item.URL, "https://github.test/acme/api/pull/100"; got != want {
			t.Errorf("item URL = %q, want %q", got, want)
		}
		if item.Detail != "#100" {
			t.Errorf("item detail = %q, want %q", item.Detail, "#100")
		}
		if item.Label != "api" {
			t.Errorf("item label = %q, want %q", item.Label, "api")
		}
	})

	t.Run("notification/skipped_without_existing_thread", func(t *testing.T) {
		// No thread for the issue means review hasn't announced it yet.
		// preview has nothing to update and stays silent — it must not
		// open the thread itself.
		f := setupPreview(t, "api")

		if err := f.run("--name", "brave-falcon", "--approve"); err != nil {
			t.Fatalf("preview: %v", err)
		}

		if n := len(f.h.Notifier.Messages()); n != 0 {
			t.Errorf("sent %d notification(s); want 0 (no thread to update)", n)
		}
		if n := len(f.deploys()); n != 1 {
			t.Errorf("deploy dispatches = %d, want 1 (the deploy is unaffected)", n)
		}
	})

	t.Run("errors/cicd_unconfigured", func(t *testing.T) {
		// No CI/CD pipeline on this project. That is a skip, not a
		// failure: preview reports it, builds no deploy row, and still
		// runs the rest of the plan. Nothing is dispatched and no env
		// is bound, because no env was ever stood up.
		f := setupPreview(t, "api")
		f.h.CICD.NewErr = errors.New("cicd: not configured")

		if err := f.run("--name", "brave-falcon", "--approve"); err != nil {
			t.Fatalf("preview should skip an unconfigured pipeline, not fail: %v", err)
		}

		if n := len(f.h.CICD.Triggers()); n != 0 {
			t.Errorf("dispatched %d workflow(s); want 0", n)
		}
		if got := f.boundName(t, "EX-1"); got != "" {
			t.Errorf("bound env %q with no pipeline to create it", got)
		}
		// "Skip, not fail" means the rest of the plan still applies —
		// the status transition is what proves the run continued past
		// the missing pipeline rather than quietly doing nothing.
		if got, _ := f.h.Tracker.Issue("EX-1"); got.Status != "In Preview Env" {
			t.Errorf("issue status = %q, want %q (the plan still ran)", got.Status, "In Preview Env")
		}
		// Silently doing less than asked is the failure mode here, so
		// the skip has to actually reach the user. Assertable only
		// because the harness captures Reporter calls — h.Stdout() is
		// still empty for this command.
		var reported bool
		for _, ev := range f.h.Reporter.OfKind(ui.CaptureSkip) {
			if strings.Contains(ev.Label, "CI/CD") {
				reported = true
			}
		}
		if !reported {
			t.Errorf("no CI/CD skip reported; the deploy was dropped silently\n%s", f.h.Reporter.Dump())
		}
	})

	t.Run("errors/dispatch_failure_surfaces", func(t *testing.T) {
		// The pipeline rejects the dispatch at apply time. The error
		// propagates out of the plan apply, and the binding is not
		// written — a recorded env that was never deployed would send
		// every later run down the "already claimed" path.
		f := setupPreview(t, "api")
		f.h.CICD.TriggerErr = errors.New("workflow dispatch 422: no ref")

		err := f.run("--name", "brave-falcon", "--approve")
		if err == nil {
			t.Fatalf("expected the dispatch failure to surface; got nil")
		}
		if !strings.Contains(err.Error(), "422") {
			t.Errorf("error %q does not carry the pipeline's message", err)
		}

		if got := f.boundName(t, "EX-1"); got != "" {
			t.Errorf("failed dispatch bound env %q; want nothing bound", got)
		}
		if n := len(f.h.Notifier.Messages()); n != 0 {
			t.Errorf("failed run sent %d notification(s); want 0", n)
		}
	})
}
