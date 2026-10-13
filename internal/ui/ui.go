package ui

import (
	"strconv"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/kloudkit/portico/internal/routes"
)

var (
	Accent = lipgloss.NewStyle().Foreground(Teal).Bold(true)
	Faint  = lipgloss.NewStyle().Foreground(Overlay0)
	Good   = lipgloss.NewStyle().Foreground(Green)
	Warn   = lipgloss.NewStyle().Foreground(Yellow)
	Bad    = lipgloss.NewStyle().Foreground(Red)
)

// Table is the style every table shares.
func Table(headers ...string) *table.Table {
	return table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(Faint).
		Headers(headers...).
		StyleFunc(func(row, col int) lipgloss.Style {
			s := lipgloss.NewStyle().Padding(0, 1)
			if row == table.HeaderRow {
				return s.Inherit(Accent)
			}
			return s
		})
}

func Routes(rs []routes.Route) string {
	t := Table("ROUTE", "SERVICE", "KIND", "PORT", "STRIP")
	for _, r := range rs {
		strip := ""
		if r.Strip && r.Path != "/" {
			strip = "✓"
		}
		t.Row(r.Path, r.Service, string(r.Kind), strconv.Itoa(r.Port), strip)
	}

	return t.String()
}
