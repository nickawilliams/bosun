package cli_test

// End-to-end scenarios for `bosun preview list`. The fleet listing is
// the discovery half of the provider contract: adoption used to require
// knowing an env's name from outside bosun, and this is where you find
// it. Rendering and filtering are unit-tested in preview_list_test.go
// (package cli); this file pins the command path — which provider gets
// asked, and what the user sees when it can't answer.

import (
	"errors"
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/cli"
	"github.com/nickawilliams/bosun/internal/preview"
	"github.com/nickawilliams/bosun/internal/testharness"
	"github.com/nickawilliams/bosun/internal/ui"
)

func TestPreviewList_RendersTheFleet(t *testing.T) {
	h := testharness.New(t)
	fake := h.InstallPreview()
	fake.SeedFleet(
		preview.Environment{Name: "wobbly-turtle", Probed: true, Status: preview.StatusCreating, DeployedBy: "dana"},
		preview.Environment{Name: "brave-falcon", Probed: true, Status: preview.StatusActive, DeployedBy: "nick"},
	)

	if err := h.Run("preview", "list"); err != nil {
		t.Fatalf("preview list: %v", err)
	}

	details := h.Reporter.OfKind(ui.CaptureDetails)
	if len(details) != 1 {
		t.Fatalf("got %d detail blocks, want 1\n%s", len(details), h.Reporter.Dump())
	}
	items := strings.Join(details[0].Items, "\n")
	// Someone else's env is listed too — that is the point of a shared
	// fleet, and adoption needs to see it.
	for _, want := range []string{"brave-falcon", "active", "nick", "wobbly-turtle", "deploying", "dana"} {
		if !strings.Contains(items, want) {
			t.Errorf("listing omits %q:\n%s", want, items)
		}
	}
	if !slicesContains(fake.Calls(), "List") {
		t.Errorf("provider.List was never called; calls = %v", fake.Calls())
	}
}

func TestPreviewList_FiltersByUser(t *testing.T) {
	h := testharness.New(t)
	h.InstallPreview().SeedFleet(
		preview.Environment{Name: "brave-falcon", Probed: true, Status: preview.StatusActive, DeployedBy: "nick"},
		preview.Environment{Name: "wobbly-turtle", Probed: true, Status: preview.StatusActive, DeployedBy: "dana"},
	)

	if err := h.Run("preview", "list", "--user", "dana"); err != nil {
		t.Fatalf("preview list: %v", err)
	}

	details := h.Reporter.OfKind(ui.CaptureDetails)
	if len(details) != 1 {
		t.Fatalf("got %d detail blocks, want 1\n%s", len(details), h.Reporter.Dump())
	}
	items := strings.Join(details[0].Items, "\n")
	if !strings.Contains(items, "wobbly-turtle") {
		t.Errorf("listing dropped the matching env:\n%s", items)
	}
	// The API has no user parameter, so the filter is applied here. A
	// filter that silently did nothing would still look like a pass
	// against a single-owner fleet.
	if strings.Contains(items, "brave-falcon") {
		t.Errorf("listing kept another user's env:\n%s", items)
	}
}

func TestPreviewList_EmptyFleet(t *testing.T) {
	h := testharness.New(t)
	h.InstallPreview()

	if err := h.Run("preview", "list"); err != nil {
		t.Fatalf("preview list: %v", err)
	}

	if got := h.Reporter.OfKind(ui.CaptureDetails); len(got) != 0 {
		t.Errorf("rendered a detail block for an empty fleet: %+v", got)
	}
	if len(h.Reporter.OfKind(ui.CaptureInfo)) == 0 {
		t.Errorf("nothing reported for an empty fleet\n%s", h.Reporter.Dump())
	}
}

func TestPreviewList_AuthFailureNamesTheFix(t *testing.T) {
	h := testharness.New(t)
	fake := h.InstallPreview()
	fake.ListErr = preview.ErrAuth

	err := h.Run("preview", "list")
	if err == nil {
		t.Fatal("preview list succeeded despite an auth failure")
	}
	if !errors.Is(err, preview.ErrAuth) {
		t.Errorf("err = %v, want it to wrap preview.ErrAuth", err)
	}
	// An expired token is not something to retry past — the user has to
	// act, so the message says what to run.
	if !strings.Contains(err.Error(), "gh auth login") {
		t.Errorf("err = %v, want it to name the remedy", err)
	}
}

// TestPreviewList_ProviderCannotList pins the degraded path. An adapter
// whose only view of an env is an HTTP probe against a known URL has no
// way to enumerate; printing an empty list there would read as "the
// fleet is empty", which is a different and wrong answer.
func TestPreviewList_ProviderCannotList(t *testing.T) {
	h := testharness.New(t)
	installNonLister(t, h)

	if err := h.Run("preview", "list"); err != nil {
		t.Fatalf("preview list: %v", err)
	}
	if got := h.Reporter.OfKind(ui.CaptureDetails); len(got) != 0 {
		t.Errorf("rendered a listing from a provider that cannot list: %+v", got)
	}
	skips := h.Reporter.OfKind(ui.CaptureSkip)
	if len(skips) == 0 {
		t.Fatalf("no skip reported; the gap is invisible\n%s", h.Reporter.Dump())
	}
	if !strings.Contains(strings.ToLower(skips[0].Label), "cannot list") {
		t.Errorf("skip = %q, want it to say the provider cannot list", skips[0].Label)
	}
}

// TestPreviewList_UnbuildableProvider pins the construction failure. A
// misconfigured provider — the HTTP adapter with no base URL, say — must
// stop the command and name the reason rather than reporting an empty
// fleet.
func TestPreviewList_UnbuildableProvider(t *testing.T) {
	h := testharness.New(t)
	prev := cli.GetServices()
	next := *prev
	next.PreviewProvider = func(string) (preview.Provider, error) {
		return nil, errors.New("api.base_url not configured")
	}
	cli.SetServices(&next)
	t.Cleanup(func() { cli.SetServices(prev) })

	err := h.Run("preview", "list")
	if err == nil {
		t.Fatal("preview list succeeded with an unbuildable provider")
	}
	if !strings.Contains(err.Error(), "api.base_url") {
		t.Errorf("err = %v, want it to name the missing config", err)
	}
}

// installNonLister swaps in a provider that implements Provider but not
// Lister — the shape of the workflow-dispatch adapter.
func installNonLister(t *testing.T, h *testharness.Harness) {
	t.Helper()
	prev := cli.GetServices()
	next := *prev
	next.PreviewProvider = func(string) (preview.Provider, error) { return nonLister{}, nil }
	cli.SetServices(&next)
	t.Cleanup(func() { cli.SetServices(prev) })
}

type nonLister struct{ preview.Provider }

func slicesContains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
