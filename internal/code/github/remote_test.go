package github

import "testing"

func TestParseRemoteURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		owner   string
		repo    string
		wantErr bool
	}{
		{"ssh", "git@github.com:myorg/my-service.git", "myorg", "my-service", false},
		{"ssh no .git", "git@github.com:myorg/my-service", "myorg", "my-service", false},
		{"https", "https://github.com/myorg/my-service.git", "myorg", "my-service", false},
		{"https no .git", "https://github.com/myorg/my-service", "myorg", "my-service", false},
		{"http", "http://github.com/myorg/my-service.git", "myorg", "my-service", false},
		{"gitlab ssh", "git@gitlab.com:myorg/my-service.git", "myorg", "my-service", false},
		{"gitlab https", "https://gitlab.com/myorg/my-service.git", "myorg", "my-service", false},
		{"invalid", "not-a-url", "", "", true},
		{"empty", "", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseRemoteURL(tt.url)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseRemoteURL(%q) expected error", tt.url)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseRemoteURL(%q) error: %v", tt.url, err)
			}
			if got.Owner != tt.owner {
				t.Errorf("Owner = %q, want %q", got.Owner, tt.owner)
			}
			if got.Name != tt.repo {
				t.Errorf("Name = %q, want %q", got.Name, tt.repo)
			}
		})
	}
}
