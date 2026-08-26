// Package cicd implements preview.Provider on top of a CI/CD pipeline
// (cicd.CICD) and an issue tracker (issue.Tracker). It dispatches
// preview.up / preview.down workflows for create/destroy and stores the
// env-to-issue binding as a JSON property on the issue.
package cicd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"text/template"

	"github.com/nickawilliams/bosun/internal/cicd"
	"github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/preview"
)

// Target identifies a workflow target the adapter should dispatch to.
type Target struct {
	Owner    string
	Repo     string
	Workflow string
	Label    string // Display label (typically the local repo name).
}

// Options configures the adapter. Pipeline and Tracker may be nil to
// indicate the feature is unavailable; Targets and InputName must be
// non-nil. URLTemplate may be nil — without it, the adapter cannot
// probe or report URLs.
type Options struct {
	Pipeline    cicd.CICD     // workflow dispatcher; may be nil
	Tracker     issue.Tracker // registry; may be nil
	Stage       string        // lifecycle stage prefix, typically "preview"
	URLTemplate *template.Template
	Targets     func(ctx context.Context, subStage string) ([]Target, error)
	InputName   func(subStage, concept string) string
}

// ErrNoPipeline is returned by Create and Destroy when no CI/CD
// pipeline is configured. Callers should treat this like the
// existing "skip" path in the preview command rather than a fatal error.
//
// It matches preview.ErrNotConfigured so a caller holding only the
// interface can recognize "this provider has no backend" without
// importing the adapter to name its sentinel.
var ErrNoPipeline = notConfigured("preview: no CI/CD pipeline configured")

// ErrNoWorkflow is returned when the requested sub-stage has no
// workflow targets configured. Matches preview.ErrNotConfigured for the
// same reason as ErrNoPipeline.
var ErrNoWorkflow = notConfigured("preview: no workflow configured for stage")

// notConfigured is an error that answers to preview.ErrNotConfigured
// while keeping its own message.
//
// A `fmt.Errorf("%w: …", preview.ErrNotConfigured)` wrap would prefix
// every one of these with "preview: provider not configured", turning
// a one-line skip notice into a doubled sentence — and these strings
// are what the preview and cleanup commands print.
type notConfigured string

func (e notConfigured) Error() string { return string(e) }

func (e notConfigured) Is(target error) bool { return target == preview.ErrNotConfigured }

// New returns a Provider that dispatches workflows via the configured
// cicd.CICD and persists the env-to-issue binding on the tracker.
func New(opts Options) preview.Provider {
	return &adapter{opts: opts, binding: preview.Binding{Store: opts.Tracker}}
}

type adapter struct {
	opts    Options
	binding preview.Binding
}

// stageURLData is the template context passed to URLTemplate.
type stageURLData struct {
	Name string
}

// probeStatus maps a reachability probe onto the status enum. A probe
// only ever answers "serving" or "not there" — it cannot see a
// provision in flight or a partial deploy — so this adapter reports
// exactly two of the five states.
func probeStatus(alive bool) preview.Status {
	if alive {
		return preview.StatusActive
	}
	return preview.StatusGone
}

func (p *adapter) Get(ctx context.Context, issueKey string) (preview.Environment, error) {
	name := p.binding.Name(ctx, issueKey)
	if name == "" {
		return preview.Environment{}, preview.ErrNoEnvironment
	}

	env := preview.Environment{
		Name:     name,
		IssueKey: issueKey,
		URL:      p.renderURL(name),
	}
	if env.URL == "" {
		// No URL template — treat as exists-but-unverifiable.
		return env, nil
	}

	alive, perr := httpProbe(ctx, env.URL)
	if perr != nil {
		// Indeterminate. Surface the name+URL alongside the error so
		// callers that don't strictly need a verified state can still
		// render what's bound; callers that need verification can
		// branch on the error.
		return env, &preview.ProbeError{URL: env.URL, Err: perr}
	}
	// Get is a pure read — report the probe result without mutating the
	// registry. A definitive-dead probe (404) means the env isn't
	// reachable: it may have been torn down, or a just-triggered deploy
	// may still be in flight (the workflow dispatch is async). Either way
	// the binding stays, so a read (e.g. `bosun status`) can't clobber a
	// pending deploy. Self-healing is the preview command's job —
	// resolvePreview recreates under the stored name and Create
	// overwrites the binding on the next deploy.
	env.Probed = true
	env.Status = probeStatus(alive)
	return env, nil
}

func (p *adapter) Inspect(ctx context.Context, name string) (preview.Environment, error) {
	if name == "" {
		return preview.Environment{}, preview.ErrNoEnvironment
	}
	env := preview.Environment{
		Name: name,
		URL:  p.renderURL(name),
	}
	if env.URL == "" {
		// No URL template — caller treats as unprobable.
		return env, nil
	}
	alive, perr := httpProbe(ctx, env.URL)
	if perr != nil {
		// Same contract as Get: surface name+URL alongside ProbeError so
		// callers (resolvePreview's --force fallback) can detect the
		// probe-error case via errors.As and report the URL.
		return env, &preview.ProbeError{URL: env.URL, Err: perr}
	}
	env.Probed = true
	env.Status = probeStatus(alive)
	return env, nil
}

func (p *adapter) Create(ctx context.Context, claim preview.Claim) (preview.Environment, error) {
	if claim.Name == "" {
		return preview.Environment{}, errors.New("preview: claim name is empty")
	}
	if p.opts.Pipeline == nil {
		return preview.Environment{}, ErrNoPipeline
	}

	subStage := p.opts.Stage + ".up"
	targets, err := p.opts.Targets(ctx, subStage)
	if err != nil {
		return preview.Environment{}, err
	}
	if len(targets) == 0 {
		return preview.Environment{}, ErrNoWorkflow
	}

	inputs := p.buildDeployInputs(subStage, claim)

	for _, t := range targets {
		if err := p.opts.Pipeline.TriggerWorkflow(ctx, cicd.TriggerRequest{
			Owner:      t.Owner,
			Repository: t.Repo,
			Workflow:   t.Workflow,
			Ref:        "main",
			Inputs:     inputs,
		}); err != nil {
			return preview.Environment{}, err
		}
	}

	_ = p.binding.Bind(ctx, claim.IssueKey, claim.Name)

	return preview.Environment{
		Name:     claim.Name,
		URL:      p.renderURL(claim.Name),
		IssueKey: claim.IssueKey,
	}, nil
}

func (p *adapter) Adopt(ctx context.Context, issueKey, name string) error {
	if name == "" {
		return errors.New("preview: adopt name is empty")
	}
	return p.binding.Bind(ctx, issueKey, name)
}

func (p *adapter) Destroy(ctx context.Context, issueKey, name string) error {
	if strings.TrimSpace(name) == "" {
		return errors.New("preview: refusing to destroy without an env name")
	}
	if p.opts.Pipeline == nil {
		return ErrNoPipeline
	}

	subStage := p.opts.Stage + ".down"
	targets, err := p.opts.Targets(ctx, subStage)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return ErrNoWorkflow
	}

	nameKey := p.opts.InputName(subStage, "name")
	if nameKey == "" {
		// Without a name input the workflow would be invoked with no
		// name; teardown workflows commonly interpret that as "clean
		// everything." Refuse rather than risk it.
		return fmt.Errorf("preview: %s workflow has no name input configured", subStage)
	}
	issueInputKey := p.opts.InputName(subStage, "issue")

	inputs := map[string]string{nameKey: name}
	if issueInputKey != "" {
		inputs[issueInputKey] = issueKey
	}

	for _, t := range targets {
		if err := p.opts.Pipeline.TriggerWorkflow(ctx, cicd.TriggerRequest{
			Owner:      t.Owner,
			Repository: t.Repo,
			Workflow:   t.Workflow,
			Ref:        "main",
			Inputs:     inputs,
		}); err != nil {
			return err
		}
	}

	_ = p.binding.Unbind(ctx, issueKey)
	return nil
}

func (p *adapter) buildDeployInputs(subStage string, claim preview.Claim) map[string]string {
	inputs := make(map[string]string)
	if k := p.opts.InputName(subStage, "name"); k != "" {
		inputs[k] = claim.Name
	}
	if k := p.opts.InputName(subStage, "issue"); k != "" {
		inputs[k] = claim.IssueKey
	}
	// No services input. A "deploy only these" filter leaves the
	// environment half-built, so the concept is gone from both halves:
	// there is no config key naming the input, and nothing to put in it.
	// Per-service information reaches the workflow through the overrides
	// below, which pin tags without narrowing the deploy.
	if len(claim.Overrides) > 0 {
		if b, err := json.Marshal(claim.Overrides); err == nil {
			inputs["image-overrides"] = string(b)
		}
	}
	return inputs
}

func (p *adapter) renderURL(name string) string {
	if p.opts.URLTemplate == nil || name == "" {
		return ""
	}
	var buf strings.Builder
	if err := p.opts.URLTemplate.Execute(&buf, stageURLData{Name: name}); err != nil {
		return ""
	}
	return buf.String()
}
