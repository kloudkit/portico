package cmd

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
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

		status, err := control.NewClient(control.Socket(cfg.Project())).Status()
		if err != nil {
			return err
		}

		t := ui.Table("SERVICE", "KIND", "STATE", "ROUTES")
		for _, svc := range status.Services {
			var routes []string
			for _, r := range svc.Routes {
				routes = append(routes, fmt.Sprintf("%s → :%d", r.Path, r.Port))
			}
			t.Row(svc.Name, svc.Kind, ui.State(svc), strings.Join(routes, "\n"))
		}

		if status.URL != "" {
			lipgloss.Println(ui.Faint.Render("reachable at ") + ui.Accent.Render(status.URL))
		}
		lipgloss.Println(t.String())

		return nil
	},
}
