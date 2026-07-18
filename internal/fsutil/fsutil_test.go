package fsutil

import (
	"os"
	"testing"
)

func TestIgnorableName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{".DS_Store", true},
		{"._resourcefork", true},
		{".localized", true},
		{"Thumbs.db", true},
		{"desktop.ini", true},
		{".directory", true},
		// Real files / dirs must never be ignored — the guard depends on it.
		{"extracker", false},
		{".env", false},
		{"secrets.key", false},
		{"dump.sql", false},
		{".gitignore", false},
		{"notes.md", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IgnorableName(tt.name); got != tt.want {
				t.Errorf("IgnorableName(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

type fakeEntry struct{ os.DirEntry; name string }

func (f fakeEntry) Name() string { return f.name }

func TestHasMeaningfulEntries(t *testing.T) {
	mk := func(names ...string) []os.DirEntry {
		out := make([]os.DirEntry, len(names))
		for i, n := range names {
			out[i] = fakeEntry{name: n}
		}
		return out
	}
	tests := []struct {
		name    string
		entries []os.DirEntry
		want    bool
	}{
		{"empty", nil, false},
		{"junk only", mk(".DS_Store", "._x"), false},
		{"real file present", mk(".DS_Store", "config.yaml"), true},
		{"real dir present", mk("extracker"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasMeaningfulEntries(tt.entries); got != tt.want {
				t.Errorf("HasMeaningfulEntries = %v, want %v", got, tt.want)
			}
		})
	}
}
