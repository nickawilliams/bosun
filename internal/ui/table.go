package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Column defines a table column.
type Column struct {
	Header string
	Width  int
}

// Table renders a simple styled table to stdout.
type Table struct {
	columns []Column
	rows    [][]string
}

// NewTable creates a new table with the given columns.
func NewTable(columns ...Column) *Table {
	return &Table{columns: columns}
}

// AddRow adds a row to the table. Values correspond to columns in order.
func (t *Table) AddRow(values ...string) {
	t.rows = append(t.rows, values)
}

// Render prints the table to stdout.
func (t *Table) Render() {
	if len(t.rows) == 0 {
		return
	}

	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(Theme.Muted)

	// Auto-calculate widths if not set.
	widths := make([]int, len(t.columns))
	for i, col := range t.columns {
		if col.Width > 0 {
			widths[i] = col.Width
		} else {
			widths[i] = len(col.Header)
			for _, row := range t.rows {
				if i < len(row) && len(row[i]) > widths[i] {
					widths[i] = len(row[i])
				}
			}
		}
	}

	// Print header.
	var header strings.Builder
	for i, col := range t.columns {
		fmt.Fprintf(&header, "  %-*s", widths[i]+2, col.Header)
	}
	fmt.Println(headerStyle.Render(header.String()))

	// Print rows.
	for _, row := range t.rows {
		var line strings.Builder
		for i := range t.columns {
			val := ""
			if i < len(row) {
				val = row[i]
			}
			fmt.Fprintf(&line, "  %-*s", widths[i]+2, val)
		}
		fmt.Println(line.String())
	}
}
