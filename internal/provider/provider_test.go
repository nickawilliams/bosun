package provider

import "testing"

// TestScopeAllowsZeroValueIsCentral pins the decision the whole
// per-repo layer rests on: an unannotated key is central-only.
//
// It is the reason no separate "secrets are never repo-scoped" rule has
// to exist. If the zero value ever came to mean "any layer", every key
// nobody thought to annotate — which is most of them, including every
// provider's token — would silently become committable to a shared
// repository, and nothing else in the codebase would notice.
func TestScopeAllowsZeroValueIsCentral(t *testing.T) {
	var unset Scope

	for _, layer := range []Scope{ScopeGlobal, ScopeProject} {
		if !unset.Allows(layer) {
			t.Errorf("zero Scope denies %v — an unannotated key must still work centrally", layer)
		}
	}
	if unset.Allows(ScopeRepo) {
		t.Error("zero Scope allows ScopeRepo — a key reaches a repo descriptor only by asking to")
	}
}

// TestScopeEffective pins the one place the zero-value reading lives.
// Anything that REPORTS a scope (rather than testing one) goes through
// it, so a caller can print or compare the result without re-deriving
// the default and getting it subtly different.
func TestScopeEffective(t *testing.T) {
	if got := Scope(0).Effective(); got != ScopeCentral {
		t.Errorf("Scope(0).Effective() = %d, want ScopeCentral (%d)", got, ScopeCentral)
	}
	for _, s := range []Scope{ScopeGlobal, ScopeProject, ScopeRepo, ScopeAny} {
		if got := s.Effective(); got != s {
			t.Errorf("Scope(%d).Effective() = %d, want it unchanged", s, got)
		}
	}
}

func TestScopeAllows(t *testing.T) {
	tests := []struct {
		name  string
		scope Scope
		layer Scope
		want  bool
	}{
		{"central denies repo", ScopeCentral, ScopeRepo, false},
		{"central allows global", ScopeCentral, ScopeGlobal, true},
		{"central allows project", ScopeCentral, ScopeProject, true},
		{"any allows repo", ScopeAny, ScopeRepo, true},
		{"any allows global", ScopeAny, ScopeGlobal, true},
		{"repo-only denies project", ScopeRepo, ScopeProject, false},
		{"repo-only allows repo", ScopeRepo, ScopeRepo, true},
		{"single layer allows itself", ScopeGlobal, ScopeGlobal, true},
		{"single layer denies its sibling", ScopeGlobal, ScopeProject, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.scope.Allows(tt.layer); got != tt.want {
				t.Errorf("Allows() = %v, want %v", got, tt.want)
			}
		})
	}
}
