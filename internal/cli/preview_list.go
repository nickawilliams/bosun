package cli

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/nickawilliams/bosun/internal/preview"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
)

// newPreviewListCmd builds `bosun preview list`.
//
// The fleet is shared, so this lists everyone's environments, not the
// caller's — that is the point. Adoption previously required knowing an
// env's name from somewhere outside bosun; this is where you find it.
//
// Not every provider can answer. An adapter whose only view of an env is
// an HTTP probe against a known URL has no way to enumerate, so the
// command reports that rather than printing an empty list, which would
// read as "the fleet is empty".
func newPreviewListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List preview environments",
		Annotations: map[string]string{
			headerAnnotationTitle: "preview environments",
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()

			// No workspace scope: listing is fleet-wide, and the
			// workspace only ever fed the workflow-target resolution
			// that this path never reaches.
			provider, err := newPreviewProvider("")
			if err != nil {
				return fmt.Errorf("preview provider: %w", err)
			}

			lister, ok := provider.(preview.Lister)
			if !ok {
				ui.Skip("preview: the configured provider cannot list environments")
				return nil
			}

			mine, _ := cmd.Flags().GetString("user")

			var envs []preview.Environment
			if err := ui.RunCard("Fetching Environments", func() error {
				got, err := lister.List(ctx)
				envs = got
				return err
			}); err != nil {
				if errors.Is(err, preview.ErrAuth) {
					return fmt.Errorf("%w (run `gh auth login`)", err)
				}
				return err
			}

			envs = filterPreviewEnvs(envs, mine)
			if len(envs) == 0 {
				ui.Info("No preview environments found.")
				return nil
			}

			ui.Details("Environments", previewListFields(envs))
			return nil
		},
	}

	// Project scope only — no workspace or issue flag. Listing is
	// fleet-wide, so scoping it to one issue's environment would answer
	// a question `bosun status` already answers.
	addProjectFlag(cmd)
	cmd.Flags().String("user", "", "only environments deployed by this account")
	return cmd
}

// filterPreviewEnvs narrows envs to those deployed by user (case
// insensitive) and sorts by name so repeated runs render identically.
// An empty user keeps everything.
//
// Filtering is client-side because the API has no user parameter — the
// web UI narrows the same full listing the same way.
func filterPreviewEnvs(envs []preview.Environment, user string) []preview.Environment {
	out := make([]preview.Environment, 0, len(envs))
	for _, env := range envs {
		if user != "" && !strings.EqualFold(env.DeployedBy, user) {
			continue
		}
		out = append(out, env)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// previewListFields renders one key-value row per environment: the name
// against its state and owner.
func previewListFields(envs []preview.Environment) ui.Fields {
	pairs := make([]string, 0, len(envs)*2)
	for _, env := range envs {
		pairs = append(pairs, env.Name, previewListValue(env))
	}
	return ui.NewFields(pairs...)
}

// previewListValue renders one environment's state and owner. Status
// leads because it is what the reader is scanning for; the owner
// follows because a shared fleet needs to say whose an env is.
func previewListValue(env preview.Environment) string {
	parts := []string{previewStatusLabel(env)}
	if env.DeployedBy != "" {
		parts = append(parts, env.DeployedBy)
	}
	return strings.Join(parts, " · ")
}

// previewStatusLabel renders a status as a word for the list. It stays
// separate from previewStateNote (the status row's parenthetical
// qualifier) because the two answer different questions: there, the name
// is the answer and the status qualifies it; here, the status is a column.
func previewStatusLabel(env preview.Environment) string {
	if !env.Probed {
		return "unknown"
	}
	switch env.Status {
	case preview.StatusCreating:
		return "deploying"
	case preview.StatusActive:
		return "active"
	case preview.StatusDegraded:
		if n := len(env.FailedServices); n > 0 {
			return fmt.Sprintf("degraded (%s)", strings.Join(env.FailedServices, ", "))
		}
		return "degraded"
	case preview.StatusDeleting:
		return "tearing down"
	case preview.StatusGone:
		return "gone"
	default:
		return "unknown"
	}
}
