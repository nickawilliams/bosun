package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/nickawilliams/bosun/internal/code"
	"github.com/nickawilliams/bosun/internal/vcs"
)

// seamVCS / seamHost are minimal embedded-interface fakes for the
// release seams: only the methods deployTargetResolver and
// resolveMultiUserContext actually touch are implemented; anything
// else panics via the embedded nil interface, keeping the tests
// honest about what these paths depend on.
type seamVCS struct {
	vcs.VCS
	branch        string
	branchErr     error
	head          string
	headErr       error
	defaultBranch string
	defaultErr    error
	tagsBySHA     map[string][]string
	tagsErr       error
}

func (f seamVCS) GetCurrentBranch(context.Context, string) (string, error) {
	return f.branch, f.branchErr
}
func (f seamVCS) HeadSHA(context.Context, string) (string, error) { return f.head, f.headErr }
func (f seamVCS) GetDefaultBranch(context.Context, string) (string, error) {
	return f.defaultBranch, f.defaultErr
}
func (f seamVCS) FetchTags(context.Context, string, string) error { return nil }
func (f seamVCS) TagsContaining(_ context.Context, _ string, sha string) ([]string, error) {
	if f.tagsErr != nil {
		return nil, f.tagsErr
	}
	return f.tagsBySHA[sha], nil
}

type seamHost struct {
	code.Host
	pr         code.PullRequest
	prErr      error
	releases   map[string]code.Release // published releases by tag
	relErrs    map[string]error        // per-tag lookup error overrides
	deployment code.Deployment
	depErr     error
	rangePRs   []code.PullRequest
	rangeErr   error

	gotExclude []int // PRsInRange's excludeNumbers, recorded
}

func (f *seamHost) GetPRForBranch(context.Context, string, string, string) (code.PullRequest, error) {
	return f.pr, f.prErr
}
func (f *seamHost) GetReleaseByTag(_ context.Context, _, _, tag string) (code.Release, error) {
	if err, ok := f.relErrs[tag]; ok {
		return code.Release{}, err
	}
	if rel, ok := f.releases[tag]; ok {
		return rel, nil
	}
	return code.Release{}, code.ErrNotFound
}
func (f *seamHost) GetLatestDeployment(context.Context, string, string, string) (code.Deployment, error) {
	return f.deployment, f.depErr
}
func (f *seamHost) PRsInRange(_ context.Context, _, _, _, _ string, exclude []int) ([]code.PullRequest, error) {
	f.gotExclude = exclude
	return f.rangePRs, f.rangeErr
}

// ParseRemote reads the repo's own remote, exactly as a real host does —
// these seams are pointed at scratch directories, and whether a path
// resolves to a repository identity is part of what they exercise.
func (f *seamHost) ParseRemote(ctx context.Context, repositoryPath string) (code.RepositoryIdentity, error) {
	return code.ParseRemote(ctx, repositoryPath)
}

// --- resolveMultiUserContext: the sweep-up walk ---

func multiUserTarget() *releaseTarget {
	return &releaseTarget{
		repo:       Repository{Name: "api", Path: "/x/api"},
		branch:     "feature-x",
		owner:      "org",
		repoName:   "api",
		currentTag: "v1.3.0",
	}
}

// TestResolveMultiUserContextSweepUp locks the containing-release
// walk: which release-shaped tag containing the merged work wins, and
// how tag-without-release and unconfirmable-release cases resolve.
func TestResolveMultiUserContextSweepUp(t *testing.T) {
	ctx := context.Background()
	mergedPR := code.PullRequest{Number: 7, State: "merged", MergeCommitSHA: "MERGE"}

	t.Run("lowest containing release wins", func(t *testing.T) {
		rt := multiUserTarget()
		g := seamVCS{defaultBranch: "main",
			tagsBySHA: map[string][]string{"MERGE": {"v1.3.0", "v1.2.9", "infra-2024"}}}
		host := &seamHost{pr: mergedPR, releases: map[string]code.Release{
			"v1.2.9": {Tag: "v1.2.9", URL: "https://r/v1.2.9"},
			"v1.3.0": {Tag: "v1.3.0", URL: "https://r/v1.3.0"},
		}}
		resolveMultiUserContext(ctx, g, host, rt)
		if rt.containingRelease == nil || rt.containingRelease.Tag != "v1.2.9" {
			t.Fatalf("containingRelease = %+v, want the lowest-semver release v1.2.9", rt.containingRelease)
		}
	})

	t.Run("tag without release doesn't shadow the real release", func(t *testing.T) {
		rt := multiUserTarget()
		g := seamVCS{defaultBranch: "main",
			tagsBySHA: map[string][]string{"MERGE": {"v1.2.9", "v1.3.0"}}}
		// v1.2.9 is a bare tag (404); v1.3.0 is the published release.
		host := &seamHost{pr: mergedPR, releases: map[string]code.Release{
			"v1.3.0": {Tag: "v1.3.0", URL: "https://r/v1.3.0"},
		}}
		resolveMultiUserContext(ctx, g, host, rt)
		if rt.containingRelease == nil || rt.containingRelease.Tag != "v1.3.0" {
			t.Fatalf("containingRelease = %+v, want the walk to pass the bare tag and land on v1.3.0", rt.containingRelease)
		}
	})

	t.Run("unconfirmable release falls back to the synthetic tag", func(t *testing.T) {
		rt := multiUserTarget()
		g := seamVCS{defaultBranch: "main",
			tagsBySHA: map[string][]string{"MERGE": {"v1.2.9"}}}
		host := &seamHost{pr: mergedPR,
			relErrs: map[string]error{"v1.2.9": errors.New("HTTP 500")}}
		resolveMultiUserContext(ctx, g, host, rt)
		if rt.containingRelease == nil || rt.containingRelease.Tag != "v1.2.9" || rt.containingRelease.URL != "" {
			t.Fatalf("containingRelease = %+v, want the URL-less synthetic release for the unconfirmed tag", rt.containingRelease)
		}
	})

	t.Run("unmerged branch probes local HEAD and finds nothing", func(t *testing.T) {
		rt := multiUserTarget()
		g := seamVCS{defaultBranch: "main", head: "LOCAL",
			tagsBySHA: map[string][]string{}}
		host := &seamHost{pr: code.PullRequest{Number: 7, State: "open"}}
		resolveMultiUserContext(ctx, g, host, rt)
		if rt.containingRelease != nil {
			t.Fatalf("containingRelease = %+v, want nil for unreleased work", rt.containingRelease)
		}
	})

	t.Run("extras exclude the workspace's own PR", func(t *testing.T) {
		rt := multiUserTarget()
		g := seamVCS{defaultBranch: "main", head: "LOCAL", tagsBySHA: map[string][]string{}}
		host := &seamHost{
			pr:       mergedPR,
			rangePRs: []code.PullRequest{{Number: 9, Title: "other work"}},
		}
		resolveMultiUserContext(ctx, g, host, rt)
		if len(rt.extraPRs) != 1 || rt.extraPRs[0].Number != 9 {
			t.Fatalf("extraPRs = %+v, want the range PR", rt.extraPRs)
		}
		if len(host.gotExclude) != 1 || host.gotExclude[0] != 7 {
			t.Errorf("PRsInRange exclude = %v, want the workspace PR number [7]", host.gotExclude)
		}
	})
}

// --- deployTargetResolver: tag-aware deployed-state resolution ---

func seamResolver(g vcs.VCS, host code.Host, override string) *deployTargetResolver {
	return &deployTargetResolver{
		g:           g,
		host:        host,
		pathByName:  map[string]string{"api": "/x/api"},
		cache:       make(map[string]deployRepoInfo),
		tagOverride: override,
	}
}

func seamTarget() DeployTarget {
	return DeployTarget{Owner: "org", Repo: "api", RepoName: "api",
		Service: "api", Environment: "production", Label: "api"}
}

// TestDeployTargetResolverResolve locks the D-resolution paths the
// review found untested: ref-is-tag, SHA→containing-tag mapping,
// deployed-from-branch (unknown), never-deployed, API failure, and
// the work-tag resolution failures.
func TestDeployTargetResolverResolve(t *testing.T) {
	ctx := context.Background()
	// Work resolution: HEAD "H" is contained in release v1.2.0.
	workVCS := func(extra map[string][]string) seamVCS {
		tags := map[string][]string{"H": {"v1.2.0"}}
		for k, v := range extra {
			tags[k] = v
		}
		return seamVCS{branch: "feature-x", head: "H", tagsBySHA: tags}
	}

	t.Run("deployed ref is a tag → direct comparison", func(t *testing.T) {
		host := &seamHost{deployment: code.Deployment{Ref: "v1.1.0", SHA: "D"}}
		st := seamResolver(workVCS(nil), host, "").resolve(ctx, seamTarget())
		if st.err != nil {
			t.Fatalf("err = %v", st.err)
		}
		if st.deployedTag != "v1.1.0" || st.state != deployGo || st.reason != "v1.1.0 → v1.2.0" {
			t.Errorf("st = state %v reason %q deployed %q, want v1.1.0 → v1.2.0 go", st.state, st.reason, st.deployedTag)
		}
	})

	t.Run("deployed ref is a branch → SHA maps to its containing tag", func(t *testing.T) {
		host := &seamHost{deployment: code.Deployment{Ref: "main", SHA: "D"}}
		st := seamResolver(workVCS(map[string][]string{"D": {"v1.1.0"}}), host, "").resolve(ctx, seamTarget())
		if st.deployedTag != "v1.1.0" || st.state != deployGo {
			t.Errorf("st = state %v deployed %q, want SHA-mapped v1.1.0 go", st.state, st.deployedTag)
		}
	})

	t.Run("deployed SHA maps to no release → permissive unknown", func(t *testing.T) {
		host := &seamHost{deployment: code.Deployment{Ref: "main", SHA: "D"}}
		st := seamResolver(workVCS(nil), host, "").resolve(ctx, seamTarget())
		if st.state != deployGo || !strings.Contains(st.reason, "deployed state unknown") {
			t.Errorf("st = state %v reason %q, want permissive go with unknown-state label", st.state, st.reason)
		}
	})

	t.Run("never deployed → first deploy", func(t *testing.T) {
		host := &seamHost{depErr: code.ErrNotFound}
		st := seamResolver(workVCS(nil), host, "").resolve(ctx, seamTarget())
		if st.state != deployGo || !strings.Contains(st.reason, "first deploy") {
			t.Errorf("st = state %v reason %q, want first-deploy go", st.state, st.reason)
		}
	})

	t.Run("deployments API failure → ✗ row, not a deploy", func(t *testing.T) {
		host := &seamHost{depErr: errors.New("HTTP 500")}
		st := seamResolver(workVCS(nil), host, "").resolve(ctx, seamTarget())
		if st.err == nil {
			t.Fatalf("st = %+v, want an err row (fail closed) for an API failure", st)
		}
	})

	t.Run("no release contains the work → block", func(t *testing.T) {
		g := seamVCS{branch: "feature-x", head: "H", tagsBySHA: map[string][]string{}}
		host := &seamHost{deployment: code.Deployment{Ref: "v1.1.0"}}
		st := seamResolver(g, host, "").resolve(ctx, seamTarget())
		if st.state != deployBlock {
			t.Errorf("state = %v, want deployBlock when no release contains the work", st.state)
		}
	})

	t.Run("PR lookup failure with no containing tag → ✗ row", func(t *testing.T) {
		// For a squash-merged branch only the PR's merge commit maps to
		// the containing release; with the lookup down, "nothing
		// contains this work" is indistinguishable from "couldn't see
		// the PR" — must fail closed rather than misdirect to prerelease.
		g := seamVCS{branch: "feature-x", head: "H", tagsBySHA: map[string][]string{}}
		host := &seamHost{prErr: errors.New("api down")}
		st := seamResolver(g, host, "").resolve(ctx, seamTarget())
		if st.err == nil {
			t.Fatalf("st = %+v, want an err row when the work release can't be resolved", st)
		}
	})

	t.Run("repo outside the workspace → ✗ row", func(t *testing.T) {
		st := seamResolver(seamVCS{}, &seamHost{}, "").resolve(ctx, DeployTarget{RepoName: "ghost", Label: "ghost"})
		if st.err == nil || !strings.Contains(st.err.Error(), "not an active workspace repo") {
			t.Fatalf("err = %v, want the unmapped-repo error", st.err)
		}
	})

	t.Run("merged PR's merge commit resolves the work tag", func(t *testing.T) {
		// Squash-merge shape: local HEAD maps to nothing, the PR's
		// MergeCommitSHA is what the release contains.
		g := seamVCS{branch: "feature-x", head: "H",
			tagsBySHA: map[string][]string{"MERGE": {"v1.2.0"}}}
		host := &seamHost{
			pr:         code.PullRequest{Number: 7, State: "merged", MergeCommitSHA: "MERGE"},
			deployment: code.Deployment{Ref: "v1.1.0"},
		}
		st := seamResolver(g, host, "").resolve(ctx, seamTarget())
		if st.workTag != "v1.2.0" || st.state != deployGo {
			t.Errorf("st = workTag %q state %v, want the merge commit's release to gate", st.workTag, st.state)
		}
	})
}
