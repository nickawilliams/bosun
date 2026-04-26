package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

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
	"github.com/nickawilliams/bosun/internal/ui"
	"github.com/nickawilliams/bosun/internal/vcs/git"
	"github.com/nickawilliams/bosun/internal/workspace"
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

// fetchIssue fetches issue details from the tracker and renders a
// RunCardReplace card showing issue type, key, and title on success.
// An optional decorate callback can customize the success card with
// additional content (e.g., KV pairs) using the fetched detail.
func fetchIssue(ctx context.Context, tracker issue.Tracker, issueKey string, decorate ...func(issue.Issue, *ui.Card)) (issue.Issue, error) {
	var detail issue.Issue
	err := ui.RunCardReplace("Fetching issue", func() error {
		var e error
		detail, e = tracker.GetIssue(ctx, issueKey)
		return e
	}, func() *ui.Card {
		card := ui.NewCard(ui.CardSuccess, fmt.Sprintf("%s: %s", detail.Type, detail.Key)).
			Subtitle(detail.Title)
		if len(decorate) > 0 {
			decorate[0](detail, card)
		}
		return card
	})
	return detail, err
}

// resolveActiveRepositories resolves repositories scoped to the current
// workspace when CWD is inside one, falling back to resolveRepositories
// (global config patterns) otherwise. Commands that operate on worktrees
// (review, prerelease) should use this instead of resolveRepositories so
// they stay scoped to the workspace context.
func resolveActiveRepositories(ctx context.Context, filterNames []string) ([]Repository, error) {
	mgr, err := newWorkspaceManager()
	if err != nil {
		return resolveRepositories(filterNames)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return resolveRepositories(filterNames)
	}

	wsName, err := mgr.DetectWorkspace(cwd)
	if err != nil {
		return resolveRepositories(filterNames)
	}

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

	wsRoot := viper.GetString("workspace_root")
	if wsRoot == "" {
		return nil, fmt.Errorf("workspaces not configured (set workspace_root in config)")
	}
	if !filepath.IsAbs(wsRoot) {
		wsRoot = filepath.Join(projectRoot, wsRoot)
	}

	return workspace.NewManager(git.New(), wsRoot), nil
}

// resolveWorkspaceName returns the workspace name from args or auto-detects
// it from CWD.
func resolveWorkspaceName(args []string) (string, error) {
	if len(args) > 0 {
		return args[0], nil
	}

	projectRoot := config.FindProjectRoot()
	if projectRoot == "" {
		return "", fmt.Errorf("not inside a bosun project (no .bosun/ directory found)")
	}

	wsRoot := viper.GetString("workspace_root")
	if wsRoot == "" {
		return "", fmt.Errorf("workspaces not configured (set workspace_root in config)")
	}
	if !filepath.IsAbs(wsRoot) {
		wsRoot = filepath.Join(projectRoot, wsRoot)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}

	return workspace.DetectName(wsRoot, cwd)
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

// newIssueTracker creates an issue.Tracker from current config. Prompts for
// missing values interactively and saves them for future use.
func newIssueTracker() (issue.Tracker, error) {
	if err := requireConfig("issue_tracker"); err != nil {
		return nil, err
	}

	provider := viper.GetString("issue_tracker")
	switch provider {
	case "jira":
		if err := requireConfig("jira"); err != nil {
			return nil, err
		}
		return jira.New(
			viper.GetString("jira.base_url"),
			viper.GetString("jira.email"),
			viper.GetString("jira.token"),
		), nil
	default:
		return nil, fmt.Errorf("unsupported issue tracker: %q", provider)
	}
}

// resolveStatus maps a bosun lifecycle status key (e.g., "in_progress") to
// the provider-specific status name from config (e.g., "In Progress").
// Falls back to schema defaults if not set in config.
func resolveStatus(key string) (string, error) {
	name := viper.GetString("statuses." + key)
	if name != "" {
		return name, nil
	}

	// Check schema defaults.
	if group, ok := lookupGroup("statuses"); ok {
		for _, ck := range group.Keys {
			if ck.Key == key && ck.Default != "" {
				return ck.Default, nil
			}
		}
	}

	return "", fmt.Errorf("status %q not mapped in config statuses section", key)
}

// buildStatusIndex returns a mapping from lowercase provider status
// name to lifecycle sequence position. Unknown statuses are absent
// from the map; callers should treat missing entries as sorting to
// the end.
func buildStatusIndex() map[string]int {
	idx := make(map[string]int, len(lifecycleStatusKeys))
	for i, key := range lifecycleStatusKeys {
		name, err := resolveStatus(key)
		if err != nil {
			continue
		}
		idx[strings.ToLower(name)] = i
	}
	return idx
}

// validateStageTransition checks the issue's current status against the
// expected status for a lifecycle command. If the status is unexpected, warns
// and prompts for confirmation. In non-interactive mode, logs a warning and
// proceeds.
func validateStageTransition(ctx context.Context, tracker issue.Tracker, issueKey, expectedStatusKey string) error {
	current, err := tracker.GetIssue(ctx, issueKey)
	if err != nil {
		return fmt.Errorf("checking issue status: %w", err)
	}

	expectedStatus, err := resolveStatus(expectedStatusKey)
	if err != nil {
		return err
	}

	if !strings.EqualFold(current.Status, expectedStatus) {
		ui.Skip(fmt.Sprintf("Issue %s is in %q, expected %q", issueKey, current.Status, expectedStatus))
		if isInteractive() {
			confirmed, err := promptConfirm("Proceed anyway?", false)
			if err != nil {
				return err
			}
			if !confirmed {
				return fmt.Errorf("aborted: unexpected issue status")
			}
		} else {
			ui.Skip("Proceeding (non-interactive mode)")
		}
	}

	return nil
}

// newCodeHost creates a code.Host from current config. Resolution order:
// 1. github.token from viper (config file or BOSUN_GITHUB_TOKEN env)
// 2. gh auth token (GitHub CLI)
// 3. GITHUB_TOKEN env var
// 4. JIT prompt (saves to config)
func newCodeHost() (code.Host, error) {
	// Check viper first (config file or env var via AutomaticEnv).
	if token := viper.GetString("github.token"); token != "" {
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

	provider := viper.GetString("code_host")
	switch provider {
	case "github":
		if err := requireConfig("github"); err != nil {
			return nil, err
		}
		return gh.New(viper.GetString("github.token")), nil
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
	IssueKey   string
	IssueTitle string
	IssueType  string        // e.g., "Story", "Bug".
	IssueURL   string
	IconURL    string        // Avatar or icon URL for card blocks.
	Items      []notify.Item // Per-repository items (PRs, releases, etc.).
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
	key := "slack.templates." + notifType

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

	// Build issue + ephemeral as two-column fields.
	var fields []notify.Field
	if data.IssueKey != "" {
		issueType := "Issue"
		if data.IssueType != "" {
			issueType = data.IssueType
		}
		issueLink := data.IssueKey + ": " + data.IssueTitle
		if data.IssueURL != "" {
			issueLink = fmt.Sprintf("<%s|%s: %s>", data.IssueURL, data.IssueKey, data.IssueTitle)
		}
		fields = append(fields,
			notify.Field{
				Key:   fmt.Sprintf("*:jira: %s*\n%s", issueType, issueLink),
				Value: "*:cloud: Ephemeral*\n_-none-_",
			},
		)
	}

	// Build per-repo card sections.
	var sections []notify.Section
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
		Fields:   fields,
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
func newNotifier() (notify.Notifier, error) {
	provider := viper.GetString("notification")
	if provider == "" {
		return nil, fmt.Errorf("notification provider not configured")
	}

	switch provider {
	case "slack":
		auth := viper.GetString("slack.auth")
		if auth == "local" {
			workspace := viper.GetString("slack.workspace")
			if workspace == "" {
				return nil, fmt.Errorf("slack.workspace required for local auth")
			}
			token, cookie, err := slack.ResolveLocalToken(workspace)
			if err != nil {
				return nil, fmt.Errorf("resolving local Slack token: %w", err)
			}
			return slack.NewWithCookie(token, cookie), nil
		}

		// Token-based auth.
		if err := requireConfig("slack"); err != nil {
			return nil, err
		}
		return slack.New(viper.GetString("slack.token")), nil
	default:
		return nil, fmt.Errorf("unsupported notification provider: %q", provider)
	}
}

// newCICD creates a cicd.CICD from current config. Token resolution mirrors
// newCodeHost: viper → gh CLI → env → JIT prompt.
func newCICD() (cicd.CICD, error) {
	// Reuse the same GitHub token used for code hosting.
	if token := viper.GetString("github.token"); token != "" {
		return githubactions.New(token), nil
	}
	if token := gh.ResolveToken(); token != "" {
		return githubactions.New(token), nil
	}

	// Fall back to config-prompted flow.
	if err := requireConfig("cicd"); err != nil {
		return nil, err
	}

	provider := viper.GetString("cicd")
	switch provider {
	case "github_actions":
		if err := requireConfig("github"); err != nil {
			return nil, err
		}
		return githubactions.New(viper.GetString("github.token")), nil
	default:
		return nil, fmt.Errorf("unsupported CI/CD provider: %q", provider)
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
func resolveWorkflowTargets(ctx context.Context, stage string) ([]WorkflowTarget, error) {
	key := "github_actions." + stage

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
	repos, err := resolveActiveRepositories(ctx, nil)
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

