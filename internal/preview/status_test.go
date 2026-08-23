package preview

import (
	"errors"
	"fmt"
	"testing"

	"github.com/nickawilliams/bosun/internal/provider"
)

// TestStatusPredicates pins the two questions callers actually ask of
// the enum. Alive drives whether an env is usable; Pending separates
// "not reachable yet" from "not there" — the distinction the whole
// taxonomy exists for, since a probe-only provider can't make it.
func TestStatusPredicates(t *testing.T) {
	cases := []struct {
		status         Status
		alive, pending bool
	}{
		{StatusUnknown, false, false},
		{StatusCreating, false, true},
		{StatusActive, true, false},
		{StatusDegraded, true, false},
		{StatusDeleting, false, true},
		{StatusGone, false, false},
	}
	for _, tc := range cases {
		t.Run(string(tc.status)+"|", func(t *testing.T) {
			if got := tc.status.Alive(); got != tc.alive {
				t.Errorf("Alive() = %v, want %v", got, tc.alive)
			}
			if got := tc.status.Pending(); got != tc.pending {
				t.Errorf("Pending() = %v, want %v", got, tc.pending)
			}
		})
	}
}

// TestEnvironmentAliveRequiresAProbe pins the pairing every caller
// needs: a status is meaningless without Probed, and folding the two
// together at each call site is how the old bool pair got misread.
func TestEnvironmentAliveRequiresAProbe(t *testing.T) {
	if (Environment{Status: StatusActive}).Alive() {
		t.Error("Alive() = true for an unprobed environment")
	}
	if !(Environment{Status: StatusActive, Probed: true}).Alive() {
		t.Error("Alive() = false for a probed, active environment")
	}
	if (Environment{Status: StatusGone, Probed: true}).Alive() {
		t.Error("Alive() = true for a gone environment")
	}
}

// strictProvider implements NameValidator with a grammar stricter than
// ValidateName's, standing in for a backend that rejects names the
// shared floor accepts.
type strictProvider struct{ Provider }

func (strictProvider) ValidateName(name string) error {
	if len(name) < 5 {
		return fmt.Errorf("too short: %q", name)
	}
	return nil
}

// looseProvider implements Provider but not NameValidator.
type looseProvider struct{ Provider }

func TestProviderValidateName(t *testing.T) {
	// A provider with no grammar of its own falls back to the shared
	// floor, which is what every caller got before the hook existed.
	if err := ProviderValidateName(looseProvider{}, "brave-falcon"); err != nil {
		t.Errorf("ProviderValidateName = %v, want nil", err)
	}
	if err := ProviderValidateName(looseProvider{}, "Brave"); err == nil {
		t.Error("ProviderValidateName accepted an uppercase name")
	}

	// A provider that declares one is the rule the user is held to,
	// even where it disagrees with the floor.
	if err := ProviderValidateName(strictProvider{}, "abc"); err == nil {
		t.Error("ProviderValidateName ignored the provider's stricter grammar")
	}
	if err := ValidateName("abc"); err != nil {
		t.Errorf("ValidateName(%q) = %v; the shared floor should accept it", "abc", err)
	}

	// A nil provider still validates: display paths reach here without
	// having built one.
	if err := ProviderValidateName(nil, ""); err == nil {
		t.Error("ProviderValidateName(nil, \"\") accepted an empty name")
	}
}

// TestNotConfiguredSentinelWraps pins the graceful-degradation contract:
// an adapter's own "nothing to talk to" error must be recognizable
// through the interface, so callers skip the step instead of failing.
func TestNotConfiguredSentinelWraps(t *testing.T) {
	wrapped := fmt.Errorf("%w: no pipeline", ErrNotConfigured)
	if !errors.Is(wrapped, ErrNotConfigured) {
		t.Error("a wrapped ErrNotConfigured is not recognized")
	}
	if errors.Is(wrapped, ErrNoEnvironment) {
		t.Error("ErrNotConfigured matches ErrNoEnvironment; the sentinels are not distinct")
	}
	if errors.Is(ErrAuth, ErrNotConfigured) {
		t.Error("ErrAuth matches ErrNotConfigured; the sentinels are not distinct")
	}
}

// TestDepsZeroValueIsUsable pins that a provider can be constructed
// with no wiring at all — the shape every read-only display path builds.
func TestDepsZeroValueIsUsable(t *testing.T) {
	var deps Deps
	if deps.Tracker != nil {
		t.Error("zero Deps carries a tracker")
	}
	if deps.Workflow.Pipeline != nil {
		t.Error("zero Deps carries a pipeline")
	}
	if deps.Workflow.Targets != nil {
		t.Error("zero Deps carries a target resolver")
	}
	// And the descriptor's New signature accepts it, which is what the
	// services registry relies on to build a catalog without deps.
	d := ProviderDescriptor{
		Name: "stub",
		New:  func(provider.Config, Deps) (Provider, error) { return nil, nil },
	}
	if _, err := d.New(nil, deps); err != nil {
		t.Errorf("New(nil, zero Deps) = %v, want nil", err)
	}
}
