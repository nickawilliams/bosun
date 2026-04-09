package ui

import (
	"fmt"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
)

var (
	tableHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(Palette.Primary).
				Padding(0, 1)

	tableCellStyle = lipgloss.NewStyle().Padding(0, 1)

	tableBorderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
)

// Column defines a table column.
type Column struct {
	Header string
}

// Table renders a styled table to stdout.
type Table struct {
	headers   []string
	rows      [][]string
	width     int
	styleFunc func(row, col int) lipgloss.Style
}

// NewTable creates a new table with the given columns.
func NewTable(columns ...Column) *Table {
	headers := make([]string, len(columns))
	for i, c := range columns {
		headers[i] = c.Header
	}
	return &Table{headers: headers}
}

// AddRow adds a row to the table. Values correspond to columns in order.
func (t *Table) AddRow(values ...string) *Table {
	t.rows = append(t.rows, values)
	return t
}

// SetWidth sets an explicit table width. 0 (default) uses terminal width.
func (t *Table) SetWidth(w int) *Table {
	t.width = w
	return t
}

// SetStyleFunc sets a custom style function for per-cell styling.
// The function receives row and col indices (row -1 = header).
func (t *Table) SetStyleFunc(fn func(row, col int) lipgloss.Style) *Table {
	t.styleFunc = fn
	return t
}

// Render prints the table to stdout.
func (t *Table) Render() {
	if len(t.rows) == 0 {
		return
	}

	w := t.width
	if w <= 0 {
		w = TermWidth()
	}

	sf := t.styleFunc
	if sf == nil {
		sf = func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return tableHeaderStyle
			}
			return tableCellStyle
		}
	}

	tbl := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(tableBorderStyle).
		Headers(t.headers...).
		Width(w).
		StyleFunc(sf)

	for _, row := range t.rows {
		tbl.Row(row...)
	}

	fmt.Println(tbl)
}
