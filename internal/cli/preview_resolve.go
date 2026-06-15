package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/nickawilliams/bosun/internal/preview"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/spf13/cobra"
)

// previewResolution captures the outcome of name + existence resolution for
// the preview command. The action plan is built from these fields:
// deployName/teardownName drive workflow triggers; isAdopt and isCurrent
// flip the deploy path off in favor of a no-op plan item.
type previewResolution struct {
	previewName  string // final name (for card + metadata)
	previewURL   string // for notifications (may be empty)
	deployName   string // "" = skip deploy
	teardownName string // "" = skip teardown

	// isAdopt is true when we're claiming an env that wasn't previously
	// tracked (rows 2/5 where the user accepts the adopt prompt). The
	// adopt action writes new metadata.
	isAdopt bool

	// isCurrent is true when stored metadata already pointed at an env
	// that probed alive (rows 3/4). The metadata is already correct;
	// nothing to do but render a no-op informational line.
	isCurrent bool

	// isRedeploy is true when the deploy is targeting an env we know is
	// alive (forced redeploy or no url_template fallback). Drives the
	// PlanModify op for the deploy action.
	isRedeploy bool
}

// adoptChoice represents the user's decision when an env conflict is detected.
type adoptChoice int

const (
	adoptExisting adoptChoice = iota
	chooseAnother
	cancelAdopt
)

// promptAdopt asks the user how to handle an existing environment. Returns
// the user's choice and (for chooseAnother) the new name they entered.
// Non-interactive callers get an error directing them to --force.
func promptAdopt(name string) (adoptChoice, string, error) {
	if !isInteractive() {
		return cancelAdopt, "", fmt.Errorf("environment %q already exists; pass --force to redeploy or run interactively", name)
	}

	// Keep the stable "Preview" card title and carry the conflict notice
	// as a muted body sentence, with the env name in keyword style to
	// match how repo names are referenced elsewhere. The trailing blank
	// line gives the options breathing room beneath the prose; the select
	// renders titleless under that.
	muted := lipgloss.NewStyle().Foreground(ui.Palette.Muted)
	notice := muted.Render("Environment ") + ui.Keyword(name) + muted.Render(" already exists.")
	slot := ui.NewSlot()
	slot.Show(ui.NewCard(ui.CardInput, "preview").
		Raw(notice, "").
		AccentBody().
		Tight())

	// Binary Adopt / New Name buttons, defaulting to New Name so an
	// accidental Enter picks a fresh name rather than silently adopting
	// (and skipping the deploy). Ctrl+c (shown in the help footer)
	// cancels. A dedicated 3+-button dialog primitive would let Cancel be
	// its own button — left as future work.
	adopt := false
	if err := runForm(
		newConfirm().
			Affirmative("Adopt").
			Negative("New Name").
			Value(&adopt),
	); err != nil {
		// Leave the question on screen as context for the caller's
		// cancellation card; pad so it lays out against huh's help row.
		ui.RequestSpacer()
		return cancelAdopt, "", err
	}
	slot.Clear()

	if !adopt {
		newName, err := promptDefault("preview", generateEphemeralName())
		if err != nil {
			return cancelAdopt, "", err
		}
		return chooseAnother, strings.TrimSpace(newName), nil
	}
	return adoptExisting, "", nil
}

// resolvePreview implements the --name × stored-metadata resolution matrix.
// Probes both names via the provider, applies --force fallback semantics on
// indeterminate probes, prompts for conflicts, and returns a
// previewResolution describing what should happen in the action plan.
func resolvePreview(cmd *cobra.Command, ctx context.Context, provider preview.Provider, issueKey, stage string, force bool) (previewResolution, error) {
	flagName, _ := cmd.Flags().GetString("name")
	flagName = strings.TrimSpace(flagName)

	// Validate flag if provided. Loop on invalid names in interactive mode.
	if flagName != "" {
		var err error
		flagName, err = enforceValidName(flagName)
		if err != nil {
			return previewResolution{}, err
		}
	}

	// Run resolution work inside one spinner so the user always gets
	// feedback during the HTTP probes (up to ~6s combined). Probes run
	// sequentially; force-fallback notices print after the spinner
	// closes to avoid interleaving with the spinner's TUI output. The
	// spinner shows even when both names are empty (Row 1) — the work is
	// near-instant in that case but the initial card frame keeps
	// feedback consistent across all paths.
	//
	// The card keeps the stable "Preview" title through every state —
	// the transient status text lives on a muted body line, so the
	// title doesn't morph from "Resolving Preview" to "Preview" between
	// the spinner and the final confirmation card. The spinner's final
	// frame morphs into the "? Preview" prompt header in the SAME
	// BubbleTea program — the screen never goes blank while huh's form
	// program boots beneath it. Paths that don't prompt rewind the
	// header immediately and print their own next card.
	var (
		metaEnv, flagEnv           preview.Environment
		metaForceURL, flagForceURL string
	)
	spinCard := ui.NewCard(ui.CardRunning, "preview").Muted("Resolving environment...")
	headerCard := ui.NewCard(ui.CardInput, "preview").Tight()
	rewind, probeErr := ui.RunCardMorph(spinCard, headerCard, func() error {
		env, err := provider.Get(ctx, issueKey)
		if err != nil {
			switch {
			case errors.Is(err, preview.ErrNoEnvironment):
				// fall through with empty metaEnv
			case force && isProbeError(err):
				metaForceURL = probeURL(err)
			default:
				return err
			}
		} else {
			metaEnv = env
		}

		if flagName != "" {
			env, err := provider.Inspect(ctx, flagName)
			if err != nil {
				switch {
				case errors.Is(err, preview.ErrNoEnvironment):
					// fall through with empty flagEnv
				case force && isProbeError(err):
					flagForceURL = probeURL(err)
				default:
					return err
				}
			} else {
				flagEnv = env
			}
		}
		return nil
	})
	if probeErr != nil {
		return previewResolution{}, probeErr
	}

	metaName := metaEnv.Name
	metaAlive := metaEnv.Probed && metaEnv.Alive
	metaUnprobable := metaName != "" && !metaEnv.Probed
	flagAlive := flagEnv.Probed && flagEnv.Alive

	// The morphed "? Preview" header stays on screen only for the
	// Row-1 interactive prompt, where huh's form renders directly
	// beneath it (the morph exists so that boundary never shows a
	// blank frame). Every other path rewinds it now and prints its
	// own next card; the rare Row-1-with-force-notice path also
	// rewinds so the notices don't render below a prompt header.
	row1Prompt := flagName == "" && metaName == "" && isInteractive() &&
		metaForceURL == "" && flagForceURL == ""
	if !row1Prompt {
		rewind()
	}

	if metaForceURL != "" {
		ui.Skip(fmt.Sprintf("couldn't verify %s, proceeding (--force)", metaForceURL))
	}
	if flagForceURL != "" {
		ui.Skip(fmt.Sprintf("couldn't verify %s, proceeding (--force)", flagForceURL))
	}

	res := previewResolution{}

	switch {
	case flagName == "" && metaName == "":
		// Row 1: unset / unset — generate and prompt (interactive
		// only). Matches `bosun start`'s slug prompt: the generated
		// name shows as the placeholder, empty submit accepts it,
		// typed input goes through enforceValidName (which loops on
		// invalid input). Non-interactive silently uses the generated
		// name.
		generated := generateEphemeralName()
		name := generated
		switch {
		case row1Prompt:
			// Header already on screen from the morph — run the form
			// directly beneath it. ClearSpacer stands in for the
			// Tight()-on-Print suppression the morphed card never got.
			input, field := newDefaultInput(generated)
			ui.ClearSpacer()
			if err := runForm(input); err != nil {
				return previewResolution{}, err
			}
			rewind()
			validated, verr := enforceValidName(strings.TrimSpace(field.Resolved()))
			if verr != nil {
				return previewResolution{}, verr
			}
			name = validated
		case isInteractive():
			// Force-notice variant: header was rewound so the notices
			// could print; fall back to the self-contained prompt.
			resolved, perr := promptDefault("preview", name)
			if perr != nil {
				return previewResolution{}, perr
			}
			resolved = strings.TrimSpace(resolved)
			if resolved == "" {
				resolved = name
			}
			validated, verr := enforceValidName(resolved)
			if verr != nil {
				return previewResolution{}, verr
			}
			name = validated
		}

		// A user-entered name was never probed (only flagName gets the
		// Inspect during the resolution spinner), so an existing live
		// env entered here would silently plan as a fresh create.
		// Route it back through the matrix as if it had been passed
		// via --name — Row 2 probes it and offers the adopt prompt on
		// a conflict. Same mechanism handleConflict's "choose another
		// name" uses. The untouched generated name skips this: it was
		// just minted, randomly, and an extra probe round-trip per run
		// would cost more than the collision risk justifies.
		if name != generated {
			if err := cmd.Flags().Set("name", name); err != nil {
				return previewResolution{}, err
			}
			return resolvePreview(cmd, ctx, provider, issueKey, stage, force)
		}

		res.previewName = name
		res.deployName = name

	case flagName != "" && metaName == "":
		// Row 2: set / unset.
		res.previewName = flagName
		if flagAlive {
			if force {
				res.deployName = flagName
				res.isRedeploy = true
			} else {
				return handleConflict(cmd, ctx, provider, issueKey, stage, force, flagName)
			}
		} else {
			res.deployName = flagName
		}

	case flagName == "" && metaName != "":
		// Row 3: unset / set.
		res.previewName = metaName
		switch {
		case metaAlive:
			if force {
				res.deployName = metaName
				res.isRedeploy = true
			} else {
				// Metadata already pointed here and the env is alive —
				// nothing to claim, just verify and render a no-op line.
				res.isCurrent = true
			}
		case metaUnprobable:
			// No url_template — preserve today's behavior (redeploy with
			// stored name, treat as modify).
			res.deployName = metaName
			res.isRedeploy = true
		default:
			// Probed and dead — the env was torn down externally
			// (cleanup job, manual delete). Metadata still points at
			// the name, so recreate under it. PlanCreate, not
			// redeploy: there's nothing alive to modify.
			res.deployName = metaName
		}

	case flagName != "" && metaName != "" && flagName == metaName:
		// Row 4: set / set / same.
		res.previewName = flagName
		switch {
		case metaAlive:
			if force {
				res.deployName = flagName
				res.isRedeploy = true
			} else {
				// Same name as metadata, alive — already current.
				res.isCurrent = true
			}
		case metaUnprobable:
			res.deployName = flagName
			res.isRedeploy = true
		default:
			// Probed and dead — same recreate semantics as Row 3.
			res.deployName = flagName
		}

	case flagName != "" && metaName != "" && flagName != metaName:
		// Row 5: set / set / different.
		res.previewName = flagName
		if metaAlive {
			res.teardownName = metaName
		}
		if flagAlive {
			if force {
				res.deployName = flagName
				res.isRedeploy = true
			} else {
				conflict, err := handleConflict(cmd, ctx, provider, issueKey, stage, force, flagName)
				if err != nil {
					return previewResolution{}, err
				}
				// Preserve the teardown decision from the parent branch —
				// the conflict resolution may have generated a new name
				// but the stale-metadata teardown still applies.
				if res.teardownName != "" {
					conflict.teardownName = res.teardownName
				}
				return conflict, nil
			}
		} else {
			res.deployName = flagName
		}
	}

	res.previewURL = renderStageURL(stage, res.previewName)
	return res, nil
}

// enforceValidName loops until the user provides a valid name (interactive)
// or returns the validation error (non-interactive). Empty input from the
// prompt cancels.
func enforceValidName(name string) (string, error) {
	for {
		if err := preview.ValidateName(name); err == nil {
			return name, nil
		} else {
			ui.Fail(err.Error())
			if !isInteractive() {
				return "", err
			}
			next, perr := promptDefault("preview", generateEphemeralName())
			if perr != nil {
				return "", perr
			}
			name = strings.TrimSpace(next)
			if name == "" {
				return "", ErrCancelled
			}
		}
	}
}

// handleConflict runs the adopt prompt for an existing-env conflict on the
// given name. On chooseAnother, recurses through resolvePreview with the new
// name set on the cobra command. Returns ErrCancelled if the user cancels.
func handleConflict(cmd *cobra.Command, ctx context.Context, provider preview.Provider, issueKey, stage string, force bool, name string) (previewResolution, error) {
	choice, newName, err := promptAdopt(name)
	if err != nil {
		return previewResolution{}, err
	}
	switch choice {
	case adoptExisting:
		return previewResolution{
			previewName: name,
			previewURL:  renderStageURL(stage, name),
			isAdopt:     true,
		}, nil
	case chooseAnother:
		if err := cmd.Flags().Set("name", newName); err != nil {
			return previewResolution{}, err
		}
		return resolvePreview(cmd, ctx, provider, issueKey, stage, force)
	}
	return previewResolution{}, ErrCancelled
}

// isProbeError reports whether err wraps a preview.ProbeError.
func isProbeError(err error) bool {
	var pe *preview.ProbeError
	return errors.As(err, &pe)
}

// probeURL extracts the URL from a preview.ProbeError, or returns empty
// if err is not a ProbeError.
func probeURL(err error) string {
	var pe *preview.ProbeError
	if errors.As(err, &pe) {
		return pe.URL
	}
	return ""
}
