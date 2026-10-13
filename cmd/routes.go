package cmd

import (
	"fmt"

	"charm.land/lipgloss/v2"
	"github.com/kloudkit/portico/internal/ui"
	"github.com/spf13/cobra"
)

var routesCmd = &cobra.Command{
	Use:   "routes",
	Short: "Print the resolved route table without starting anything",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, rs, err := load(cmd, nil)
		if err != nil {
			return err
		}

		lipgloss.Println(ui.Faint.Render(fmt.Sprintf("%s engine on %s:%d", cfg.Proxy.Engine, cfg.Proxy.Host, cfg.Proxy.Port)))
		lipgloss.Println(ui.Routes(rs))

		return nil
	},
}

func init() {
	addServiceFlags(routesCmd)
}
