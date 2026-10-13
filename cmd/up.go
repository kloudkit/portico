package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"syscall"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/kloudkit/portico/internal/config"
	"github.com/kloudkit/portico/internal/proxy"
	"github.com/kloudkit/portico/internal/routes"
	"github.com/kloudkit/portico/internal/ui"
	"github.com/oklog/run"
	"github.com/spf13/cobra"
)

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Start the proxy and route to your services",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		cfg, rs, err := load(cmd)
		if err != nil {
			return err
		}
		if cfg.Proxy.Engine != "builtin" {
			return fmt.Errorf("proxy.engine: %s is not implemented yet", cfg.Proxy.Engine)
		}

		for _, r := range rs {
			if r.Kind != config.KindAttached {
				ui.Log("%s %s", r.Service, ui.Warn.Render(fmt.Sprintf("(%s) is not started by portico yet, expecting it on :%d", r.Kind, r.Port)))
			}
		}

		addr := net.JoinHostPort(cfg.Proxy.Host, strconv.Itoa(cfg.Proxy.Port))
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			return err
		}

		p := proxy.New(rs, cfg.Proxy.CORS)
		srv := &http.Server{Handler: p, ReadHeaderTimeout: 10 * time.Second}

		lipgloss.Println(ui.Routes(rs))
		ui.Log("listening on %s", ui.Accent.Render("http://"+addr))

		ctx, cancel := context.WithCancel(cmd.Context())
		var g run.Group

		g.Add(func() error {
			return srv.Serve(ln)
		}, func(error) {
			shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
			defer done()
			srv.Shutdown(shutdown)
		})

		g.Add(func() error {
			p.Watch(ctx, time.Second, func(r routes.Route, ready bool) {
				if ready {
					ui.Log("%s %s", r.Service, ui.Good.Render(fmt.Sprintf("ready on :%d", r.Port)))
				} else {
					ui.Log("%s %s", r.Service, ui.Bad.Render(fmt.Sprintf("down on :%d", r.Port)))
				}
			})
			return nil
		}, func(error) {
			cancel()
		})

		g.Add(run.SignalHandler(ctx, os.Interrupt, syscall.SIGTERM))

		err = g.Run()
		if errors.As(err, &run.SignalError{}) {
			ui.Log("stopped")
			return nil
		}

		return err
	},
}

func init() {
	addServiceFlags(upCmd)
}
