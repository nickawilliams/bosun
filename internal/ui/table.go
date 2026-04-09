package ui

import (
	"fmt"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
)

var (
	tableHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("99")).
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
	headers []string
	rows    [][]string
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
func (t *Table) AddRow(values ...string) {
	t.rows = append(t.rows, values)
}

// Render prints the table to stdout.
func (t *Table) Render() {
	if len(t.rows) == 0 {
		return
	}

	tbl := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(tableBorderStyle).
		Headers(t.headers...).
		StyleFunc(func(row, col int) lipgloss.Style {
			if row == table.HeaderRow {
				return tableHeaderStyle
			}
			return tableCellStyle
		})

	for _, row := range t.rows {
		tbl.Row(row...)
	}

	fmt.Println(tbl)
}
