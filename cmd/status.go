package cmd

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/kloudkit/portico/internal/control"
	"github.com/kloudkit/portico/internal/ui"
	"github.com/spf13/cobra"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the state of every service of a running portico",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := locate(cmd)
		if err != nil {
			return err
		}

		services, err := control.NewClient(control.Socket(cfg.Project())).Status()
		if err != nil {
			return err
		}

		t := table.New().
			Border(lipgloss.RoundedBorder()).
			BorderStyle(ui.Faint).
			Headers("SERVICE", "KIND", "STATE", "ROUTES").
			StyleFunc(func(row, col int) lipgloss.Style {
				s := lipgloss.NewStyle().Padding(0, 1)
				if row == table.HeaderRow {
					return s.Inherit(ui.Accent)
				}
				return s
			})

		for _, svc := range services {
			var routes []string
			for _, r := range svc.Routes {
				routes = append(routes, fmt.Sprintf("%s → :%d", r.Path, r.Port))
			}
			t.Row(svc.Name, svc.Kind, state(svc), strings.Join(routes, "\n"))
		}

		lipgloss.Println(t.String())

		return nil
	},
}

func state(svc control.Service) string {
	switch svc.State {
	case "running", "up":
		return ui.Good.Render(svc.State)
	case "starting":
		return ui.Warn.Render(svc.State)
	case "exited":
		return ui.Bad.Render(fmt.Sprintf("exited (%d)", *svc.ExitCode))
	default:
		return ui.Bad.Render(svc.State)
	}
}
