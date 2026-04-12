package ui

import (
	"fmt"
	"image/color"

	"charm.land/lipgloss/v2"
)

// Panel renders content inside a bordered box with an optional title.
type Panel struct {
	title   string
	content string
	width   int
}

// NewPanel creates a new panel with an optional title.
func NewPanel(title string) *Panel {
	return &Panel{title: title}
}

// Content sets the panel body.
func (p *Panel) Content(s string) *Panel {
	p.content = s
	return p
}

// Width sets an explicit width. 0 (default) uses terminal width.
func (p *Panel) Width(w int) *Panel {
	p.width = w
	return p
}

// Render returns the panel as a styled string.
func (p *Panel) Render() string {
	w := p.width
	if w <= 0 {
		w = TermWidth() - 4
		if w > 72 {
			w = 72
		}
	}

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(Palette.Primary)

	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(Palette.Border).
		Padding(1, 2).
		Width(w)

	if p.title != "" {
		style = style.BorderTop(true)
		rendered := style.Render(p.content)
		// Inject the title into the top border.
		title := " " + titleStyle.Render(p.title) + " "
		return injectTitle(rendered, title, Palette.Border)
	}

	return style.Render(p.content)
}

// Print writes the panel to stdout.
func (p *Panel) Print() {
	fmt.Println(p.Render())
}

// injectTitle replaces part of the first line (the top border) with a title.
func injectTitle(rendered, title string, borderColor color.Color) string {
	lines := splitLines(rendered)
	if len(lines) == 0 {
		return rendered
	}

	// The first line is the top border. Replace characters after the
	// opening corner with the title.
	topBorder := lines[0]
	borderStyle := lipgloss.NewStyle().Foreground(borderColor)

	// Build: corner + dash + title + remaining dashes + corner
	// We need to account for ANSI codes in the original border.
	// Simpler approach: rebuild the top border entirely.
	runes := []rune(stripAnsi(topBorder))
	if len(runes) < 4 {
		return rendered
	}

	corner := string(runes[0])
	endCorner := string(runes[len(runes)-1])
	innerWidth := len(runes) - 2

	// Title visible length (without ANSI).
	titleLen := len([]rune(stripAnsi(title)))
	if titleLen > innerWidth-2 {
		// Title too long, skip it.
		return rendered
	}

	dashChar := "─"
	leftDash := dashChar
	rightDashes := ""
	remaining := innerWidth - 1 - titleLen
	for range remaining {
		rightDashes += dashChar
	}

	newTop := borderStyle.Render(corner+leftDash) + title + borderStyle.Render(rightDashes+endCorner)
	lines[0] = newTop

	result := ""
	for i, line := range lines {
		if i > 0 {
			result += "\n"
		}
		result += line
	}
	return result
}

// splitLines splits a string into lines without using strings package.
func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// stripAnsi removes ANSI escape sequences from a string.
func stripAnsi(s string) string {
	var result []byte
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			// Skip until we hit the terminator (a letter).
			j := i + 2
			for j < len(s) && !((s[j] >= 'A' && s[j] <= 'Z') || (s[j] >= 'a' && s[j] <= 'z')) {
				j++
			}
			if j < len(s) {
				j++ // skip the terminator
			}
			i = j
		} else {
			result = append(result, s[i])
			i++
		}
	}
	return string(result)
}
