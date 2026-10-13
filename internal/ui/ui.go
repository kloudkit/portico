package ui

import (
	"fmt"
	"strconv"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/kloudkit/portico/internal/routes"
)

var (
	Accent = lipgloss.NewStyle().Foreground(lipgloss.Color("#8caaee")).Bold(true)
	Faint  = lipgloss.NewStyle().Foreground(lipgloss.Color("#737994"))
	Good   = lipgloss.NewStyle().Foreground(lipgloss.Color("#a6d189"))
	Warn   = lipgloss.NewStyle().Foreground(lipgloss.Color("#e5c890"))
	Bad    = lipgloss.NewStyle().Foreground(lipgloss.Color("#e78284"))
)

func Log(format string, args ...any) {
	lipgloss.Println(Accent.Render("portico") + Faint.Render(" │ ") + fmt.Sprintf(format, args...))
}

func Routes(rs []routes.Route) string {
	t := table.New().
		Border(lipgloss.RoundedBorder()).
		BorderStyle(Faint).
		Headers("ROUTE", "SERVICE", "KIND", "PORT", "STRIP").
		StyleFunc(func(row, col int) lipgloss.Style {
			s := lipgloss.NewStyle().Padding(0, 1)
			if row == table.HeaderRow {
				return s.Inherit(Accent)
			}
			return s
		})

	for _, r := range rs {
		strip := ""
		if r.Strip && r.Path != "/" {
			strip = "✓"
		}
		t.Row(r.Path, r.Service, string(r.Kind), strconv.Itoa(r.Port), strip)
	}

	return t.String()
}
