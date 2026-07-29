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
var ErrNoPipeline = errors.New("preview: no CI/CD pipeline configured")

// ErrNoWorkflow is returned when the requested sub-stage has no
// workflow targets configured.
var ErrNoWorkflow = errors.New("preview: no workflow configured for stage")

// New returns a Provider that dispatches workflows via the configured
// cicd.CICD and persists the env-to-issue binding on the tracker.
func New(opts Options) preview.Provider {
	return &provider{opts: opts}
}

type provider struct {
	opts Options
}

// stageURLData is the template context passed to URLTemplate.
type stageURLData struct {
	Name string
}

// registryEntry is the JSON shape stored at the issue's preview_name
// property: {"preview_name": "brave-falcon"}.
type registryEntry struct {
	PreviewName string `json:"preview_name"`
}

func (p *provider) Get(ctx context.Context, issueKey string) (preview.Environment, error) {
	name, err := p.readName(ctx, issueKey)
	if err != nil || name == "" {
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
	env.Alive = alive
	return env, nil
}

func (p *provider) Inspect(ctx context.Context, name string) (preview.Environment, error) {
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
	env.Alive = alive
	return env, nil
}

func (p *provider) Create(ctx context.Context, claim preview.Claim) (preview.Environment, error) {
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

	if p.opts.Tracker != nil {
		_ = p.writeName(ctx, claim.IssueKey, claim.Name)
	}

	return preview.Environment{
		Name:     claim.Name,
		URL:      p.renderURL(claim.Name),
		IssueKey: claim.IssueKey,
	}, nil
}

func (p *provider) Adopt(ctx context.Context, issueKey, name string) error {
	if name == "" {
		return errors.New("preview: adopt name is empty")
	}
	if p.opts.Tracker == nil {
		return nil
	}
	return p.writeName(ctx, issueKey, name)
}

func (p *provider) Destroy(ctx context.Context, issueKey, name string) error {
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

	if p.opts.Tracker != nil {
		_ = p.opts.Tracker.DeleteProperty(ctx, issueKey)
	}
	return nil
}

func (p *provider) buildDeployInputs(subStage string, claim preview.Claim) map[string]string {
	inputs := make(map[string]string)
	if k := p.opts.InputName(subStage, "name"); k != "" {
		inputs[k] = claim.Name
	}
	if k := p.opts.InputName(subStage, "issue"); k != "" {
		inputs[k] = claim.IssueKey
	}
	if k := p.opts.InputName(subStage, "services"); k != "" && len(claim.Services) > 0 {
		inputs[k] = strings.Join(claim.Services, ",")
	}
	if len(claim.Overrides) > 0 {
		if b, err := json.Marshal(claim.Overrides); err == nil {
			inputs["image-overrides"] = string(b)
		}
	}
	return inputs
}

// readName reads the preview_name property from the tracker. Returns
// empty string for any non-success path: missing property, nil tracker,
// unexpected JSON shape, or tracker error. Matches the legacy
// fetchExistingPreviewName behavior.
func (p *provider) readName(ctx context.Context, issueKey string) (string, error) {
	if p.opts.Tracker == nil {
		return "", nil
	}
	raw, err := p.opts.Tracker.GetProperty(ctx, issueKey)
	if err != nil || raw == nil {
		return "", nil
	}
	var entry registryEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		return "", nil
	}
	return entry.PreviewName, nil
}

func (p *provider) writeName(ctx context.Context, issueKey, name string) error {
	return p.opts.Tracker.SetProperty(ctx, issueKey, registryEntry{PreviewName: name})
}

func (p *provider) renderURL(name string) string {
	if p.opts.URLTemplate == nil || name == "" {
		return ""
	}
	var buf strings.Builder
	if err := p.opts.URLTemplate.Execute(&buf, stageURLData{Name: name}); err != nil {
		return ""
	}
	return buf.String()
}
