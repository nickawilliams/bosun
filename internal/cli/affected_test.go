package cli

import (
	"testing"
)

func TestAnyPathMatches(t *testing.T) {
	tests := []struct {
		name     string
		changed  []string
		prefixes []string
		want     bool
	}{
		{
			name:     "directory prefix match",
			changed:  []string{"cmd/api/activity/handler.go"},
			prefixes: []string{"cmd/api/activity/"},
			want:     true,
		},
		{
			name:     "exact file match",
			changed:  []string{"go.mod"},
			prefixes: []string{"go.mod"},
			want:     true,
		},
		{
			name:     "no match",
			changed:  []string{"cmd/worker/main.go"},
			prefixes: []string{"cmd/api/activity/"},
			want:     false,
		},
		{
			name:     "prefix without trailing slash requires exact match",
			changed:  []string{"go.modx"},
			prefixes: []string{"go.mod"},
			want:     false,
		},
		{
			name:     "multiple changed files one matches",
			changed:  []string{"README.md", "cmd/api/activity/handler.go"},
			prefixes: []string{"cmd/api/activity/"},
			want:     true,
		},
		{
			name:     "multiple prefixes one matches",
			changed:  []string{"pkg/shared/util.go"},
			prefixes: []string{"cmd/api/", "pkg/shared/"},
			want:     true,
		},
		{
			name:     "empty changed files",
			changed:  nil,
			prefixes: []string{"cmd/api/"},
			want:     false,
		},
		{
			name:     "empty prefixes",
			changed:  []string{"cmd/api/main.go"},
			prefixes: nil,
			want:     false,
		},
		{
			name:     "nested directory match",
			changed:  []string{"pkg/auth/jwt/token.go"},
			prefixes: []string{"pkg/"},
			want:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := anyPathMatches(tt.changed, tt.prefixes)
			if got != tt.want {
				t.Errorf("anyPathMatches(%v, %v) = %v, want %v",
					tt.changed, tt.prefixes, got, tt.want)
			}
		})
	}
}

func TestMatchServicePaths(t *testing.T) {
	services := []string{"activity-api", "admin-api", "worker"}
	pathMap := map[string][]string{
		"activity-api": {"cmd/api/activity/"},
		"admin-api":    {"cmd/api/admin/"},
		"worker":       {"cmd/worker/"},
		"_shared":      {"go.mod", "go.sum", "pkg/"},
	}

	t.Run("single service affected", func(t *testing.T) {
		changed := []string{"cmd/api/activity/handler.go", "cmd/api/activity/routes.go"}
		result := matchServicePaths("extracker", services, changed, pathMap)

		if !result.HasChanges {
			t.Error("HasChanges should be true")
		}
		if len(result.Services) != 1 || result.Services[0] != "activity-api" {
			t.Errorf("Services = %v, want [activity-api]", result.Services)
		}
		if len(result.Skipped) != 2 {
			t.Errorf("Skipped = %v, want 2 entries", result.Skipped)
		}
	})

	t.Run("shared trigger includes all", func(t *testing.T) {
		changed := []string{"go.mod"}
		result := matchServicePaths("extracker", services, changed, pathMap)

		if !result.HasChanges {
			t.Error("HasChanges should be true")
		}
		if len(result.Services) != 3 {
			t.Errorf("Services = %v, want all 3", result.Services)
		}
		if len(result.Skipped) != 0 {
			t.Errorf("Skipped = %v, want empty", result.Skipped)
		}
	})

	t.Run("shared pkg prefix includes all", func(t *testing.T) {
		changed := []string{"pkg/auth/token.go"}
		result := matchServicePaths("extracker", services, changed, pathMap)

		if !result.HasChanges {
			t.Error("HasChanges should be true")
		}
		if len(result.Services) != 3 {
			t.Errorf("Services = %v, want all 3", result.Services)
		}
	})

	t.Run("no matching paths", func(t *testing.T) {
		changed := []string{"README.md", ".github/workflows/ci.yml"}
		result := matchServicePaths("extracker", services, changed, pathMap)

		if result.HasChanges {
			t.Error("HasChanges should be false")
		}
		if len(result.Services) != 0 {
			t.Errorf("Services = %v, want empty", result.Services)
		}
		if len(result.Skipped) != 3 {
			t.Errorf("Skipped = %v, want all 3", result.Skipped)
		}
	})

	t.Run("multiple services affected", func(t *testing.T) {
		changed := []string{"cmd/api/activity/handler.go", "cmd/worker/main.go"}
		result := matchServicePaths("extracker", services, changed, pathMap)

		if !result.HasChanges {
			t.Error("HasChanges should be true")
		}
		if len(result.Services) != 2 {
			t.Errorf("Services = %v, want 2", result.Services)
		}
		if len(result.Skipped) != 1 {
			t.Errorf("Skipped = %v, want 1", result.Skipped)
		}
	})

	t.Run("service without path config included conservatively", func(t *testing.T) {
		servicesWithExtra := []string{"activity-api", "admin-api", "worker", "unmapped-svc"}
		changed := []string{"cmd/api/activity/handler.go"}
		result := matchServicePaths("extracker", servicesWithExtra, changed, pathMap)

		// unmapped-svc has no entry in pathMap → included conservatively.
		found := false
		for _, s := range result.Services {
			if s == "unmapped-svc" {
				found = true
			}
		}
		if !found {
			t.Errorf("Services = %v, want unmapped-svc included", result.Services)
		}
	})
}
