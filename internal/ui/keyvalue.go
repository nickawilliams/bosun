package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
)

// KV renders aligned key-value pairs.
type KV struct {
	pairs []kvPair
}

type kvPair struct {
	key   string
	value string
}

// NewKV creates a new key-value display.
func NewKV() *KV {
	return &KV{}
}

// Add appends a key-value pair.
func (kv *KV) Add(key, value string) *KV {
	kv.pairs = append(kv.pairs, kvPair{key, value})
	return kv
}

// Render returns the formatted key-value block as a string.
func (kv *KV) Render() string {
	if len(kv.pairs) == 0 {
		return ""
	}

	keyStyle := lipgloss.NewStyle().Foreground(Palette.Muted)
	valStyle := lipgloss.NewStyle().Foreground(Palette.NormalFg)
	dotStyle := lipgloss.NewStyle().Foreground(Palette.Muted)

	// Find max key width for alignment.
	maxKey := 0
	for _, p := range kv.pairs {
		if len(p.key) > maxKey {
			maxKey = len(p.key)
		}
	}

	var b strings.Builder
	for _, p := range kv.pairs {
		paddedKey := fmt.Sprintf("%-*s", maxKey, p.key)
		fmt.Fprintf(&b, "  %s %s %s\n",
			keyStyle.Render(paddedKey),
			dotStyle.Render(Palette.Dot),
			valStyle.Render(p.value),
		)
	}

	return b.String()
}

// Print writes the formatted key-value block to stdout.
func (kv *KV) Print() {
	fmt.Print(kv.Render())
}
