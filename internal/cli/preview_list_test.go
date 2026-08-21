package cli

import (
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/preview"
)

func TestFilterPreviewEnvs(t *testing.T) {
	fleet := []preview.Environment{
		{Name: "wobbly-turtle", DeployedBy: "dana"},
		{Name: "brave-falcon", DeployedBy: "Nick"},
		{Name: "clever-fox", DeployedBy: "nick"},
	}

	t.Run("unfiltered, sorted by name", func(t *testing.T) {
		// Sorted so two runs against the same fleet render identically;
		// the API orders by creation time, which shuffles as envs come
		// and go.
		got := filterPreviewEnvs(fleet, "")
		want := []string{"brave-falcon", "clever-fox", "wobbly-turtle"}
		if len(got) != len(want) {
			t.Fatalf("got %d envs, want %d", len(got), len(want))
		}
		for i := range want {
			if got[i].Name != want[i] {
				t.Errorf("envs[%d] = %q, want %q", i, got[i].Name, want[i])
			}
		}
	})

	t.Run("filtered case-insensitively", func(t *testing.T) {
		// GitHub logins are case-insensitive, so "Nick" and "nick" are
		// the same person and both are theirs.
		got := filterPreviewEnvs(fleet, "NICK")
		if len(got) != 2 {
			t.Fatalf("got %d envs, want 2: %+v", len(got), got)
		}
	})

	t.Run("filter matching nobody", func(t *testing.T) {
		if got := filterPreviewEnvs(fleet, "nobody"); len(got) != 0 {
			t.Errorf("got %d envs, want 0", len(got))
		}
	})

	t.Run("empty fleet", func(t *testing.T) {
		if got := filterPreviewEnvs(nil, ""); len(got) != 0 {
			t.Errorf("got %d envs, want 0", len(got))
		}
	})
}

func TestPreviewStatusLabel(t *testing.T) {
	cases := []struct {
		name string
		env  preview.Environment
		want string
	}{
		{"active", preview.Environment{Probed: true, Status: preview.StatusActive}, "active"},
		{"creating", preview.Environment{Probed: true, Status: preview.StatusCreating}, "deploying"},
		{"deleting", preview.Environment{Probed: true, Status: preview.StatusDeleting}, "tearing down"},
		{"gone", preview.Environment{Probed: true, Status: preview.StatusGone}, "gone"},
		{
			"degraded names the casualties",
			preview.Environment{Probed: true, Status: preview.StatusDegraded, FailedServices: []string{"api", "worker"}},
			"degraded (api, worker)",
		},
		{"degraded without detail", preview.Environment{Probed: true, Status: preview.StatusDegraded}, "degraded"},
		// An unprobed env is one the provider couldn't verify. Rendering
		// any state word would claim a check that never ran.
		{"unprobed", preview.Environment{Status: preview.StatusActive}, "unknown"},
		{"unrecognized status", preview.Environment{Probed: true, Status: preview.Status("teleporting")}, "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := previewStatusLabel(tc.env); got != tc.want {
				t.Errorf("previewStatusLabel = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPreviewListValue(t *testing.T) {
	env := preview.Environment{Probed: true, Status: preview.StatusActive, DeployedBy: "dana"}
	if got, want := previewListValue(env), "active · dana"; got != want {
		t.Errorf("previewListValue = %q, want %q", got, want)
	}

	// A fleet-wide listing has to say whose an env is, but the API can
	// report an unknown actor and a separator with nothing after it
	// reads as a rendering bug.
	env.DeployedBy = ""
	if got, want := previewListValue(env), "active"; got != want {
		t.Errorf("previewListValue with no owner = %q, want %q", got, want)
	}
}

func TestPreviewListFields(t *testing.T) {
	fields := previewListFields([]preview.Environment{
		{Name: "brave-falcon", Probed: true, Status: preview.StatusActive, DeployedBy: "dana"},
		{Name: "wobbly-turtle", Probed: true, Status: preview.StatusCreating},
	})

	if len(fields) != 2 {
		t.Fatalf("got %d fields, want 2", len(fields))
	}
	if fields[0].Key != "brave-falcon" || !strings.Contains(fields[0].Value, "active") {
		t.Errorf("fields[0] = %+v", fields[0])
	}
	if fields[1].Key != "wobbly-turtle" || !strings.Contains(fields[1].Value, "deploying") {
		t.Errorf("fields[1] = %+v", fields[1])
	}
}
