package cmd

import (
	"github.com/kloudkit/portico/internal/control"
	"github.com/kloudkit/portico/internal/ui"
	"github.com/spf13/cobra"
)

var restartCmd = &cobra.Command{
	Use:   "restart <service>...",
	Short: "Restart services of a running portico",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := locate(cmd)
		if err != nil {
			return err
		}

		client := control.NewClient(control.Socket(cfg.Project()))
		for _, name := range args {
			if err := client.Restart(name); err != nil {
				return err
			}
			ui.Log("%s %s", name, ui.Good.Render("restarting"))
		}

		return nil
	},
}
