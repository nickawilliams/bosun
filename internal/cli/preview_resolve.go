package cli

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"charm.land/huh/v2"
	"github.com/nickawilliams/bosun/internal/issue"
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

// previewNameRe approximates k8s subdomain rules: lowercase letter start,
// lowercase alphanumerics or hyphens, alphanumeric end, max 63 chars.
var previewNameRe = regexp.MustCompile(`^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$`)

func validatePreviewName(name string) error {
	if name == "" {
		return errors.New("name is empty")
	}
	if !previewNameRe.MatchString(name) {
		return fmt.Errorf("invalid name %q: must be lowercase letters, digits, and hyphens; start with a letter, end alphanumeric, max 63 chars", name)
	}
	return nil
}

// fetchExistingPreviewName reads the preview_name property from the issue
// tracker. Returns "" for any non-success path — a missing property, nil
// tracker, or unexpected JSON shape are all "no stored name."
func fetchExistingPreviewName(ctx context.Context, tracker issue.Tracker, issueKey string) string {
	if tracker == nil {
		return ""
	}
	raw, err := tracker.GetProperty(ctx, issueKey)
	if err != nil || raw == nil {
		return ""
	}
	var props struct {
		PreviewName string `json:"preview_name"`
	}
	if err := json.Unmarshal(raw, &props); err != nil {
		return ""
	}
	return props.PreviewName
}

// probeOutcome captures the result of an environment existence check.
// probeUnknown means we couldn't probe at all (no url_template configured)
// — callers should fall back to "trust stored name" behavior.
type probeOutcome int

const (
	probeUnknown probeOutcome = iota
	probeAlive
	probeDead
)

// classifyProbeStatus maps an HTTP status code to (alive, definitive). 5xx is
// indefinite (caller should retry); 404 is a definitive miss; anything else
// 2xx-4xx is alive (auth-gated envs return 401/403 and we treat that as a
// signal that the host is reachable).
func classifyProbeStatus(status int) (alive, definitive bool) {
	switch {
	case status == http.StatusNotFound:
		return false, true
	case status >= 500 && status < 600:
		return false, false
	case status >= 200 && status < 500:
		return true, true
	default:
		return false, false
	}
}

// httpProbe sends a HEAD (falling back to GET on 405) and classifies the
// response. One retry on transient errors; returns an error after retries
// are exhausted.
func httpProbe(ctx context.Context, url string) (bool, error) {
	client := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	do := func(method string) (int, error) {
		rc, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(rc, method, url, nil)
		if err != nil {
			return 0, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return 0, err
		}
		resp.Body.Close()
		return resp.StatusCode, nil
	}

	attempt := func() (alive, definitive bool, err error) {
		status, err := do(http.MethodHead)
		if err != nil {
			return false, false, err
		}
		if status == http.StatusMethodNotAllowed {
			status, err = do(http.MethodGet)
			if err != nil {
				return false, false, err
			}
		}
		alive, definitive = classifyProbeStatus(status)
		return alive, definitive, nil
	}

	var lastErr error
	for range 2 {
		alive, definitive, err := attempt()
		if err == nil && definitive {
			return alive, nil
		}
		if err != nil {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = errors.New("indeterminate response after retry")
	}
	return false, lastErr
}

// probePreviewName probes the URL for the given name. Returns probeUnknown
// when name is empty or the stage has no url_template configured (no probe
// possible). Honors --force on probe failure: prints a notice and returns
// probeDead so the caller proceeds as if the env doesn't exist.
func probePreviewName(ctx context.Context, stage, name string, force bool) (probeOutcome, error) {
	if name == "" {
		return probeUnknown, nil
	}
	url := renderStageURL(stage, name)
	if url == "" {
		return probeUnknown, nil
	}
	alive, err := httpProbe(ctx, url)
	if err != nil {
		if force {
			ui.Skip(fmt.Sprintf("couldn't verify %s, proceeding (--force)", url))
			return probeDead, nil
		}
		return 0, fmt.Errorf("verifying %s: %w", url, err)
	}
	if alive {
		return probeAlive, nil
	}
	return probeDead, nil
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

	choice := adoptExisting
	err := runForm(
		huh.NewSelect[adoptChoice]().
			Title(fmt.Sprintf("environment %q already exists", name)).
			Options(
				huh.NewOption("adopt existing (skip deploy)", adoptExisting),
				huh.NewOption("choose another name", chooseAnother),
				huh.NewOption("cancel", cancelAdopt),
			).
			Value(&choice),
	)
	if err != nil {
		return cancelAdopt, "", err
	}

	if choice == chooseAnother {
		newName, err := promptDefault("preview name", generateEphemeralName())
		if err != nil {
			return cancelAdopt, "", err
		}
		return chooseAnother, strings.TrimSpace(newName), nil
	}
	return choice, "", nil
}

// resolvePreview implements the --name × stored-metadata resolution matrix.
// Performs HTTP probes, immediately deletes stale metadata, prompts for
// conflicts, and returns a previewResolution describing what should happen
// in the action plan.
func resolvePreview(cmd *cobra.Command, ctx context.Context, tracker issue.Tracker, issueKey, stage string, force bool) (previewResolution, error) {
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

	metaName := strings.TrimSpace(fetchExistingPreviewName(ctx, tracker, issueKey))

	metaProbe, err := probePreviewName(ctx, stage, metaName, force)
	if err != nil {
		return previewResolution{}, err
	}

	// Stale metadata cleanup happens immediately during resolution. Trade-
	// off documented in the plan: a false-negative probe wipes the name
	// pointer, but recovery is trivial (re-run with --name + adopt) and
	// the env itself keeps running.
	if metaProbe == probeDead && metaName != "" && tracker != nil {
		if derr := tracker.DeleteProperty(ctx, issueKey); derr != nil {
			ui.Fail(fmt.Sprintf("couldn't clear stale metadata: %v", derr))
		} else {
			ui.Complete(fmt.Sprintf("cleared stale metadata: %s", metaName))
		}
		metaName = ""
		metaProbe = probeUnknown
	}

	flagProbe, err := probePreviewName(ctx, stage, flagName, force)
	if err != nil {
		return previewResolution{}, err
	}

	res := previewResolution{}

	switch {
	case flagName == "" && metaName == "":
		// Row 1: unset / unset — generate, optionally prompt.
		name := generateEphemeralName()
		if forceInteractive(cmd) {
			resolved, perr := promptDefault("preview name", name)
			if perr != nil {
				return previewResolution{}, perr
			}
			name = strings.TrimSpace(resolved)
		}
		res.previewName = name
		res.deployName = name

	case flagName != "" && metaName == "":
		// Row 2: set / unset.
		res.previewName = flagName
		if flagProbe == probeAlive {
			if force {
				res.deployName = flagName
				res.isRedeploy = true
			} else {
				return handleConflict(cmd, ctx, tracker, issueKey, stage, force, flagName)
			}
		} else {
			res.deployName = flagName
		}

	case flagName == "" && metaName != "":
		// Row 3: unset / set.
		res.previewName = metaName
		switch metaProbe {
		case probeAlive:
			if force {
				res.deployName = metaName
				res.isRedeploy = true
			} else {
				// Metadata already pointed here and the env is alive —
				// nothing to claim, just verify and render a no-op line.
				res.isCurrent = true
			}
		case probeUnknown:
			// No url_template — preserve today's behavior (redeploy with
			// stored name, treat as modify).
			res.deployName = metaName
			res.isRedeploy = true
		}

	case flagName != "" && metaName != "" && flagName == metaName:
		// Row 4: set / set / same.
		res.previewName = flagName
		switch metaProbe {
		case probeAlive:
			if force {
				res.deployName = flagName
				res.isRedeploy = true
			} else {
				// Same name as metadata, alive — already current.
				res.isCurrent = true
			}
		case probeUnknown:
			res.deployName = flagName
			res.isRedeploy = true
		}

	case flagName != "" && metaName != "" && flagName != metaName:
		// Row 5: set / set / different.
		res.previewName = flagName
		if metaProbe == probeAlive {
			res.teardownName = metaName
		}
		if flagProbe == probeAlive {
			if force {
				res.deployName = flagName
				res.isRedeploy = true
			} else {
				conflict, err := handleConflict(cmd, ctx, tracker, issueKey, stage, force, flagName)
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
		if err := validatePreviewName(name); err == nil {
			return name, nil
		} else {
			ui.Fail(err.Error())
			if !isInteractive() {
				return "", err
			}
			next, perr := promptDefault("preview name", generateEphemeralName())
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
func handleConflict(cmd *cobra.Command, ctx context.Context, tracker issue.Tracker, issueKey, stage string, force bool, name string) (previewResolution, error) {
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
		return resolvePreview(cmd, ctx, tracker, issueKey, stage, force)
	}
	return previewResolution{}, ErrCancelled
}
