package cli

import (
	"reflect"
	"testing"
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
	repos := func(branches ...string) []repoContext {
		out := make([]repoContext, len(branches))
		for i, b := range branches {
			out[i] = repoContext{defaultBranch: b}
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
