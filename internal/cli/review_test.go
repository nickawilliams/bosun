package cli

import (
	"reflect"
	"testing"

	"github.com/nickawilliams/bosun/internal/code"
)

// rc builds a repoContext with a PR in the given state for classification tests.
func rc(state string, number int) repoContext {
	return repoContext{pr: code.PullRequest{Number: number, State: state}}
}

func TestRepoContextClassification(t *testing.T) {
	tests := []struct {
		name                               string
		rc                                 repoContext
		wantActive, wantCreatable, wantPre bool
	}{
		{"no pr", repoContext{}, false, true, true},
		{"open pr", rc("open", 7), true, false, false},
		{"draft pr", rc("draft", 7), true, false, false},
		{"closed pr", rc("closed", 7), false, true, true},
		{"merged pr", rc("merged", 7), false, true, false},
		{"lookup error", repoContext{prErr: errNope}, false, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.rc.activePR(); got != tt.wantActive {
				t.Errorf("activePR() = %v, want %v", got, tt.wantActive)
			}
			if got := tt.rc.creatable(); got != tt.wantCreatable {
				t.Errorf("creatable() = %v, want %v", got, tt.wantCreatable)
			}
			if got := tt.rc.preselect(); got != tt.wantPre {
				t.Errorf("preselect() = %v, want %v", got, tt.wantPre)
			}
		})
	}
}

var errNope = errStub("lookup failed")

type errStub string

func (e errStub) Error() string { return string(e) }

func TestPlanSync(t *testing.T) {
	existing := code.PullRequest{
		Number:             7,
		Title:              "Old title",
		Body:               "Old body",
		BaseRef:            "main",
		RequestedReviewers: []string{"alice", "bob"},
		RequestedTeams:     []string{"backend"},
		Assignees:          []string{"alice"},
	}

	t.Run("reconciles adds and removes", func(t *testing.T) {
		// Want carol+alice as reviewers (drop bob), no teams (drop backend),
		// assignee dave (drop alice).
		s := planSync(existing,
			[]string{"carol", "alice"}, nil, []string{"dave"},
			"Old title", "Old body", "main")

		if !reflect.DeepEqual(s.addRevs, []string{"carol"}) {
			t.Errorf("addRevs = %v", s.addRevs)
		}
		if !reflect.DeepEqual(s.removeRevs, []string{"bob"}) {
			t.Errorf("removeRevs = %v", s.removeRevs)
		}
		if !reflect.DeepEqual(s.removeTeams, []string{"backend"}) {
			t.Errorf("removeTeams = %v", s.removeTeams)
		}
		if !reflect.DeepEqual(s.addAsns, []string{"dave"}) {
			t.Errorf("addAsns = %v", s.addAsns)
		}
		if !reflect.DeepEqual(s.removeAsns, []string{"alice"}) {
			t.Errorf("removeAsns = %v", s.removeAsns)
		}
		if s.contentChanged {
			t.Errorf("contentChanged = true, want false (title/body/base unchanged)")
		}
		if !s.hasChanges() {
			t.Errorf("hasChanges() = false, want true")
		}
	})

	t.Run("completed reviewers are not re-requested", func(t *testing.T) {
		// carol reviewed already, so GitHub dropped her from the
		// pending list; wanting her must NOT re-request (which would
		// reset her review). Mixed case exercises the fold.
		reviewed := existing
		reviewed.RequestedReviewers = []string{"alice"}
		reviewed.ReviewedBy = []string{"Carol"}
		s := planSync(reviewed,
			[]string{"alice", "carol", "dave"}, nil, nil,
			"Old title", "Old body", "main")

		if !reflect.DeepEqual(s.addRevs, []string{"dave"}) {
			t.Errorf("addRevs = %v, want only the never-asked dave", s.addRevs)
		}
	})

	t.Run("detects content change", func(t *testing.T) {
		s := planSync(existing,
			[]string{"alice", "bob"}, []string{"backend"}, []string{"alice"},
			"New title", "Old body", "main")
		if !s.contentChanged {
			t.Errorf("contentChanged = false, want true (title differs)")
		}
		if !s.hasChanges() {
			t.Errorf("hasChanges() = false, want true")
		}
	})

	t.Run("base retarget is a content change", func(t *testing.T) {
		s := planSync(existing,
			[]string{"alice", "bob"}, []string{"backend"}, []string{"alice"},
			"Old title", "Old body", "develop")
		if !s.contentChanged {
			t.Errorf("contentChanged = false, want true (base differs)")
		}
	})

	t.Run("no change when already in sync", func(t *testing.T) {
		s := planSync(existing,
			[]string{"alice", "bob"}, []string{"backend"}, []string{"alice"},
			"Old title", "Old body", "main")
		if s.hasChanges() {
			t.Errorf("hasChanges() = true, want false (PR already matches request)")
		}
	})
}

func TestSyncSummary(t *testing.T) {
	tests := []struct {
		name string
		s    *prState
		want string
	}{
		{
			name: "content only",
			s:    &prState{contentChanged: true},
			want: "content",
		},
		{
			name: "reviewers added and removed",
			s:    &prState{addRevs: []string{"a"}, removeRevs: []string{"b", "c"}},
			want: "+1/-2 rev",
		},
		{
			name: "assignees added only",
			s:    &prState{addAsns: []string{"a"}},
			want: "+1 asn",
		},
		{
			name: "teams count toward reviewers",
			s:    &prState{addTeams: []string{"t"}, removeRevs: []string{"b"}},
			want: "+1/-1 rev",
		},
		{
			name: "everything",
			s:    &prState{contentChanged: true, addRevs: []string{"a"}, removeAsns: []string{"x"}},
			want: "content +1 rev -1 asn",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := syncSummary(tt.s); got != tt.want {
				t.Errorf("syncSummary() = %q, want %q", got, tt.want)
			}
		})
	}
}
