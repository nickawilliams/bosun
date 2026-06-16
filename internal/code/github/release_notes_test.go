package github

import "testing"

func TestPrettifyReleaseNotes(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "PR URL becomes #N link",
			in:   "Fix by @alice in https://github.com/org/repo/pull/42",
			want: "Fix by [@alice](https://github.com/alice) in [#42](https://github.com/org/repo/pull/42)",
		},
		{
			name: "issue URL becomes #N link",
			in:   "Closes https://github.com/org/repo/issues/7",
			want: "Closes [#7](https://github.com/org/repo/issues/7)",
		},
		{
			name: "compare URL becomes range link",
			in:   "**Full Changelog**: https://github.com/org/repo/compare/v1.0.0...v1.1.0",
			want: "**Full Changelog**: [v1.0.0...v1.1.0](https://github.com/org/repo/compare/v1.0.0...v1.1.0)",
		},
		{
			name: "release-notes shape end-to-end",
			in:   "## What's Changed\n* EX-1: foo by @alice in https://github.com/org/repo/pull/1\n* bar by @bob in https://github.com/org/repo/pull/2\n\n**Full Changelog**: https://github.com/org/repo/compare/v1...v2",
			want: "What's Changed\n* EX-1: foo by [@alice](https://github.com/alice) in [#1](https://github.com/org/repo/pull/1)\n* bar by [@bob](https://github.com/bob) in [#2](https://github.com/org/repo/pull/2)\n\n**Full Changelog**: [v1...v2](https://github.com/org/repo/compare/v1...v2)",
		},
		{
			name: "heading marker stripped, text preserved",
			in:   "## What's Changed",
			want: "What's Changed",
		},
		{
			name: "all heading levels stripped",
			in:   "# h1\n## h2\n### h3\n#### h4\n##### h5\n###### h6",
			want: "h1\nh2\nh3\nh4\nh5\nh6",
		},
		{
			name: "inline hash reference left alone",
			in:   "fixes #42 in the PR",
			want: "fixes #42 in the PR",
		},
		{
			name: "hash at line start without space left alone",
			in:   "#42 was the PR number",
			want: "#42 was the PR number",
		},
		{
			name: "email not matched as mention",
			in:   "contact user@example.com",
			want: "contact user@example.com",
		},
		{
			name: "mention inside URL not double-matched",
			in:   "see https://api.github.com/users/x@y for details",
			want: "see https://api.github.com/users/x@y for details",
		},
		{
			name: "URL already wrapped in markdown link is untouched",
			in:   "see [#42](https://github.com/org/repo/pull/42) for the fix",
			want: "see [#42](https://github.com/org/repo/pull/42) for the fix",
		},
		{
			name: "mention at start of line",
			in:   "@alice noticed this",
			want: "[@alice](https://github.com/alice) noticed this",
		},
		{
			name: "PR URL at start of line",
			in:   "https://github.com/org/repo/pull/99 is the fix",
			want: "[#99](https://github.com/org/repo/pull/99) is the fix",
		},
		{
			name: "non-github URL untouched",
			in:   "see https://example.com/something for context",
			want: "see https://example.com/something for context",
		},
		{
			name: "blob URL untouched (not pull/issues/compare)",
			in:   "in https://github.com/org/repo/blob/main/README.md",
			want: "in https://github.com/org/repo/blob/main/README.md",
		},
		{
			name: "mention with hyphen and digits",
			in:   "thanks @christopher-rauch and @user123",
			want: "thanks [@christopher-rauch](https://github.com/christopher-rauch) and [@user123](https://github.com/user123)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PrettifyReleaseNotes(tt.in); got != tt.want {
				t.Errorf("\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
}
