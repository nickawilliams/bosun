package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"text/template"

	"github.com/nickawilliams/bosun/internal/code"
	"github.com/nickawilliams/bosun/internal/config"
	"github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/notify"
	"github.com/nickawilliams/bosun/internal/preview"
	"github.com/nickawilliams/bosun/internal/services"
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/nickawilliams/bosun/internal/vcs/git"
	"github.com/nickawilliams/bosun/internal/workspace"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

// Repository represents a resolved repository with a short name and absolute path.
type Repository struct {
	Name string // Directory basename, used for worktree directory names.
	Path string // Absolute path to the repository.
}

// resolveRepositories expands the repositories: globs from config, filters to
// directories containing .git/, and returns the resolved set. If filterNames
// is non-empty, only repositories whose names match are returned.
func resolveRepositories(filterNames []string) ([]Repository, error) {
	patterns := viper.GetStringSlice("workspace.repositories")
	if len(patterns) == 0 {
		return nil, fmt.Errorf("no repository patterns configured: set workspace.repositories in .bosun/config.yaml")
	}

	projectRoot := config.FindProjectRoot()

	var repositories []Repository
	seen := make(map[string]bool)

	for _, pattern := range patterns {
		// Resolve relative patterns against project root.
		if !filepath.IsAbs(pattern) && projectRoot != "" {
			pattern = filepath.Join(projectRoot, pattern)
		}

		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, fmt.Errorf("invalid glob %q: %w", pattern, err)
		}

		for _, match := range matches {
			abs, err := filepath.Abs(match)
			if err != nil {
				continue
			}

			// Must be a directory with .git/.
			info, err := os.Stat(abs)
			if err != nil || !info.IsDir() {
				continue
			}
			if _, err := os.Stat(filepath.Join(abs, ".git")); err != nil {
				continue
			}

			name := filepath.Base(abs)
			if seen[abs] {
				continue
			}
			seen[abs] = true

			repositories = append(repositories, Repository{Name: name, Path: abs})
		}
	}

	if len(filterNames) > 0 {
		filter := make(map[string]bool, len(filterNames))
		for _, n := range filterNames {
			filter[n] = true
		}
		matched := make(map[string]bool, len(filterNames))
		var filtered []Repository
		for _, r := range repositories {
			if filter[r.Name] {
				matched[r.Name] = true
				filtered = append(filtered, r)
			}
		}
		// Every requested name must match — silently dropping a typo'd
		// name while its siblings proceed reads as success for an
		// operation that never happened (`workspace rm api typo`
		// removed api and said nothing about typo).
		var unknown []string
		for _, n := range filterNames {
			if !matched[n] {
				unknown = append(unknown, n)
			}
		}
		if len(unknown) > 0 {
			return nil, fmt.Errorf(
				"no repositories matched %s (available: %s)",
				strings.Join(unknown, ", "), repositoryNames(repositories),
			)
		}
		repositories = filtered
	}

	if len(repositories) == 0 {
		return nil, fmt.Errorf("no repositories found matching configured patterns")
	}

	return repositories, nil
}

// emitWorkspaceIssuePreamble fetches the issue and renders the
// two-card preamble shared by `bosun status` workspace mode and
// every lifecycle command: the Status card (lifecycle stepper)
// followed by the issue card. A single tracker fetch populates both —
// the Status card morphs in via RunCardReplace (its spinner doubles
// as the loading affordance for both cards), and the issue card
// prints once the fetch resolves.
//
// alongside, when non-nil, runs inside the spinner concurrently with
// the issue fetch — status uses it to fold its preview-env and
// last-activity lookups into the same loading phase. Pass nil when
// there is nothing else to fetch.
//
// Tolerates a missing tracker (returns zero detail; nothing rendered)
// and a failed fetch (both cards render their degraded variants:
// Status collapses to "(unavailable)", the issue card keeps the key
// with a muted "(title unavailable)"). The uniform render-and-continue
// posture matters because every lifecycle command behaves the same
// when issue-tracker connectivity is degraded — no command quietly
// aborts while a sibling carries on. Commands that have a hard
// dependency on detail fields can still short-circuit by checking
// `detail.Key == ""`.
// The fetch error is returned alongside the detail (nil when no
// tracker is configured) so commands whose mutations are keyed on the
// issue can branch on issue.ErrNotFound — a definitive "no such key"
// — without giving up the render-and-continue posture for transient
// failures.
func emitWorkspaceIssuePreamble(ctx context.Context, issueKey string, alongside func()) (issue.Issue, error) {
	var detail issue.Issue
	var fetchErr error

	tracker, err := newIssueTracker()
	if err != nil || tracker == nil {
		return detail, nil
	}

	// fn always returns nil so RunCardReplace takes the successCard
	// path on success AND failure — buildWorkspaceStoryCard renders
	// the designed degraded variant instead of the generic failed card.
	_ = ui.RunCardReplace("", func() error {
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			detail, fetchErr = tracker.GetIssue(ctx, issueKey)
		}()
		if alongside != nil {
			alongside()
		}
		wg.Wait()
		return nil
	}, func() *ui.Card {
		return buildWorkspaceStoryCard(detail, issueKey, fetchErr == nil)
	})

	return detail, fetchErr
}

// emitLifecyclePreamble renders the visual preamble shown at the
// start of every lifecycle command (start, review, preview,
// prerelease, release, cleanup) — a thin wrapper over
// emitWorkspaceIssuePreamble with no alongside work, kept as a named
// symbol so call sites read as intent rather than mechanics. Future
// expansion of the preamble lands in the shared helper and every
// command inherits the change.
func emitLifecyclePreamble(ctx context.Context, issueKey string) (issue.Issue, error) {
	return emitWorkspaceIssuePreamble(ctx, issueKey, nil)
}

// resolveActiveRepositories resolves repositories scoped to the given
// workspace. Workspace-required lifecycle commands (review, prerelease)
// use this to stay scoped to the workspace's worktrees rather than
// every configured repository. Callers must ensure workspace is
// non-empty (via cc.RequireWorkspace()); resolution does not fall back.
func resolveActiveRepositories(ctx context.Context, workspace string, filterNames []string) ([]Repository, error) {
	mgr, err := newWorkspaceManager()
	if err != nil {
		return nil, err
	}

	wsName := workspace

	statuses, err := mgr.Status(ctx, wsName)
	if err != nil {
		return nil, fmt.Errorf("workspace %s: %w", wsName, err)
	}

	repositories := make([]Repository, 0, len(statuses))
	for _, s := range statuses {
		repositories = append(repositories, Repository{Name: s.Name, Path: s.Path})
	}

	if len(filterNames) > 0 {
		filter := make(map[string]bool, len(filterNames))
		for _, n := range filterNames {
			filter[n] = true
		}
		var filtered []Repository
		for _, r := range repositories {
			if filter[r.Name] {
				filtered = append(filtered, r)
			}
		}
		if len(filtered) == 0 {
			return nil, fmt.Errorf(
				"no repositories matched filter %v (workspace repos: %s)",
				filterNames, repositoryNames(repositories),
			)
		}
		repositories = filtered
	}

	if len(repositories) == 0 {
		return nil, fmt.Errorf("no repositories found in workspace %s", wsName)
	}

	return repositories, nil
}

// newWorkspaceManager creates a workspace.Manager from current config.
func newWorkspaceManager() (*workspace.Manager, error) {
	projectRoot := config.FindProjectRoot()
	if projectRoot == "" {
		return nil, fmt.Errorf("not inside a bosun project (no .bosun/ directory found)")
	}

	wsRoot := viper.GetString("workspace.root")
	if wsRoot == "" {
		return nil, fmt.Errorf("workspaces not configured (set workspace.root in config)")
	}
	if !filepath.IsAbs(wsRoot) {
		wsRoot = filepath.Join(projectRoot, wsRoot)
	}

	return workspace.NewManager(git.New(), wsRoot), nil
}

// resolveWorkspaceName returns the workspace name from the resolution chain:
// (1) --workspace flag, (2) BOSUN_WORKSPACE env var,
// (3) auto-detected from CWD.
func resolveWorkspaceName(cmd *cobra.Command) (string, error) {
	if cmd != nil {
		if name, _ := cmd.Flags().GetString("workspace"); name != "" {
			return name, nil
		}
	}
	// Read the env var directly rather than through viper — see
	// resolveIssueSilent. A bare `workspace:` key in a config file
	// would otherwise pin every command to one workspace, and it would
	// collide with the `workspace:` block that holds root and
	// repositories.
	if name := os.Getenv("BOSUN_WORKSPACE"); name != "" {
		return name, nil
	}
	return detectWorkspaceFromCWD()
}

// detectWorkspaceFromCWD returns the workspace name implied by the current
// working directory, or an error if CWD is not inside a workspace.
func detectWorkspaceFromCWD() (string, error) {
	projectRoot := config.FindProjectRoot()
	if projectRoot == "" {
		return "", fmt.Errorf("not inside a bosun project (no .bosun/ directory found)")
	}

	wsRoot := viper.GetString("workspace.root")
	if wsRoot == "" {
		return "", fmt.Errorf("workspaces not configured (set workspace.root in config)")
	}
	if !filepath.IsAbs(wsRoot) {
		wsRoot = filepath.Join(projectRoot, wsRoot)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	if name, err := workspace.DetectName(wsRoot, cwd); err == nil {
		return name, nil
	}

	// Fall back to the path relative to workspace root (CWD is the workspace
	// directory itself, not inside a worktree).
	rel, err := filepath.Rel(wsRoot, cwd)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("not inside a workspace under %s", wsRoot)
	}
	return rel, nil
}

// cliRepositoriesToWorkspaceRepositories converts CLI Repository types to workspace Repository types.
func cliRepositoriesToWorkspaceRepositories(repositories []Repository) []workspace.Repository {
	result := make([]workspace.Repository, len(repositories))
	for i, r := range repositories {
		result[i] = workspace.Repository{Name: r.Name, Path: r.Path}
	}
	return result
}

// repositoryNames returns a comma-separated string of repository names.
func repositoryNames(repositories []Repository) string {
	names := make([]string, len(repositories))
	for i, r := range repositories {
		names[i] = r.Name
	}
	return strings.Join(names, ", ")
}

// resolveStatus maps a bosun lifecycle status key (e.g., "in_progress") to
// the provider-specific status name from config (e.g., "In Progress").
// Falls back to schema defaults if not set in config.
func resolveStatus(key string) (string, error) {
	name := viper.GetString("issue_tracker.statuses." + key)
	if name != "" {
		return name, nil
	}

	// Check schema defaults.
	if group, ok := lookupGroup("issue_tracker.statuses"); ok {
		for _, ck := range group.Keys {
			if ck.Key == key && ck.Default != "" {
				return ck.Default, nil
			}
		}
	}

	return "", fmt.Errorf("status %q not mapped in config statuses section", key)
}

// buildStatusIndex returns a mapping from lowercase provider status
// name to lifecycle sequence position. Includes "done" as the
// terminal position after all lifecycleStatusKeys entries. Unknown
// statuses are absent from the map; callers should treat missing
// entries as sorting after "done".
func buildStatusIndex() map[string]int {
	idx := make(map[string]int, len(lifecycleStatusKeys)+1)
	for i, key := range lifecycleStatusKeys {
		name, err := resolveStatus(key)
		if err != nil {
			continue
		}
		idx[strings.ToLower(name)] = i
	}
	if name, err := resolveStatus("done"); err == nil {
		idx[strings.ToLower(name)] = len(lifecycleStatusKeys)
	}
	return idx
}

// lifecycleKeyForStatus reverse-resolves a provider status name
// (e.g., "Ready for Release") to the bosun lifecycle key (e.g.,
// "ready_for_release") it's mapped from. Returns "" if the status
// doesn't match any configured lifecycle stage. Comparison is
// case-insensitive.
func lifecycleKeyForStatus(status string) string {
	if status == "" {
		return ""
	}
	target := strings.ToLower(status)
	for _, key := range append(append([]string{}, lifecycleStatusKeys...), "done") {
		name, err := resolveStatus(key)
		if err != nil {
			continue
		}
		if strings.ToLower(name) == target {
			return key
		}
	}
	return ""
}

// prTemplateData holds the fields available to PR title and body
// templates. Branch and BaseBranch are the PR's own; the issue fields
// come from the shared vocabulary (see template.go).
type prTemplateData struct {
	Issue      issueRef
	Branch     string
	BaseBranch string
}

// executePRTemplate parses and executes a Go text/template with PR data.
func executePRTemplate(name, pattern string, data prTemplateData) (string, error) {
	tmpl, err := template.New(name).Parse(pattern)
	if err != nil {
		return "", err
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// buildPRTitle generates a PR title from the configured pattern and issue metadata.
func buildPRTitle(data prTemplateData) string {
	pattern := viper.GetString("code_host.pr.title_template")
	configured := pattern != ""
	if !configured {
		pattern = "[{{.Issue.Key}}] {{.Issue.Title}}"
	}
	result, err := executePRTemplate("pr-title", pattern, data)
	if err != nil {
		// The fallback still runs — a PR title is worth having even
		// from a broken template — but the substitution is reported
		// rather than made silently.
		if configured {
			reportTemplateFailure("code_host.pr.title_template", pattern, err, nil)
		}
		return fmt.Sprintf("[%s] %s", data.Issue.Key, data.Issue.Title)
	}
	return result
}

// buildPRBody generates a PR body from the configured template and issue
// metadata. Returns empty string if no template is configured.
func buildPRBody(data prTemplateData) string {
	pattern := viper.GetString("code_host.pr.body_template")
	if pattern == "" {
		return ""
	}
	result, err := executePRTemplate("pr-body", pattern, data)
	if err != nil {
		reportTemplateFailure("code_host.pr.body_template", pattern, err, nil)
		return ""
	}
	return result
}

// repoIdentity resolves a repository's host identity, routing through
// the code host when one is configured.
//
// The fallback exists because a clone's identity is knowable without
// credentials, and the lifecycle commands' pre-flight passes resolve
// identities even when the host is unreachable — review and prerelease
// list their repos and explain what they can't do, rather than
// collapsing the whole section. Callers that already know they hold a
// host should call the method directly.
func repoIdentity(ctx context.Context, host code.Host, repositoryPath string) (code.RepositoryIdentity, error) {
	if host != nil {
		return host.ParseRemote(ctx, repositoryPath)
	}
	return code.ParseRemote(ctx, repositoryPath)
}

// cardIconPixels is the source resolution requested for raster card icons
// that accept a pixel size (code-host avatars). Card icons render at a fixed
// display size in Slack, so this governs source crispness, not on-screen
// dimensions. The Slack adapter requests a matching size from tracker icons
// so the two sources sit at the same scale on a card.
const cardIconPixels = 48

// avatarURL builds the avatar image URL for a host login at the shared
// card-icon size. Returns "" for a nil host or empty login, which the
// card renderers already treat as "fall back to a glyph".
func avatarURL(host code.Host, login string) string {
	if host == nil || login == "" {
		return ""
	}
	return host.AvatarURL(login, cardIconPixels)
}

// branchURL builds the web link for a repository branch, or "" when no
// host is configured — notification items render the label without a
// branch link in that case rather than failing the send.
func branchURL(host code.Host, owner, repository, branch string) string {
	if host == nil {
		return ""
	}
	return host.BranchURL(code.RepositoryIdentity{Owner: owner, Name: repository}, branch)
}

// notifyTemplateData holds the fields available to notification
// templates. Items and IconURL are the notification's own; Issue and
// Preview come from the shared vocabulary (see template.go).
//
// The namespacing does useful work here beyond consistency: the
// author avatar and the issue-type icon used to sit side by side as
// IconURL and IssueIconURL, one letter of prefix apart. They are now
// IconURL and Issue.IconURL, which reads as the different subjects
// they are.
type notifyTemplateData struct {
	Issue   issueRef
	Preview preview.Ref

	IconURL string        // Avatar or icon URL for card blocks.
	Items   []notify.Item // Per-repository items (PRs, releases, etc.).
}

// Default structured templates per notification type. Used when the type
// falls through the text-default path below and no map config is set.
// Preview shares the review type (it augments the review notification in
// place), so it needs no entry of its own.
var defaultNotifyTemplates = map[string]map[string]string{
	"review": {
		"header":  "Ready for Review",
		"context": "via bosun",
	},
}

// Default text templates per notification type. Routes a type through the
// plain-text Content.Text path (no structured data) when no map config is
// set — provider-agnostic content rather than a Slack card. Prerelease
// matches the #release_coordination convention: one block per item with
// the host-generated notes (CreateReleaseRequest.GenerateNotes) inline.
// Templates emit standard Markdown; provider adapters render it to their
// native format (the Slack adapter posts it as a markdown block so
// headings, bullets, links, and tables all render natively).
var defaultTextNotifyTemplates = map[string]string{
	"prerelease": "{{range $i, $item := .Items}}{{if $i}}\n\n{{end}}going out `{{$item.Label}}`: {{$item.URL}}\n{{$item.Body}}{{end}}",
}

// buildNotifyContent reads the template config for a notification type and
// renders it into a provider-agnostic notify.Content. Resolution order:
//
//  1. Config as a string: notification.templates.<type>: "…" → Content.Text
//  2. Built-in text default (defaultTextNotifyTemplates) → Content.Text
//  3. Config as a map of override fields, or built-in defaults → structured
//     Content (rendered header/body/context + issue/items/preview data)
//
// The text path emits a flat body; the structured path carries data the
// provider adapter renders into its own presentation (the Slack adapter
// builds cards). This function owns content and config only — no cards,
// button labels, or glyphs live here.
func buildNotifyContent(notifType string, data notifyTemplateData) notify.Content {
	key := "notification.templates." + notifType

	// Check if it's a simple string template.
	if s := viper.GetString(key); s != "" {
		return notify.Content{Text: renderNotifyTemplate(key, s, data)}
	}

	// Built-in text default — overridden by a map config for this type.
	// The empty key is what stops a failing built-in from being
	// reported against a config key the user never set.
	if s, ok := defaultTextNotifyTemplates[notifType]; ok {
		if sub := viper.GetStringMapString(key); len(sub) == 0 {
			return notify.Content{Text: renderNotifyTemplate("", s, data)}
		}
	}

	// Structured path: a map of override fields, falling back to defaults.
	sub := viper.GetStringMapString(key)
	defaults := defaultNotifyTemplates[notifType]
	// get returns the pattern and the key to blame for it — the user's
	// when they supplied an override, none when the built-in default
	// is what rendered.
	get := func(field string) (string, string) {
		if v, ok := sub[field]; ok {
			return v, key + "." + field
		}
		return defaults[field], ""
	}
	render := func(field string) string {
		pattern, blame := get(field)
		return renderNotifyTemplate(blame, pattern, data)
	}

	c := notify.Content{
		Header:  render("header"),
		Body:    render("body"),
		Context: render("context"),
		Items:   data.Items,
		IconURL: data.IconURL,
	}
	if data.Issue.Key != "" {
		c.Issue = &notify.IssueRef{
			Key:         data.Issue.Key,
			Title:       data.Issue.Title,
			Type:        data.Issue.Type,
			URL:         data.Issue.URL,
			Description: data.Issue.Description,
			IconURL:     data.Issue.IconURL,
		}
	}
	if data.Preview.Name != "" || data.Preview.URL != "" {
		c.Preview = &notify.PreviewRef{Name: data.Preview.Name, URL: data.Preview.URL}
	}
	return c
}

// renderNotifyTemplate parses and executes a Go text/template. Returns
// empty string on empty pattern or error, reporting the failure under
// configKey so a template that stopped being honored doesn't read as
// one that was never set.
func renderNotifyTemplate(configKey, pattern string, data notifyTemplateData) string {
	if pattern == "" {
		return ""
	}

	tmpl, err := template.New("notify").Parse(pattern)
	if err != nil {
		reportTemplateFailure(configKey, pattern, err, nil)
		return ""
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		reportTemplateFailure(configKey, pattern, err, nil)
		return ""
	}

	return buf.String()
}

// newPreviewProviderImpl creates a preview.Provider for the given
// workspace, resolving which adapter to build through the services
// registry (preview.provider in config).
//
// Everything the CLI knows and an adapter can't read out of config
// travels in preview.Deps: the tracker that holds the env-to-issue
// binding, the URL template, and the workflow targets — which are the
// configured workflows intersected with the active workspace's
// repositories, so they need the workspace this function was handed.
//
// The pipeline and tracker are optional — if either is unavailable,
// the returned provider still supports the read paths (Get, Inspect)
// and gracefully reports ErrNoPipeline / nothing-to-write on the
// write paths.
func newPreviewProviderImpl(workspace string) (preview.Provider, error) {
	pipeline, _ := newCICD()
	tracker, _ := newIssueTracker()

	const stage = preview.ConfigGroup
	urlTmpl, err := stageURLTemplate(stage)
	if err != nil {
		return nil, err
	}

	return services.PreviewProvider(providerConfig{}, preview.Deps{
		Tracker:     tracker,
		URLTemplate: urlTmpl,
		Workflow: preview.WorkflowDeps{
			Pipeline:  pipeline,
			Stage:     stage,
			Targets:   memoizedWorkflowTargets(workspace),
			InputName: stageInputName,
		},
	})
}

// memoizedWorkflowTargets adapts resolveWorkflowTargets to the shape
// preview.Deps carries, caching per sub-stage for the life of the
// returned closure — which is one provider, built once per command.
//
// The cache is not premature. In per-repo mode resolution walks the
// workspace status and shells out to parse each repo's git remote, and
// the provider now asks twice per operation: once to answer Ready
// before the row is planned, once to dispatch. Neither the config nor
// the active repo set changes mid-command, so the second answer is the
// first one by construction. Errors are cached alongside successes for
// the same reason — a target that will not parse will not parse twice.
func memoizedWorkflowTargets(workspace string) func(context.Context, string) ([]preview.Target, error) {
	type resolved struct {
		targets []preview.Target
		err     error
	}
	var mu sync.Mutex
	cache := map[string]resolved{}

	return func(ctx context.Context, subStage string) ([]preview.Target, error) {
		mu.Lock()
		defer mu.Unlock()
		if r, ok := cache[subStage]; ok {
			return r.targets, r.err
		}

		var r resolved
		raw, err := resolveWorkflowTargets(ctx, workspace, subStage)
		if err != nil {
			r.err = err
		} else {
			r.targets = make([]preview.Target, len(raw))
			for i, t := range raw {
				r.targets[i] = preview.Target{
					Owner:    t.Owner,
					Repo:     t.Repo,
					Workflow: t.Workflow,
					Label:    t.Label,
				}
			}
		}
		cache[subStage] = r
		return r.targets, r.err
	}
}

// WorkflowTarget represents a resolved GitHub Actions workflow to trigger.
type WorkflowTarget struct {
	Owner    string // GitHub owner (e.g., "ExtrackerInc").
	Repo     string // GitHub repo name (e.g., "devops-tooling").
	Workflow string // Workflow filename for the API call (e.g., "deploy.yml").
	Label    string // Display label for plan cards (local repo name or workflow repo).
}

// parseWorkflowPath parses an absolute workflow path
// (owner/repo/.github/workflows/file.yml) into a WorkflowTarget.
func parseWorkflowPath(path string) (WorkflowTarget, error) {
	parts := strings.SplitN(path, "/", 3)
	if len(parts) < 3 || parts[0] == "" || parts[1] == "" {
		return WorkflowTarget{}, fmt.Errorf("invalid workflow path %q: expected owner/repo/.github/workflows/file.yml", path)
	}
	// Extract just the filename from the full path for the API call.
	workflow := path[strings.LastIndex(path, "/")+1:]
	return WorkflowTarget{
		Owner:    parts[0],
		Repo:     parts[1],
		Workflow: workflow,
		Label:    parts[1],
	}, nil
}

// resolveWorkflowTargets resolves the workflows configured for a preview
// sub-stage ("preview.up", "preview.down"). Returns nil if the sub-stage
// is not configured.
//
// The sub-stage doubles as the config prefix — the preview block is laid
// out as preview.<sub-stage>.workflow — so the caller names the stage
// once and this reads the key beneath it. Release targets do not come
// through here: they carry per-service deploy environments and are
// resolved by resolveReleaseDeployTargets.
//
// Config shapes:
//   - String → global mode: one workflow triggered once
//   - Map    → per-repo mode: keyed by local repo name, intersected with
//     active workspace repos. Values are strings or lists of strings.
//
// Relative paths (starting with ".github/") are resolved to absolute paths
// using the local repo's git remote.
func resolveWorkflowTargets(ctx context.Context, workspace string, subStage string) ([]WorkflowTarget, error) {
	key := subStage + ".workflow"

	// Try string first (global mode).
	if s := viper.GetString(key); s != "" {
		t, err := parseWorkflowPath(s)
		if err != nil {
			return nil, err
		}
		return []WorkflowTarget{t}, nil
	}

	// Try map (per-repo mode).
	raw := viper.Get(key)
	if raw == nil {
		return nil, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, nil
	}

	// Build repo name → Repository lookup from active workspace.
	repos, err := resolveActiveRepositories(ctx, workspace, nil)
	if err != nil {
		return nil, err
	}
	repoByName := make(map[string]Repository, len(repos))
	for _, r := range repos {
		repoByName[r.Name] = r
	}

	var targets []WorkflowTarget
	for repoName, v := range m {
		repo, active := repoByName[repoName]
		if !active {
			continue
		}

		// Collect workflow path strings (value is string or []interface{}).
		var paths []string
		switch val := v.(type) {
		case string:
			paths = []string{val}
		case []any:
			for _, item := range val {
				if s, ok := item.(string); ok {
					paths = append(paths, s)
				}
			}
		default:
			continue
		}

		for _, p := range paths {
			// Resolve relative paths to absolute.
			if strings.HasPrefix(p, ".github/") {
				identity, err := code.ParseRemote(ctx, repo.Path)
				if err != nil {
					continue
				}
				p = fmt.Sprintf("%s/%s/%s", identity.Owner, identity.Name, p)
			}

			t, err := parseWorkflowPath(p)
			if err != nil {
				continue
			}
			t.Label = repoName
			targets = append(targets, t)
		}
	}

	return targets, nil
}

// defaultReleaseVersionInput is the workflow_dispatch input name that
// carries the git tag to deploy. Overridable via
// cicd.workflows.release.inputs.version for the rare non-"version" name.
const defaultReleaseVersionInput = "version"

// releaseVersionInput returns the configured version input name, or the
// default "version".
func releaseVersionInput() string {
	if v := viper.GetString("cicd.workflows.release.inputs.version"); v != "" {
		return v
	}
	return defaultReleaseVersionInput
}

// DeployTarget is one resolved per-service production deploy target: the
// workflow to dispatch and the GitHub Deployments environment to read
// the currently-live version from. A single-service repo yields one
// target (Service == RepoName, Environment "production"); a monorepo
// yields one per service.
type DeployTarget struct {
	Owner       string // GitHub owner (from ParseRemote of the local repo)
	Repo        string // GitHub repo name
	RepoName    string // bosun local repo name — keys tag/affected lookups
	Service     string // bosun service name; == RepoName for single-service repos
	Workflow    string // workflow filename for the dispatch API
	Environment string // GitHub Deployments environment (default "production")
	Label       string // display: "repo" or "repo · service"
}

// releaseTargetKey is the config path for production deploy targets.
// Centrally it holds a map keyed by repo name; a repository's own
// descriptor holds the value that would sit under its key.
const releaseTargetKey = "cicd.workflows.release.target"

// resolveReleaseDeployTargets resolves per-service production deploy
// targets from cicd.workflows.release.target. A repository's value is
// either a workflow-path string (single-service; env "production") or a
// per-service map {service: workflow-string | {workflow, environment}}.
// Env for a map entry defaults to "<service>-production" unless overridden.
// Returns nil when unconfigured. Only active workspace repos are included.
//
// The walk is over the active repositories rather than over the central
// map's keys, which is the inversion the per-repo layer forces: a
// repository configured only by its own descriptor appears in no
// central map, so a map-first walk would never reach it. Iterating
// repositories reaches both layers through repoConfig, and sorting by
// name keeps the plan's row order stable — resolveActiveRepositories
// returns whatever order the workspace manager reports.
func resolveReleaseDeployTargets(ctx context.Context, workspace string) ([]DeployTarget, error) {
	repos, err := resolveActiveRepositories(ctx, workspace, nil)
	if err != nil {
		return nil, err
	}
	sorted := slices.Clone(repos)
	slices.SortFunc(sorted, func(a, b Repository) int { return strings.Compare(a.Name, b.Name) })

	var targets []DeployTarget
	for _, repo := range sorted {
		// A bare central string is the whole-workspace form, which this
		// deployment-aware path doesn't support; repoKeyed returns nil
		// for it because there is no per-repo level to index into.
		raw := loadRepoConfig(repo).repoKeyed(releaseTargetKey)
		if raw == nil {
			continue
		}
		ts, err := parseServiceDeployValue(ctx, repo, repo.Name, raw)
		if err != nil {
			return nil, err
		}
		targets = append(targets, ts...)
	}
	return targets, nil
}

// parseServiceDeployValue turns one repo's release.target value into
// deploy targets. String → a single-service target (service == repo, env
// "production"). Map → one target per service key (value: workflow-path
// string, or {workflow, environment}); env defaults to
// "<service>-production".
func parseServiceDeployValue(ctx context.Context, repo Repository, repoName string, v any) ([]DeployTarget, error) {
	switch val := v.(type) {
	case string:
		wt, err := resolveWorkflowFilename(ctx, repo, val)
		if err != nil {
			return nil, err
		}
		return []DeployTarget{{
			Owner: wt.Owner, Repo: wt.Repo, RepoName: repoName,
			Service: repoName, Workflow: wt.Workflow,
			Environment: "production",
			Label:       deployLabel(ctx, repo, wt, repoName),
		}}, nil
	case map[string]any:
		svcs := make([]string, 0, len(val))
		for s := range val {
			svcs = append(svcs, s)
		}
		sort.Strings(svcs)

		var out []DeployTarget
		for _, svc := range svcs {
			var workflow, env string
			switch sv := val[svc].(type) {
			case string:
				workflow = sv
			case map[string]any:
				workflow, _ = sv["workflow"].(string)
				env, _ = sv["environment"].(string)
			default:
				continue
			}
			if workflow == "" {
				continue
			}
			wt, err := resolveWorkflowFilename(ctx, repo, workflow)
			if err != nil {
				return nil, err
			}
			if env == "" {
				env = svc + "-production"
			}
			out = append(out, DeployTarget{
				Owner: wt.Owner, Repo: wt.Repo, RepoName: repoName,
				Service: svc, Workflow: wt.Workflow,
				Environment: env,
				Label:       deployLabel(ctx, repo, wt, repoName+" · "+svc),
			})
		}
		return out, nil
	}
	return nil, nil
}

// deployLabel is the plan row's name for one deploy target: the local
// name (repo, or "repo · service"), plus the dispatch destination when
// that ISN'T the local repository.
//
// The plan is the approval gate for a production deploy, and the label
// is the only part of a target it renders — so a row reading `api` has
// to mean "a workflow in api". A workflow path may be absolute
// (owner/repo/.github/…), and now that release.target is repo-scoped
// that path can come from a file committed to the repository rather
// than from the central config the operator wrote. A row naming only
// the local repo would let a dispatch — carrying the version input,
// under the operator's own credentials — go somewhere the approval
// never showed.
//
// Annotating only a PROVEN mismatch leaves the common row unchanged. An
// unreadable remote annotates nothing, which is deliberate and is the
// weaker of the two available failures: annotating on "couldn't tell"
// would decorate every row in a repo whose remote won't parse — most of
// them pointing at that same repo — and a warning that fires on
// ordinary configuration is one operators learn to read past.
//
// The case that motivates the annotation is unaffected. A descriptor
// redirecting a dispatch lives in a normal repository with a working
// remote, so the mismatch is provable; a repo whose origin can't be
// read has no pushed branch, no merged PR and no release tag, and never
// reaches the deploy plan at all.
func deployLabel(ctx context.Context, repo Repository, wt WorkflowTarget, local string) string {
	identity, err := code.ParseRemote(ctx, repo.Path)
	if err != nil {
		return local
	}
	if strings.EqualFold(identity.Owner, wt.Owner) && strings.EqualFold(identity.Name, wt.Repo) {
		return local
	}
	return local + " → " + wt.Owner + "/" + wt.Repo
}

// resolveWorkflowFilename resolves a workflow path (absolute
// owner/repo/.github/... or relative .github/...) into a WorkflowTarget,
// resolving relative paths through the repo's git remote.
func resolveWorkflowFilename(ctx context.Context, repo Repository, path string) (WorkflowTarget, error) {
	if strings.HasPrefix(path, ".github/") {
		identity, err := code.ParseRemote(ctx, repo.Path)
		if err != nil {
			return WorkflowTarget{}, err
		}
		path = fmt.Sprintf("%s/%s/%s", identity.Owner, identity.Name, path)
	}
	return parseWorkflowPath(path)
}

// repoHasServices reports whether a repository contributes anything to
// the deployment surfaces — the rule detectRepoAffected applies to
// decide whether a repo is tracked at all. Note an *absent* config
// isn't the empty case: resolveRepoServiceNames falls back to the repo
// name as its own service, so only an explicitly empty list or a map
// with nothing but _shared answers false.
//
// Exposed so callers that need the answer *before* spending work on a
// repo (emitDeploymentSources drops these repos ahead of its gather
// rather than stepping through them) ask the same question rather than
// a lookalike of it.
func repoHasServices(r Repository) bool {
	return len(resolveRepoServiceNames(r)) > 0
}

// resolveRepoServiceNames returns the service names configured for a single
// repository. Supports string, list, and map config shapes. Falls back to
// the repo name when not configured.
//
// It takes the whole Repository rather than its name because the answer
// now comes from the repository's own `.bosun.yaml` when it has one,
// with the central `services.<repo>` map as the fallback. Which path
// the caller resolved therefore decides which topology is read: the
// workspace-scoped callers pass worktree paths, so a branch that adds a
// service is seen by affected-service detection on that branch — the
// capability a central map structurally cannot have.
func resolveRepoServiceNames(r Repository) []string {
	raw := loadRepoConfig(r).repoKeyed(servicesConfigGroup)

	switch val := raw.(type) {
	case string:
		return []string{val}
	case []any:
		var names []string
		for _, item := range val {
			if s, ok := item.(string); ok {
				names = append(names, s)
			}
		}
		return names
	case map[string]any:
		var names []string
		for k := range val {
			if k != "_shared" {
				names = append(names, k)
			}
		}
		// Sorted because a YAML mapping has no order to preserve, and
		// Go randomizes map iteration — so without this the same config
		// yields a different slice from one call to the next. Callers
		// compare these names (affected-service narrowing), render them,
		// and key deploy targets off them, so the nondeterminism reaches
		// output and assertions rather than staying internal.
		//
		// The list branch above is deliberately left in declaration
		// order: a YAML sequence is written in an order the author
		// chose, and reordering it would discard information this
		// branch doesn't have.
		sort.Strings(names)
		return names
	default:
		// Not configured — repo name is the service name.
		return []string{r.Name}
	}
}

// stageInputName returns the configured workflow input parameter name
// for a bosun concept ("name", "issue") within a preview sub-stage
// ("preview.up", "preview.down"). Returns empty string if not
// configured, signaling callers to skip.
//
// Config path: <sub-stage>.inputs.<concept>
func stageInputName(subStage, concept string) string {
	return viper.GetString(subStage + ".inputs." + concept)
}

// stageURLLegacyFields is the retirement the shared map cannot claim
// globally: bare {{.Name}} was this template's own spelling before the
// vocabulary was unified, and elsewhere .Name is a range variable's
// field rather than a stale reference.
var stageURLLegacyFields = map[string]string{"Name": "Preview.Name"}

// stageURLTemplate parses <stage>.url_template and proves it can
// actually render, returning nil when none is configured.
//
// The render is the point. Parsing alone admits {{.Name}} — valid
// syntax naming a field that no longer exists — and the adapters
// render URLTemplate themselves, deep inside Get/Inspect/List, where
// they have no way to report and simply return "". A legacy template
// would then leave `bosun preview list` printing blank URLs and the
// cicd adapter probing nothing, in silence. This is the one place that
// sees the template before anything depends on it.
//
// A template that cannot render is a hard error rather than a warning,
// matching what a parse failure already did: every preview URL in the
// run comes from it, so there is no degraded mode worth having.
func stageURLTemplate(stage string) (*template.Template, error) {
	configKey := stage + ".url_template"
	pattern := viper.GetString(configKey)
	if pattern == "" {
		return nil, nil
	}

	fail := func(err error) error {
		if hint := templateMigrationHint(pattern, stageURLLegacyFields); hint != "" {
			return fmt.Errorf("%s: %w — %s", configKey, err, hint)
		}
		return fmt.Errorf("%s: %w", configKey, err)
	}

	parsed, err := template.New("stage-url").Parse(pattern)
	if err != nil {
		return nil, fail(err)
	}
	// The context carries only Preview.Name and Preview.URL, so a probe
	// value exercises every field a real render could reach.
	if err := parsed.Execute(io.Discard, preview.URLTemplateData{
		Preview: preview.Ref{Name: "probe"},
	}); err != nil {
		return nil, fail(err)
	}
	return parsed, nil
}

// renderStageURL renders the url_template for a stage with the given name.
// Returns empty string if the template is not configured or rendering fails.
//
// Config path: <stage>.url_template
func renderStageURL(stage, name string) string {
	configKey := stage + ".url_template"
	pattern := viper.GetString(configKey)
	if pattern == "" {
		return ""
	}
	tmpl, err := template.New("stage-url").Parse(pattern)
	if err != nil {
		reportTemplateFailure(configKey, pattern, err, stageURLLegacyFields)
		return ""
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, preview.URLTemplateData{Preview: preview.Ref{Name: name}}); err != nil {
		reportTemplateFailure(configKey, pattern, err, stageURLLegacyFields)
		return ""
	}
	return buf.String()
}
