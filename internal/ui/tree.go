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
	ValueColor color.Color // nil = use the styledValue heuristic
	Children   []*TreeNode // non-empty for group/branch nodes
}

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
		Glyph:      Palette.Inactive,
		GlyphColor: Palette.Muted,
		Key:        key,
		Children:   children,
	}
}

// Tree renders a hierarchical structure using box-drawing characters.
// The tree integrates with the card timeline by rendering its
// connectors in the timeline column position.
type Tree struct {
	nodes          []*TreeNode
	valueColumn    int  // 0 = auto from widest leaf; >0 = align values at this column
	continuesBelow bool // outermost last node renders with ├── instead of └──
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

// IsEmpty reports whether the tree has no root-level nodes.
func (t *Tree) IsEmpty() bool { return len(t.nodes) == 0 }

// ContinuesBelow signals that the tree is part of a larger timeline
// segment — the outermost last node renders with the ├── tee instead
// of the └── terminator, and the spine continues down (│) past its
// children. Use when the row below the tree is another card in the
// timeline (a summary card, etc.). Default (terminator) shape is
// correct for self-contained trees whose footer is a legend / key
// rather than a continuing card.
func (t *Tree) ContinuesBelow() *Tree {
	t.continuesBelow = true
	return t
}

// ValueColumn requests that leaf values start at the given 1-indexed
// terminal column. The natural auto-pad still applies as a floor —
// values are pushed right past the requested column when a key is
// too long to fit, never truncated. Zero (the default) keeps the
// pure auto-pad behavior.
func (t *Tree) ValueColumn(col int) *Tree {
	t.valueColumn = col
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
	// If the caller requested a target column, expand globalMax so
	// values land at (or past) it. 11 accounts for the fixed visible
	// columns before the padded key in renderTreeNodes' format:
	// " " + branch(4) + glyph(1) + " " + key + " " + dot + " " + value.
	if t.valueColumn > 0 {
		globalMax = max(globalMax, t.valueColumn-11)
	}

	var b strings.Builder
	renderTreeNodes(&b, t.nodes, "", 0, globalMax, t.continuesBelow)
	return b.String()
}

// Print writes the tree to stdout, integrating with the timeline.
// Consumes a pending comfy break and sets one for the next card.
func (t *Tree) Print() {
	fmt.Print(spacerPrefix() + t.Render())
}

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

// renderTreeNodes recursively renders the tree. lastIsContinuation
// applies only at this call's level: when true, the last node uses
// the ├── tee + treeDown child-indent so the timeline visually
// continues into whatever follows. Recursive calls into a node's
// children always pass false — the continuation flag only governs
// the outermost last branch.
func renderTreeNodes(b *strings.Builder, nodes []*TreeNode, indent string, depth, globalMax int, lastIsContinuation bool) {
	connStyle := lipgloss.NewStyle().Foreground(Palette.Recessed)
	keyStyle := lipgloss.NewStyle().Foreground(Palette.Muted)
	dotStyle := lipgloss.NewStyle().Foreground(Palette.Muted)

	for i, node := range nodes {
		isLast := i == len(nodes)-1
		terminates := isLast && !lastIsContinuation

		branch := treeBranch
		if terminates {
			branch = treeLast
		}

		glyph := lipgloss.NewStyle().Foreground(node.GlyphColor).Render(node.Glyph)

		if node.Value != "" {
			// Pad key so the dot column aligns globally.
			padWidth := max(globalMax-depth*treeIndentWidth, len(node.Key))
			paddedKey := fmt.Sprintf("%-*s", padWidth, node.Key)
			value := styledValue(node.Value)
			if node.ValueColor != nil {
				value = lipgloss.NewStyle().Foreground(node.ValueColor).Render(node.Value)
			}
			fmt.Fprintf(b, " %s%s%s %s %s %s\n",
				indent,
				connStyle.Render(branch),
				glyph,
				keyStyle.Render(paddedKey),
				dotStyle.Render(Palette.Dot),
				value,
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
			childIndent := indent + connStyle.Render(treeDown)
			if terminates {
				childIndent = indent + connStyle.Render(treeBlank)
			}
			renderTreeNodes(b, node.Children, childIndent, depth+1, globalMax, false)
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
