package fakes

import (
	"context"
	"sync"

	"github.com/nickawilliams/bosun/internal/preview"
)

// Preview is an in-memory preview.Provider. Seed environments via
// SeedEnv before invoking commands; inspect Envs(), Destroyed(), and
// Calls() after to assert on what was provisioned or torn down. Safe
// for concurrent use.
//
// Unlike Tracker and Host, this fake is NOT installed by
// testharness.New — the PreviewProvider factory defaults to a
// fail-loudly stub so a command that unexpectedly reaches for a
// preview env surfaces the gap. Tests for commands that legitimately
// use one install it via Harness.InstallPreview.
type Preview struct {
	mu sync.Mutex

	// envs are the bound environments, keyed by issue key.
	envs map[string]preview.Environment

	// destroyed records every Destroy call as "issueKey|name", in order.
	destroyed []string

	// GetErr, CreateErr, DestroyErr override default behavior to force
	// error paths. nil means use the default success behavior.
	//
	// GetErr stands in for an indeterminate probe, and Get returns it
	// the way the real contract specifies: alongside the seeded
	// Environment, not instead of it. preview.Provider.Get documents
	// that an indeterminate probe still yields Name / URL / IssueKey so
	// callers that only need the binding can use it — and callers do.
	// cleanup's teardown row reads env.Name off exactly this path and
	// passes it to Destroy, so a fake that zeroed the Environment here
	// would let a test lock in "Destroy receives an empty name" and
	// pass, while the real provider hands over the real one.
	GetErr     error
	CreateErr  error
	DestroyErr error

	// calls records the method names invoked, in order.
	calls []string
}

// NewPreview constructs an empty Preview.
func NewPreview() *Preview {
	return &Preview{envs: map[string]preview.Environment{}}
}

// SeedEnv binds env to issueKey so Get returns it. Overwrites any
// prior entry for the same key.
func (p *Preview) SeedEnv(issueKey string, env preview.Environment) *Preview {
	p.mu.Lock()
	defer p.mu.Unlock()
	env.IssueKey = issueKey
	p.envs[issueKey] = env
	return p
}

// Env returns the environment bound to issueKey, or (zero, false).
func (p *Preview) Env(issueKey string) (preview.Environment, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	env, ok := p.envs[issueKey]
	return env, ok
}

// Destroyed returns a snapshot of the "issueKey|name" pairs passed to
// Destroy, in call order.
func (p *Preview) Destroyed() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.destroyed))
	copy(out, p.destroyed)
	return out
}

// Calls returns a snapshot of method calls in invocation order.
func (p *Preview) Calls() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.calls))
	copy(out, p.calls)
	return out
}

func (p *Preview) recordCall(name string) {
	p.calls = append(p.calls, name)
}

// --- preview.Provider implementation ---

func (p *Preview) Get(_ context.Context, issueKey string) (preview.Environment, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.recordCall("Get")
	env, ok := p.envs[issueKey]
	if p.GetErr != nil {
		// Partially-populated result + error, per the Provider
		// contract. Probed/Alive stay false; whatever the test seeded
		// for Name/URL comes back. Nothing seeded → zero Environment,
		// which is also a legal indeterminate result.
		return env, p.GetErr
	}
	if !ok {
		return preview.Environment{}, preview.ErrNoEnvironment
	}
	return env, nil
}

func (p *Preview) Inspect(_ context.Context, name string) (preview.Environment, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.recordCall("Inspect")
	if name == "" {
		return preview.Environment{}, preview.ErrNoEnvironment
	}
	for _, env := range p.envs {
		if env.Name == name {
			env.IssueKey = ""
			return env, nil
		}
	}
	return preview.Environment{}, preview.ErrNoEnvironment
}

func (p *Preview) Create(_ context.Context, claim preview.Claim) (preview.Environment, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.recordCall("Create")
	if p.CreateErr != nil {
		return preview.Environment{}, p.CreateErr
	}
	env := preview.Environment{Name: claim.Name, IssueKey: claim.IssueKey}
	p.envs[claim.IssueKey] = env
	return env, nil
}

func (p *Preview) Adopt(_ context.Context, issueKey, name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.recordCall("Adopt")
	p.envs[issueKey] = preview.Environment{Name: name, IssueKey: issueKey}
	return nil
}

func (p *Preview) Destroy(_ context.Context, issueKey, name string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.recordCall("Destroy")
	if p.DestroyErr != nil {
		return p.DestroyErr
	}
	p.destroyed = append(p.destroyed, issueKey+"|"+name)
	delete(p.envs, issueKey)
	return nil
}

// Verify Preview satisfies preview.Provider at compile time.
var _ preview.Provider = (*Preview)(nil)
