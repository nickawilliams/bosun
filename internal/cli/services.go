package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"text/template"

	"github.com/nickawilliams/bosun/internal/cicd"
	"github.com/nickawilliams/bosun/internal/cicd/githubactions"
	"github.com/nickawilliams/bosun/internal/code"
	gh "github.com/nickawilliams/bosun/internal/code/github"
	"github.com/nickawilliams/bosun/internal/config"
	"github.com/nickawilliams/bosun/internal/issue"
	"github.com/nickawilliams/bosun/internal/issue/jira"
	"github.com/nickawilliams/bosun/internal/notify"
	"github.com/nickawilliams/bosun/internal/notify/slack"
	"github.com/nickawilliams/bosun/internal/preview"
	previewcicd "github.com/nickawilliams/bosun/internal/preview/cicd"
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
	patterns := viper.GetStringSlice("repositories")
	if len(patterns) == 0 {
		return nil, fmt.Errorf("no repository patterns configured: set repositories in .bosun/config.yaml")
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
		var filtered []Repository
		for _, r := range repositories {
			if filter[r.Name] {
				filtered = append(filtered, r)
			}
		}
		if len(filtered) == 0 {
			return nil, fmt.Errorf(
				"no repositories matched filter %v (available: %s)",
				filterNames, repositoryNames(repositories),
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
func emitWorkspaceIssuePreamble(ctx context.Context, issueKey string, alongside func()) issue.Issue {
	var detail issue.Issue
	var fetchErr error

	tracker, err := newIssueTracker()
	if err != nil || tracker == nil {
		return detail
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

	return detail
}

// emitLifecyclePreamble renders the visual preamble shown at the
// start of every lifecycle command (start, review, preview,
// prerelease, release, cleanup) — a thin wrapper over
// emitWorkspaceIssuePreamble with no alongside work, kept as a named
// symbol so call sites read as intent rather than mechanics. Future
// expansion of the preamble lands in the shared helper and every
// command inherits the change.
func emitLifecyclePreamble(ctx context.Context, issueKey string) issue.Issue {
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
	if name := viper.GetString("workspace"); name != "" {
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

// newIssueTrackerImpl is the production factory for issue.Tracker.
// Reachable via the newIssueTracker wrapper in services_factory.go,
// which dispatches through the swappable Services struct.
func newIssueTrackerImpl() (issue.Tracker, error) {
	if err := requireConfig("issue_tracker"); err != nil {
		return nil, err
	}

	provider := viper.GetString("issue_tracker.provider")
	switch provider {
	case "jira":
		return jira.New(
			viper.GetString("issue_tracker.base_url"),
			viper.GetString("issue_tracker.email"),
			viper.GetString("issue_tracker.token"),
		), nil
	default:
		return nil, fmt.Errorf("unsupported issue tracker: %q", provider)
	}
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

// newCodeHost creates a code.Host from current config. Resolution order:
// 1. code_host.token from viper (config file or GITHUB_TOKEN env)
// 2. gh auth token (GitHub CLI)
// 3. GITHUB_TOKEN env var
// 4. JIT prompt (saves to config)
func newCodeHostImpl() (code.Host, error) {
	// Check viper first (config file or env var via AutomaticEnv).
	if token := viper.GetString("code_host.token"); token != "" {
		return gh.New(token), nil
	}

	// Try automatic resolution (gh CLI, GITHUB_TOKEN env).
	if token := gh.ResolveToken(); token != "" {
		return gh.New(token), nil
	}

	// Fall back to config-prompted token.
	if err := requireConfig("code_host"); err != nil {
		return nil, err
	}

	provider := viper.GetString("code_host.provider")
	switch provider {
	case "github":
		return gh.New(viper.GetString("code_host.token")), nil
	default:
		return nil, fmt.Errorf("unsupported code host: %q", provider)
	}
}

// prTemplateData holds the fields available to PR title and body templates.
type prTemplateData struct {
	IssueKey   string
	IssueTitle string
	IssueType  string
	IssueURL   string
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
	pattern := viper.GetString("pull_request.title_template")
	if pattern == "" {
		pattern = "[{{.IssueKey}}] {{.IssueTitle}}"
	}
	result, err := executePRTemplate("pr-title", pattern, data)
	if err != nil {
		return fmt.Sprintf("[%s] %s", data.IssueKey, data.IssueTitle)
	}
	return result
}

// buildPRBody generates a PR body from the configured template and issue
// metadata. Returns empty string if no template is configured.
func buildPRBody(data prTemplateData) string {
	pattern := viper.GetString("pull_request.body_template")
	if pattern == "" {
		return ""
	}
	result, err := executePRTemplate("pr-body", pattern, data)
	if err != nil {
		return ""
	}
	return result
}

// notifyTemplateData holds the fields available to notification templates.
type notifyTemplateData struct {
	IssueKey    string
	IssueTitle  string
	IssueType   string        // e.g., "Story", "Bug".
	IssueURL    string
	IconURL     string        // Avatar or icon URL for card blocks.
	Items       []notify.Item // Per-repository items (PRs, releases, etc.).
	PreviewName string        // Ephemeral environment name (e.g., "brave-falcon").
	PreviewURL  string        // Rendered preview environment URL.
}

// Default block templates per notification type.
var defaultNotifyTemplates = map[string]map[string]string{
	"review": {
		"header":  "Ready for Review",
		"context": "via bosun",
	},
	"release": {
		"header":  "Release",
		"context": "via bosun",
	},
	"preview": {
		"body": "Preview deployment requested for <{{.IssueURL}}|{{.IssueKey}}>",
	},
}

// buildNotifyContent reads the template config for a notification type and
// renders it with the given data. Supports two config shapes:
//
//	slack.templates.review: "plain text template"     → Content.Text (no blocks)
//	slack.templates.review:                           → Content with block fields
//	  header: "..."
//	  body: "..."
//	  context: "..."
func buildNotifyContent(notifType string, data notifyTemplateData) notify.Content {
	key := "notification.templates." + notifType

	// Check if it's a simple string template.
	if s := viper.GetString(key); s != "" {
		return notify.Content{Text: renderTemplate(s, data)}
	}

	// Check if it's a map of block fields.
	sub := viper.GetStringMapString(key)

	// Fall back to defaults.
	defaults := defaultNotifyTemplates[notifType]

	get := func(field string) string {
		if v, ok := sub[field]; ok {
			return v
		}
		return defaults[field]
	}

	var sections []notify.Section

	// Jira ticket card.
	if data.IssueKey != "" {
		issueType := "Issue"
		if data.IssueType != "" {
			issueType = data.IssueType
		}
		title := data.IssueKey
		if data.IssueTitle != "" {
			title = fmt.Sprintf("[%s] %s", data.IssueKey, data.IssueTitle)
		}
		var buttons []notify.CardButton
		if data.IssueURL != "" {
			buttons = append(buttons, notify.CardButton{
				Text:  "View Issue",
				URL:   data.IssueURL,
				Style: "primary",
			})
		}
		sections = append(sections, notify.Section{
			Text:     ":jira: " + title,
			Subtitle: issueType,
			Buttons:  buttons,
		})
	}

	// Ephemeral deployment card.
	if data.PreviewName != "" || data.PreviewURL != "" {
		name := data.PreviewName
		if name == "" {
			name = "Preview"
		}
		var buttons []notify.CardButton
		if data.PreviewURL != "" {
			buttons = append(buttons, notify.CardButton{
				Text:  "View Deployment",
				URL:   data.PreviewURL,
				Style: "primary",
			})
		}
		sections = append(sections, notify.Section{
			Text:     ":cloud: " + name,
			Subtitle: "Ephemeral preview",
			Buttons:  buttons,
		})
	}

	// Per-repo PR card sections.
	for _, item := range data.Items {
		// Card title: PR title (same as what we set on the PR).
		title := data.IssueKey
		if data.IssueTitle != "" {
			title = fmt.Sprintf("[%s] %s", data.IssueKey, data.IssueTitle)
		}
		// Card subtitle: repo name + PR number.
		subtitle := fmt.Sprintf("`%s` %s", item.Label, item.Detail)
		var buttons []notify.CardButton
		if item.URL != "" {
			buttons = append(buttons, notify.CardButton{
				Text:  "View Pull Request",
				URL:   item.URL,
				Style: "primary",
			})
			if item.BranchURL != "" {
				buttons = append(buttons, notify.CardButton{
					Text: "View Branch",
					URL:  item.BranchURL,
				})
			}
		}
		sections = append(sections, notify.Section{
			Text:     title,
			Subtitle: subtitle,
			Body:     item.Body,
			IconURL:  data.IconURL,
			Buttons:  buttons,
		})
	}

	return notify.Content{
		Header:   renderTemplate(get("header"), data),
		Body:     renderTemplate(get("body"), data),
		Sections: sections,
		Context:  renderTemplate(get("context"), data),
	}
}

// renderTemplate parses and executes a Go text/template. Returns empty
// string on empty pattern or error.
func renderTemplate(pattern string, data notifyTemplateData) string {
	if pattern == "" {
		return ""
	}

	tmpl, err := template.New("notify").Parse(pattern)
	if err != nil {
		return ""
	}

	var buf strings.Builder
	if err := tmpl.Execute(&buf, data); err != nil {
		return ""
	}

	return buf.String()
}

// newNotifier creates a notify.Notifier from current config. Returns an error
// if the notification provider is not configured — callers treat this as a
// skip, not a fatal error. Does not prompt for missing values (opt-in only).
func newNotifierImpl() (notify.Notifier, error) {
	provider := viper.GetString("notification.provider")
	if provider == "" {
		return nil, fmt.Errorf("notification provider not configured")
	}

	switch provider {
	case "slack":
		auth := viper.GetString("notification.auth")
		if auth == "local" {
			workspace := viper.GetString("notification.workspace")
			if workspace == "" {
				return nil, fmt.Errorf("notification.workspace required for local auth")
			}
			token, cookie, err := slack.ResolveLocalToken(workspace)
			if err != nil {
				return nil, fmt.Errorf("resolving local Slack token: %w", err)
			}
			return slack.NewWithCookie(token, cookie), nil
		}

		// Token-based auth.
		if err := requireConfig("notification"); err != nil {
			return nil, err
		}
		return slack.New(viper.GetString("notification.token")), nil
	default:
		return nil, fmt.Errorf("unsupported notification provider: %q", provider)
	}
}

// newCICD creates a cicd.CICD from current config. Token resolution mirrors
// newCodeHost: viper → gh CLI → env → JIT prompt.
func newCICDImpl() (cicd.CICD, error) {
	// Reuse the same GitHub token used for code hosting.
	if token := viper.GetString("code_host.token"); token != "" {
		return githubactions.New(token), nil
	}
	if token := gh.ResolveToken(); token != "" {
		return githubactions.New(token), nil
	}

	// Fall back to config-prompted flow.
	if err := requireConfig("code_host"); err != nil {
		return nil, err
	}

	provider := viper.GetString("cicd.provider")
	switch provider {
	case "github_actions":
		return githubactions.New(viper.GetString("code_host.token")), nil
	default:
		return nil, fmt.Errorf("unsupported CI/CD provider: %q", provider)
	}
}

// newPreviewProvider creates a preview.Provider with the default
// OnInfo callback that renders incidental events inline as a success
// card with a title (the action, title-cased by default) and a muted
// raw-cased value. Suitable for commands where side-effect notifications
// can fire immediately alongside other output (e.g., the preview command
// itself).
func newPreviewProvider(workspace string) (preview.Provider, error) {
	return newPreviewProviderWithInfo(workspace, func(action, value string) {
		ui.NewCard(ui.CardSuccess, action).Value(value).Print()
	})
}

// newPreviewProviderWithInfo creates a preview.Provider with a custom
// OnInfo sink — useful when callers want to buffer side-effect events
// (e.g., the status command at project scope, which captures
// per-workspace cleanup notices and prints them after the relevant
// card so they don't race with the loading spinner).
//
// The pipeline and tracker are optional — if either is unavailable,
// the returned provider still supports the read paths (Get, Inspect)
// and gracefully reports ErrNoPipeline / nothing-to-write on the
// write paths.
func newPreviewProviderWithInfoImpl(workspace string, onInfo func(action, value string)) (preview.Provider, error) {
	pipeline, _ := newCICD()
	tracker, _ := newIssueTracker()

	const stage = "preview"
	var urlTmpl *template.Template
	if pattern := viper.GetString("cicd.workflows." + stage + ".url_template"); pattern != "" {
		parsed, err := template.New("stage-url").Parse(pattern)
		if err != nil {
			return nil, fmt.Errorf("preview url_template: %w", err)
		}
		urlTmpl = parsed
	}

	return previewcicd.New(previewcicd.Options{
		Pipeline:    pipeline,
		Tracker:     tracker,
		Stage:       stage,
		URLTemplate: urlTmpl,
		Targets: func(ctx context.Context, subStage string) ([]previewcicd.Target, error) {
			raw, err := resolveWorkflowTargets(ctx, workspace, subStage)
			if err != nil {
				return nil, err
			}
			out := make([]previewcicd.Target, len(raw))
			for i, t := range raw {
				out[i] = previewcicd.Target{
					Owner:    t.Owner,
					Repo:     t.Repo,
					Workflow: t.Workflow,
					Label:    t.Label,
				}
			}
			return out, nil
		},
		InputName: stageInputName,
		OnInfo:    onInfo,
	}), nil
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

// resolveWorkflowTargets resolves configured workflow targets for a lifecycle
// stage ("preview" or "release"). Returns nil if the stage is not configured.
//
// Config shapes:
//   - String → global mode: one workflow triggered once
//   - Map    → per-repo mode: keyed by local repo name, intersected with
//     active workspace repos. Values are strings or lists of strings.
//
// Relative paths (starting with ".github/") are resolved to absolute paths
// using the local repo's git remote.
func resolveWorkflowTargets(ctx context.Context, workspace string, stage string) ([]WorkflowTarget, error) {
	key := "cicd.workflows." + stage + ".target"

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
				identity, err := gh.ParseRemote(ctx, repo.Path)
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

// resolveRepoServiceNames returns the service names configured for a single
// repository. Supports string, list, and map config shapes. Falls back to
// the repo name when not configured.
func resolveRepoServiceNames(repoName string) []string {
	key := "services." + repoName
	raw := viper.Get(key)

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
		return names
	default:
		// Not configured — repo name is the service name.
		return []string{repoName}
	}
}

// stageInputName returns the configured workflow input parameter name for a
// given bosun concept (e.g., "services", "issue", "name") within a lifecycle
// stage. Returns empty string if not configured, signaling callers to skip.
//
// Config path: cicd.workflows.<stage>.inputs.<concept>
func stageInputName(stage, concept string) string {
	return viper.GetString("cicd.workflows." + stage + ".inputs." + concept)
}

// buildWorkflowInputs constructs the inputs map for a workflow dispatch.
// Reads input parameter names from the stage's config
// (github_actions.workflows.<stage>.inputs.*). Used by the release
// command; the preview command builds inputs inside its adapter.
func buildWorkflowInputs(cmd *cobra.Command, ctx context.Context, workspace, stage, issue string) (map[string]string, error) {
	inputs := make(map[string]string)

	if issueKey := stageInputName(stage, "issue"); issueKey != "" {
		inputs[issueKey] = issue
	}

	inputName := stageInputName(stage, "services")
	if inputName == "" {
		return inputs, nil
	}

	// --service flag overrides auto-detection.
	flagServices, _ := cmd.Flags().GetStringSlice("service")
	if len(flagServices) > 0 {
		inputs[inputName] = strings.Join(flagServices, ",")
		return inputs, nil
	}

	// Change-based detection: diff branches, filter to affected
	// services. Detection runs inside the observation group's per-repo
	// spinners (no PR resolution — this path only needs the services
	// list; image overrides are preview's concern).
	g := git.New()
	repos, repoBranch, err := prepareAffectedRepos(ctx, workspace, g)
	if err != nil {
		return nil, err
	}
	results, _, _, err := emitDeploymentSources(ctx, cmd, g, repos, repoBranch, false)
	if err != nil {
		return nil, err
	}

	var affected []string
	for _, r := range results {
		affected = append(affected, r.Services...)
	}
	if len(affected) > 0 {
		inputs[inputName] = strings.Join(affected, ",")
	}

	return inputs, nil
}

// stageURLTemplate holds the data available when rendering a stage URL.
type stageURLTemplate struct {
	Name string // Environment name (e.g., "brave-falcon").
}

// renderStageURL renders the url_template for a stage with the given name.
// Returns empty string if the template is not configured or rendering fails.
//
// Config path: cicd.workflows.<stage>.url_template
func renderStageURL(stage, name string) string {
	pattern := viper.GetString("cicd.workflows." + stage + ".url_template")
	if pattern == "" {
		return ""
	}
	tmpl, err := template.New("stage-url").Parse(pattern)
	if err != nil {
		return ""
	}
	var buf strings.Builder
	if err := tmpl.Execute(&buf, stageURLTemplate{Name: name}); err != nil {
		return ""
	}
	return buf.String()
}

