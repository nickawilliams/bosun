package cli

import (
	"reflect"
	"slices"
	"testing"

	"github.com/spf13/viper"
)

func TestRepoContextBaseBranch(t *testing.T) {
	tests := []struct {
		name          string
		defaultBranch string
		want          string
	}{
		{"host answered", "develop", "develop"},
		{"nothing resolved falls back", "", defaultBaseBranch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc := repoContext{defaultBranch: tt.defaultBranch}
			if got := rc.baseBranch(); got != tt.want {
				t.Errorf("baseBranch() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBasePlaceholder(t *testing.T) {
	// Every fixture repo is selected: the placeholder only ever considers
	// repos this run will write to (see TestBasePlaceholderIgnoresUnwrittenRepos).
	repos := func(branches ...string) []repoContext {
		out := make([]repoContext, len(branches))
		for i, b := range branches {
			out[i] = repoContext{defaultBranch: b, include: true}
		}
		return out
	}

	tests := []struct {
		name       string
		globalBase string
		resolved   []repoContext
		want       string
	}{
		{
			// An override answers for every repo, so it's the literal
			// the prompt offers regardless of what the repos default to.
			name:       "global override wins",
			globalBase: "release",
			resolved:   repos("main", "develop"),
			want:       "release",
		},
		{
			name:     "uniform defaults show the shared branch",
			resolved: repos("develop", "develop"),
			want:     "develop",
		},
		{
			name:     "divergent defaults describe the rule",
			resolved: repos("main", "develop"),
			want:     mixedBasePlaceholder,
		},
		{
			// Unresolved repos all fall back to the same literal, so
			// they still count as uniform.
			name:     "unresolved defaults collapse to the fallback",
			resolved: repos("", ""),
			want:     defaultBaseBranch,
		},
		{
			name:     "no repos",
			resolved: nil,
			want:     defaultBaseBranch,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := basePlaceholder(tt.globalBase, tt.resolved); got != tt.want {
				t.Errorf("basePlaceholder() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWritableRepos(t *testing.T) {
	resolved := []repoContext{
		{repo: Repository{Name: "web"}, include: true},
		{repo: Repository{Name: "api"}, include: true},
		{repo: Repository{Name: "docs"}},                               // not selected
		{repo: Repository{Name: "ops"}, include: true, prErr: errNope}, // lookup failed
	}
	got := writableRepos(resolved)
	var names []string
	for _, i := range got {
		names = append(names, resolved[i].repo.Name)
	}
	if !reflect.DeepEqual(names, []string{"api", "web"}) {
		t.Errorf("writableRepos() = %v, want the selected, error-free repos alphabetically", names)
	}
}

func TestBasePlaceholderIgnoresUnwrittenRepos(t *testing.T) {
	// docs isn't selected, so its divergent default must not force the
	// prompt into the "(per repo default)" wording — every repo actually
	// being written to agrees on develop.
	resolved := []repoContext{
		{repo: Repository{Name: "api"}, include: true, defaultBranch: "develop"},
		{repo: Repository{Name: "web"}, include: true, defaultBranch: "develop"},
		{repo: Repository{Name: "docs"}, defaultBranch: "trunk"},
	}
	if got := basePlaceholder("", resolved); got != "develop" {
		t.Errorf("basePlaceholder() = %q, want %q", got, "develop")
	}
}

func TestFirstNonBlank(t *testing.T) {
	tests := []struct {
		name       string
		candidates []string
		want       string
	}{
		{"first wins", []string{"a", "b"}, "a"},
		{"blank falls through", []string{"   ", "b"}, "b"},
		{"empty falls through", []string{"", "", "c"}, "c"},
		{"trims the winner", []string{"  a  "}, "a"},
		{"all blank", []string{"", "  "}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := firstNonBlank(tt.candidates...); got != tt.want {
				t.Errorf("firstNonBlank(%q) = %q, want %q", tt.candidates, got, tt.want)
			}
		})
	}
}

func TestMultiSelectOver(t *testing.T) {
	t.Run("no options and no selection drops the field", func(t *testing.T) {
		var sel []string
		if f := multiSelectOver(nil, &sel, "Reviewers", ""); f != nil {
			t.Error("multiSelectOver() = non-nil, want nil for an empty picker")
		}
	})

	t.Run("preselected values missing from options are folded in", func(t *testing.T) {
		// alice comes from config (or another repo's collaborators) and
		// isn't a collaborator here — dropping her would silently
		// discard an answer the user already gave.
		sel := []string{"alice"}
		f := multiSelectOver([]string{"bob"}, &sel, "Reviewers", "")
		if f == nil {
			t.Fatal("multiSelectOver() = nil, want a field")
		}
		got := f.GetValue().([]string)
		if !reflect.DeepEqual(got, []string{"alice"}) {
			t.Errorf("bound value = %v, want the preselection preserved", got)
		}
	})
}

// TestRepoContextPrConfigNilReceiver covers the representative-repo
// hole: the shared prompt pass previews one repo's resolution, and
// there is no repo to preview when nothing in the workspace is
// writable. A nil receiver has to answer from the central layers rather
// than panic — the run still has prompts to show even with no PR to
// open.
func TestRepoContextPrConfigNilReceiver(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("pull_request.reviewers", []string{"alice"})

	var rc *repoContext
	if got := rc.prConfig().StringSlice("pull_request.reviewers"); !slices.Equal(got, []string{"alice"}) {
		t.Errorf("reviewers = %v, want the central list", got)
	}
}

// TestPinnedSelection covers when a shared multi-select speaks for
// EVERY repository and when it stays silent. Getting either half wrong
// is a quiet data-loss bug rather than a visible one: the run completes
// and opens its PRs, just with the wrong people on them.
func TestPinnedSelection(t *testing.T) {
	tests := []struct {
		name     string
		selected []string
		seed     []string
		want     []string // nil = did not pin
	}{
		{
			// typeaheadMultiSelect returns (nil, nil) when the fetch
			// yields no options — a token without the scope to list
			// collaborators, an org with no teams. No form rendered, so
			// nothing was answered. Pinning here would wipe every
			// repository's configured reviewers on the strength of a
			// prompt nobody saw.
			name: "no form rendered does not pin",
			seed: []string{"alice"},
			want: nil,
		},
		{
			// The seed is ONE representative repo's resolution.
			// Submitting it unchanged says nothing about the others, so
			// pinning it would push that repo's reviewers onto every
			// sibling — the fan-out this change exists to end.
			name:     "an unchanged submission does not pin",
			selected: []string{"alice"},
			seed:     []string{"alice"},
			want:     nil,
		},
		{
			// The seed comes from config order, the form returns fetch
			// order. A sequence comparison would read this as an edit
			// and pin on every single interactive run.
			name:     "reordering is not an edit",
			selected: []string{"bob", "alice"},
			seed:     []string{"alice", "bob"},
			want:     nil,
		},
		{
			name:     "an added name pins",
			selected: []string{"alice", "carol"},
			seed:     []string{"alice"},
			want:     []string{"alice", "carol"},
		},
		{
			// Clearing the list is a real answer, and the only one that
			// needs empty to survive as distinct from unanswered.
			name:     "clearing the list pins nothing, deliberately",
			selected: []string{},
			seed:     []string{"alice"},
			want:     []string{},
		},
		{
			name:     "an empty form over an empty seed does not pin",
			selected: []string{},
			seed:     nil,
			want:     nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pinnedSelection(tt.selected, tt.seed)
			if tt.want == nil {
				if got != nil {
					t.Errorf("pinnedSelection() = %#v, want nil (no pin)", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("pinnedSelection() = nil, want the pin %v", tt.want)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("pinnedSelection() = %v, want %v", got, tt.want)
			}
		})
	}
}
