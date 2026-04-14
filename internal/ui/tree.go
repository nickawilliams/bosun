package ui

import (
	"fmt"
	"image/color"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
)

// TreeNode represents a node in a rendered tree. Leaf nodes carry a
// Value; group nodes carry Children. Both have a glyph that encodes
// semantic meaning (e.g., config source) via shape and color.
type TreeNode struct {
	Glyph      string
	GlyphColor color.Color
	Key        string
	Value      string      // empty for group/branch nodes
	Children   []*TreeNode // non-empty for group/branch nodes
}

const (
	// TreeGlyphGroup is the default glyph for group/branch nodes.
	TreeGlyphGroup = "○"
)

// Leaf creates a leaf node with a glyph, color, key, and value.
func Leaf(glyph string, glyphColor color.Color, key, value string) *TreeNode {
	return &TreeNode{
		Glyph:      glyph,
		GlyphColor: glyphColor,
		Key:        key,
		Value:      value,
	}
}

// Group creates a group node with the default group glyph (○ muted)
// and optional children.
func Group(key string, children ...*TreeNode) *TreeNode {
	return &TreeNode{
		Glyph:      TreeGlyphGroup,
		GlyphColor: Palette.Muted,
		Key:        key,
		Children:   children,
	}
}

// Tree renders a hierarchical structure using box-drawing characters.
// The tree integrates with the card timeline by rendering its
// connectors in the timeline column position.
type Tree struct {
	nodes []*TreeNode
}

// NewTree creates an empty tree.
func NewTree() *Tree {
	return &Tree{}
}

// Add appends root-level nodes to the tree.
func (t *Tree) Add(nodes ...*TreeNode) *Tree {
	t.nodes = append(t.nodes, nodes...)
	return t
}

// Render returns the tree as a styled multi-line string.
func (t *Tree) Render() string {
	if len(t.nodes) == 0 {
		return ""
	}

	// Pre-compute global alignment: max (depth * indentWidth + keyLen)
	// across all leaf nodes, so dots align across the entire tree.
	globalMax := maxEffectiveKeyWidth(t.nodes, 0)

	var b strings.Builder
	renderTreeNodes(&b, t.nodes, "", 0, globalMax)
	return b.String()
}

// Print writes the tree to stdout, integrating with the timeline.
// Consumes a pending comfy break and sets one for the next card.
func (t *Tree) Print() {
	fmt.Print(comfyPrefix() + t.Render())
	comfyBreak = true
}

const (
	treeBranch = "├── "
	treeLast   = "└── "
	treeDown   = "│   "
	treeBlank  = "    "

	// Each nesting level adds this many visual columns of indentation.
	treeIndentWidth = 4
)

// maxEffectiveKeyWidth walks the tree to find the widest
// (depth * indentWidth + keyLen) among all leaf nodes.
func maxEffectiveKeyWidth(nodes []*TreeNode, depth int) int {
	max := 0
	for _, n := range nodes {
		if n.Value != "" {
			w := depth*treeIndentWidth + len(n.Key)
			if w > max {
				max = w
			}
		}
		if len(n.Children) > 0 {
			if w := maxEffectiveKeyWidth(n.Children, depth+1); w > max {
				max = w
			}
		}
	}
	return max
}

func renderTreeNodes(b *strings.Builder, nodes []*TreeNode, indent string, depth, globalMax int) {
	connStyle := lipgloss.NewStyle().Foreground(Palette.Recessed)
	keyStyle := lipgloss.NewStyle().Foreground(Palette.Muted)
	dotStyle := lipgloss.NewStyle().Foreground(Palette.Muted)

	for i, node := range nodes {
		isLast := i == len(nodes)-1

		var branch string
		if isLast {
			branch = treeLast
		} else {
			branch = treeBranch
		}

		glyph := lipgloss.NewStyle().Foreground(node.GlyphColor).Render(node.Glyph)

		if node.Value != "" {
			// Pad key so the dot column aligns globally.
			padWidth := globalMax - depth*treeIndentWidth
			if padWidth < len(node.Key) {
				padWidth = len(node.Key)
			}
			paddedKey := fmt.Sprintf("%-*s", padWidth, node.Key)
			fmt.Fprintf(b, " %s%s%s %s %s %s\n",
				indent,
				connStyle.Render(branch),
				glyph,
				keyStyle.Render(paddedKey),
				dotStyle.Render(Palette.Dot),
				styledValue(node.Value),
			)
		} else {
			fmt.Fprintf(b, " %s%s%s %s\n",
				indent,
				connStyle.Render(branch),
				glyph,
				keyStyle.Render(node.Key),
			)
		}

		if len(node.Children) > 0 {
			var childIndent string
			if isLast {
				childIndent = indent + connStyle.Render(treeBlank)
			} else {
				childIndent = indent + connStyle.Render(treeDown)
			}
			renderTreeNodes(b, node.Children, childIndent, depth+1, globalMax)
		}
	}
}

// styledValue applies type-aware color to a config value string.
func styledValue(v string) string {
	switch {
	case strings.HasPrefix(v, "••"):
		// Masked secret.
		return lipgloss.NewStyle().Foreground(Palette.Muted).Render(v)
	case v == "true" || v == "false":
		return lipgloss.NewStyle().Foreground(Palette.Accent).Render(v)
	case isNumeric(v):
		return lipgloss.NewStyle().Foreground(Palette.Primary).Render(v)
	default:
		return lipgloss.NewStyle().Foreground(Palette.Success).Render(v)
	}
}

// isNumeric reports whether s looks like an integer or float.
func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}
