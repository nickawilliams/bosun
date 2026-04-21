package cli

import (
	"testing"

	"github.com/spf13/viper"
)

func TestBuildBranchName(t *testing.T) {
	// Set up config for branch naming.
	viper.Reset()
	viper.Set("branch.template", "{{.Category}}/{{.IssueNumber}}_{{.IssueSlug}}")
	viper.Set("branch.categories.story", "feature")
	viper.Set("branch.categories.bug", "fix")
	t.Cleanup(func() { viper.Reset() })

	tests := []struct {
		name      string
		issueKey  string
		issueType string
		title     string
		slug      string
		want      string
	}{
		{
			name:      "story",
			issueKey:  "PROJ-123",
			issueType: "Story",
			title:     "Add widget endpoint",
			want:      "feature/PROJ-123_add-widget-endpoint",
		},
		{
			name:      "bug",
			issueKey:  "PROJ-456",
			issueType: "Bug",
			title:     "Fix broken auth",
			want:      "fix/PROJ-456_fix-broken-auth",
		},
		{
			name:      "unmapped type falls back to lowercase",
			issueKey:  "PROJ-789",
			issueType: "Task",
			title:     "Update docs",
			want:      "task/PROJ-789_update-docs",
		},
		{
			name:      "special characters in title",
			issueKey:  "CS-42",
			issueType: "Story",
			title:     "Add API endpoint for /users (v2)",
			want:      "feature/CS-42_add-api-endpoint-for-users-v2",
		},
		{
			name:      "custom slug overrides auto-generated",
			issueKey:  "PROJ-123",
			issueType: "Story",
			title:     "Add widget endpoint",
			slug:      "my-custom-slug",
			want:      "feature/PROJ-123_my-custom-slug",
		},
		{
			name:      "empty slug falls back to auto-generated",
			issueKey:  "PROJ-123",
			issueType: "Story",
			title:     "Add widget endpoint",
			slug:      "",
			want:      "feature/PROJ-123_add-widget-endpoint",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildBranchName(tt.issueKey, tt.issueType, tt.title, tt.slug)
			if err != nil {
				t.Fatalf("buildBranchName() error: %v", err)
			}
			if got != tt.want {
				t.Errorf("buildBranchName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBuildBranchNameDefaultPattern(t *testing.T) {
	viper.Reset()
	// No pattern configured — should use default.
	viper.Set("branch.categories.story", "feature")
	t.Cleanup(func() { viper.Reset() })

	got, err := buildBranchName("PROJ-1", "Story", "Test", "")
	if err != nil {
		t.Fatalf("buildBranchName() error: %v", err)
	}
	if got != "feature/PROJ-1_test" {
		t.Errorf("buildBranchName() = %q, want %q", got, "feature/PROJ-1_test")
	}
}

func TestSlugify(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"Add widget endpoint", "add-widget-endpoint"},
		{"Fix broken auth!", "fix-broken-auth"},
		{"  spaces  everywhere  ", "spaces-everywhere"},
		{"UPPERCASE TITLE", "uppercase-title"},
		{"special/chars&here", "special-chars-here"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := slugify(tt.input)
			if got != tt.want {
				t.Errorf("slugify(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
