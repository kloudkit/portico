package cmd

import (
	"strings"
	"time"

	"github.com/kloudkit/portico/internal/control"
	"github.com/kloudkit/portico/internal/supervisor"
	"github.com/kloudkit/portico/internal/ui"
	"github.com/spf13/cobra"
)

var downCmd = &cobra.Command{
	Use:   "down",
	Short: "Stop a running portico, or clean up what a crashed one left behind",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, err := locate(cmd)
		if err != nil {
			return err
		}

		client := control.NewClient(control.Socket(cfg.Project()))
		if client.Running() {
			if err := client.Stop(); err != nil {
				return err
			}
			for range 300 {
				if !client.Running() {
					ui.Log("stopped")
					return nil
				}
				time.Sleep(100 * time.Millisecond)
			}
			ui.Log("%s", ui.Warn.Render("still stopping after 30s"))
			return nil
		}

		if stopped := supervisor.Cleanup(cfg); len(stopped) > 0 {
			ui.Log("cleaned up %s", strings.Join(stopped, ", "))
		} else {
			ui.Log("nothing to clean up")
		}

		return nil
	},
}
