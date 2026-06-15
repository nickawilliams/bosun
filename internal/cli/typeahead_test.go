package cli

import (
	"reflect"
	"testing"
)

func TestWithoutUser(t *testing.T) {
	tests := []struct {
		name  string
		items []string
		user  string
		want  []string
	}{
		{
			name:  "removes exact match",
			items: []string{"alice", "bob", "carol"},
			user:  "bob",
			want:  []string{"alice", "carol"},
		},
		{
			name:  "removes case-insensitively",
			items: []string{"Alice", "Bob"},
			user:  "alice",
			want:  []string{"Bob"},
		},
		{
			name:  "absent user is a no-op",
			items: []string{"alice", "bob"},
			user:  "dave",
			want:  []string{"alice", "bob"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := withoutUser(tt.items, tt.user); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("withoutUser() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestUserToFront(t *testing.T) {
	tests := []struct {
		name  string
		items []string
		user  string
		want  []string
	}{
		{
			name:  "moves present user to front",
			items: []string{"alice", "bob", "carol"},
			user:  "carol",
			want:  []string{"carol", "alice", "bob"},
		},
		{
			name:  "keeps canonical casing from items",
			items: []string{"alice", "Bob"},
			user:  "bob",
			want:  []string{"Bob", "alice"},
		},
		{
			name:  "prepends absent user",
			items: []string{"alice", "bob"},
			user:  "dave",
			want:  []string{"dave", "alice", "bob"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := userToFront(tt.items, tt.user); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("userToFront() = %v, want %v", got, tt.want)
			}
		})
	}
}
