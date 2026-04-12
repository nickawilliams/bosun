package code

import "testing"

func TestDeriveNextVersion(t *testing.T) {
	tests := []struct {
		current string
		bump    string
		want    string
		wantErr bool
	}{
		{"v1.2.3", "patch", "v1.2.4", false},
		{"v1.2.3", "minor", "v1.3.0", false},
		{"v1.2.3", "major", "v2.0.0", false},
		{"v0.0.0", "patch", "v0.0.1", false},
		{"v0.1.0", "minor", "v0.2.0", false},
		{"v1.0.0", "major", "v2.0.0", false},

		// No existing tag.
		{"", "patch", "v0.0.1", false},
		{"", "minor", "v0.1.0", false},
		{"", "major", "v1.0.0", false},
		{"", "", "v0.0.1", false}, // empty bump defaults to patch

		// With v prefix.
		{"v2.5.10", "patch", "v2.5.11", false},

		// Without v prefix.
		{"1.2.3", "patch", "v1.2.4", false},

		// Pre-release suffix stripped.
		{"v1.2.3-beta.1", "patch", "v1.2.4", false},

		// Errors.
		{"not-semver", "patch", "", true},
		{"v1.2", "patch", "", true},
		{"v1.2.3", "invalid", "", true},
	}

	for _, tt := range tests {
		name := tt.current + "+" + tt.bump
		if name == "+" {
			name = "empty"
		}
		t.Run(name, func(t *testing.T) {
			got, err := DeriveNextVersion(tt.current, tt.bump)
			if tt.wantErr {
				if err == nil {
					t.Errorf("DeriveNextVersion(%q, %q) expected error", tt.current, tt.bump)
				}
				return
			}
			if err != nil {
				t.Fatalf("DeriveNextVersion(%q, %q) error: %v", tt.current, tt.bump, err)
			}
			if got != tt.want {
				t.Errorf("DeriveNextVersion(%q, %q) = %q, want %q", tt.current, tt.bump, got, tt.want)
			}
		})
	}
}
