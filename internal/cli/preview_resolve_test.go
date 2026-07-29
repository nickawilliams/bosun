package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/preview"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
)

// fakePreviewProvider drives resolvePreview's probes. Get returns the
// metadata binding; Inspect answers by name. Mutating methods are
// never reached by resolution (it plans; the actions apply).
type fakePreviewProvider struct {
	getEnv  preview.Environment
	getErr  error
	inspect map[string]preview.Environment
}

func (f *fakePreviewProvider) Get(context.Context, string) (preview.Environment, error) {
	return f.getEnv, f.getErr
}

func (f *fakePreviewProvider) Inspect(_ context.Context, name string) (preview.Environment, error) {
	if env, ok := f.inspect[name]; ok {
		return env, nil
	}
	return preview.Environment{}, preview.ErrNoEnvironment
}

func (f *fakePreviewProvider) Create(context.Context, preview.Claim) (preview.Environment, error) {
	return preview.Environment{}, nil
}
func (f *fakePreviewProvider) Adopt(context.Context, string, string) error { return nil }
func (f *fakePreviewProvider) Destroy(context.Context, string, string) error {
	return nil
}

// resolveWith runs resolvePreview in raw/non-interactive mode with an
// optional --name flag value.
func resolveWith(t *testing.T, p preview.Provider, flagName string, force bool) (previewResolution, error) {
	t.Helper()
	prev := ui.IsRaw()
	ui.SetDefault(ui.NewRawReporter())
	t.Cleanup(func() {
		if !prev {
			ui.SetDefault(ui.NewCardReporter())
		}
	})

	cmd := &cobra.Command{Use: "preview"}
	cmd.Flags().String("name", "", "")
	if flagName != "" {
		_ = cmd.Flags().Set("name", flagName)
	}
	return resolvePreview(cmd, context.Background(), p, "EX-1", "preview", force)
}

// alive/dead/unprobed build the three probe outcomes.
func alive(name string) preview.Environment {
	return preview.Environment{Name: name, Probed: true, Alive: true}
}
func dead(name string) preview.Environment {
	return preview.Environment{Name: name, Probed: true, Alive: false}
}
func unprobed(name string) preview.Environment {
	return preview.Environment{Name: name}
}

// TestResolvePreviewMatrix locks the --name × stored-metadata
// resolution matrix's non-interactive outcomes (the review found the
// 5-row switch entirely untested; two of its edges had live-env
// teardown bugs).
func TestResolvePreviewMatrix(t *testing.T) {
	t.Run("row 1: nothing bound → generated name deploys", func(t *testing.T) {
		res, err := resolveWith(t, &fakePreviewProvider{getErr: preview.ErrNoEnvironment}, "", false)
		if err != nil {
			t.Fatalf("resolvePreview: %v", err)
		}
		if res.previewName == "" || res.deployName != res.previewName {
			t.Errorf("res = %+v, want a generated name deploying under itself", res)
		}
		if res.isCurrent || res.isRedeploy || res.teardownName != "" {
			t.Errorf("row 1 should be a plain create: %+v", res)
		}
	})

	t.Run("row 2: flag name free → deploys under flag name", func(t *testing.T) {
		res, err := resolveWith(t, &fakePreviewProvider{getErr: preview.ErrNoEnvironment}, "wanted-name", false)
		if err != nil {
			t.Fatalf("resolvePreview: %v", err)
		}
		if res.previewName != "wanted-name" || res.deployName != "wanted-name" {
			t.Errorf("res = %+v, want deploy under the flag name", res)
		}
	})

	t.Run("row 2: flag name alive, no force → conflict error", func(t *testing.T) {
		p := &fakePreviewProvider{
			getErr:  preview.ErrNoEnvironment,
			inspect: map[string]preview.Environment{"taken": alive("taken")},
		}
		_, err := resolveWith(t, p, "taken", false)
		if err == nil || !strings.Contains(err.Error(), "--force") {
			t.Errorf("err = %v, want a conflict error pointing at --force", err)
		}
	})

	t.Run("row 2: flag name alive + force → redeploy", func(t *testing.T) {
		p := &fakePreviewProvider{
			getErr:  preview.ErrNoEnvironment,
			inspect: map[string]preview.Environment{"taken": alive("taken")},
		}
		res, err := resolveWith(t, p, "taken", true)
		if err != nil {
			t.Fatalf("resolvePreview: %v", err)
		}
		if res.deployName != "taken" || !res.isRedeploy {
			t.Errorf("res = %+v, want forced redeploy of the flag name", res)
		}
	})

	t.Run("row 3: metadata alive → current, nothing deploys", func(t *testing.T) {
		res, err := resolveWith(t, &fakePreviewProvider{getEnv: alive("bound")}, "", false)
		if err != nil {
			t.Fatalf("resolvePreview: %v", err)
		}
		if !res.isCurrent || res.deployName != "" || res.teardownName != "" {
			t.Errorf("res = %+v, want isCurrent no-op", res)
		}
	})

	t.Run("row 3: metadata alive + force → redeploy stored name", func(t *testing.T) {
		res, err := resolveWith(t, &fakePreviewProvider{getEnv: alive("bound")}, "", true)
		if err != nil {
			t.Fatalf("resolvePreview: %v", err)
		}
		if res.deployName != "bound" || !res.isRedeploy {
			t.Errorf("res = %+v, want forced redeploy of the stored name", res)
		}
	})

	t.Run("row 3: metadata unprobable → redeploy stored name", func(t *testing.T) {
		res, err := resolveWith(t, &fakePreviewProvider{getEnv: unprobed("bound")}, "", false)
		if err != nil {
			t.Fatalf("resolvePreview: %v", err)
		}
		if res.deployName != "bound" || !res.isRedeploy {
			t.Errorf("res = %+v, want stored-name redeploy on unverifiable env", res)
		}
	})

	t.Run("row 3: metadata dead → recreate under stored name", func(t *testing.T) {
		res, err := resolveWith(t, &fakePreviewProvider{getEnv: dead("bound")}, "", false)
		if err != nil {
			t.Fatalf("resolvePreview: %v", err)
		}
		if res.deployName != "bound" || res.isRedeploy || res.isCurrent {
			t.Errorf("res = %+v, want a fresh create under the stored name", res)
		}
	})

	t.Run("row 4: flag equals metadata, alive → current", func(t *testing.T) {
		res, err := resolveWith(t, &fakePreviewProvider{getEnv: alive("bound")}, "bound", false)
		if err != nil {
			t.Fatalf("resolvePreview: %v", err)
		}
		if !res.isCurrent || res.deployName != "" {
			t.Errorf("res = %+v, want isCurrent no-op", res)
		}
	})

	t.Run("row 5: different names, stale metadata alive → teardown + deploy", func(t *testing.T) {
		res, err := resolveWith(t, &fakePreviewProvider{getEnv: alive("old-env")}, "new-env", false)
		if err != nil {
			t.Fatalf("resolvePreview: %v", err)
		}
		if res.deployName != "new-env" || res.teardownName != "old-env" {
			t.Errorf("res = %+v, want new-env deployed and old-env torn down", res)
		}
	})

	t.Run("row 5: both alive + force → redeploy flag, teardown metadata", func(t *testing.T) {
		p := &fakePreviewProvider{
			getEnv:  alive("old-env"),
			inspect: map[string]preview.Environment{"new-env": alive("new-env")},
		}
		res, err := resolveWith(t, p, "new-env", true)
		if err != nil {
			t.Fatalf("resolvePreview: %v", err)
		}
		if res.deployName != "new-env" || !res.isRedeploy || res.teardownName != "old-env" {
			t.Errorf("res = %+v, want forced redeploy with stale-env teardown", res)
		}
		if res.teardownName == res.previewName {
			t.Errorf("resolution tears down the env it resolved to: %+v", res)
		}
	})

	t.Run("probe error + force keeps the stored binding", func(t *testing.T) {
		// Regression: the env returned alongside a ProbeError carries
		// the stored binding; discarding it routed to Row 1, deployed
		// a fresh generated name, and orphaned the live env.
		p := &fakePreviewProvider{
			getEnv: preview.Environment{Name: "bound", URL: "https://bound.example"},
			getErr: &preview.ProbeError{URL: "https://bound.example", Err: errors.New("timeout")},
		}
		res, err := resolveWith(t, p, "", true)
		if err != nil {
			t.Fatalf("resolvePreview: %v", err)
		}
		if res.deployName != "bound" || !res.isRedeploy {
			t.Errorf("res = %+v, want stored-name redeploy, not a fresh generated name", res)
		}
	})

	t.Run("probe error without force is an error", func(t *testing.T) {
		p := &fakePreviewProvider{
			getEnv: preview.Environment{Name: "bound", URL: "https://bound.example"},
			getErr: &preview.ProbeError{URL: "https://bound.example", Err: errors.New("timeout")},
		}
		if _, err := resolveWith(t, p, "", false); err == nil {
			t.Error("expected the indeterminate probe to surface as an error without --force")
		}
	})
}
