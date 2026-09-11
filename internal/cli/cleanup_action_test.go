package cli

// Unit coverage for cleanupPreviewAction's Assess outcomes — the
// mapping from what the provider's Get says to how the plan row
// behaves. The full command path (teardown ordering, plan gating) is
// exercised end-to-end in cleanup_test.go.

import (
	"context"
	"errors"
	"testing"

	"github.com/nickawilliams/bosun/internal/preview"
)

// stubCleanupProvider is the minimal preview.Provider for driving
// cleanupPreviewAction: Ready always answers wired, Get returns the
// scripted result, and the mutating methods are never reached by
// Assess.
type stubCleanupProvider struct {
	env    preview.Environment
	getErr error
}

func (s *stubCleanupProvider) Ready(context.Context, preview.Operation) error { return nil }
func (s *stubCleanupProvider) Get(context.Context, string) (preview.Environment, error) {
	return s.env, s.getErr
}
func (s *stubCleanupProvider) Inspect(context.Context, string) (preview.Environment, error) {
	return preview.Environment{}, preview.ErrNoEnvironment
}
func (s *stubCleanupProvider) Create(context.Context, preview.Claim) (preview.Environment, error) {
	return preview.Environment{}, errors.New("not implemented")
}
func (s *stubCleanupProvider) Adopt(context.Context, string, string) error {
	return errors.New("not implemented")
}
func (s *stubCleanupProvider) Destroy(context.Context, string, string) error { return nil }

func TestCleanupPreviewActionAssess(t *testing.T) {
	tests := []struct {
		name       string
		provider   *stubCleanupProvider
		wantState  ActionState
		wantDetail string
	}{
		{
			// A definitive "no env" omits the row entirely: a plan row
			// for a subject that doesn't exist asserts nothing.
			name:      "no_env_omits_row",
			provider:  &stubCleanupProvider{getErr: preview.ErrNoEnvironment},
			wantState: ActionSkipped,
		},
		{
			// An indeterminate probe still plans the teardown (unknown
			// ≠ nonexistent — a stale registry entry must not strand an
			// env), with the placeholder detail.
			name:       "indeterminate_probe_keeps_teardown",
			provider:   &stubCleanupProvider{getErr: errors.New("probe timeout")},
			wantState:  ActionNeeded,
			wantDetail: "(unknown)",
		},
		{
			name: "bound_env_plans_teardown",
			provider: &stubCleanupProvider{
				env: preview.Environment{Name: "brave-falcon"},
			},
			wantState:  ActionNeeded,
			wantDetail: "brave-falcon",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			ready := &previewReadiness{provider: tt.provider}
			action := cleanupPreviewAction(ctx, ready, tt.provider, "EX-1")

			state, detail, err := action.Assess(ctx)
			if err != nil {
				t.Fatalf("Assess: %v", err)
			}
			if state != tt.wantState {
				t.Errorf("state = %v, want %v", state, tt.wantState)
			}
			if state == ActionNeeded && detail != tt.wantDetail {
				t.Errorf("detail = %q, want %q", detail, tt.wantDetail)
			}
		})
	}
}
