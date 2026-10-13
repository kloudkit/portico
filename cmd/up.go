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
	"github.com/charmbracelet/x/term"
	"github.com/kloudkit/portico/internal/config"
	"github.com/kloudkit/portico/internal/control"
	"github.com/kloudkit/portico/internal/proxy"
	"github.com/kloudkit/portico/internal/routes"
	"github.com/kloudkit/portico/internal/supervisor"
	"github.com/kloudkit/portico/internal/tui"
	"github.com/kloudkit/portico/internal/ui"
	"github.com/oklog/run"
	"github.com/spf13/cobra"
)

var upCmd = &cobra.Command{
	Use:   "up [service...]",
	Short: "Start your services and the proxy in front of them",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, rs, err := load(cmd, nil)
		if err != nil {
			return err
		}

		f := cmd.Flags()
		ui.Raw, _ = f.GetBool("raw")
		ui.Timestamps, _ = f.GetBool("timestamps")
		hide, _ := f.GetStringSlice("hide")
		noLogs, _ := f.GetBool("no-logs")
		dashboard, _ := f.GetBool("tui")
		watch, _ := f.GetBool("watch")

		if dashboard && (ui.Raw || !term.IsTerminal(os.Stdout.Fd())) {
			return errors.New("--tui needs a terminal and can't be combined with --raw")
		}
		if watch && cfg.File == "" {
			return errors.New("--watch needs a portico.yaml to watch")
		}

		sup, err := supervisor.New(cfg, supervisor.Options{Only: args, Hide: hide, NoLogs: noLogs})
		if err != nil {
			return err
		}

		a := &app{cmd: cmd, cfg: cfg, rs: rs, sup: sup, dashboard: dashboard}

		addr := net.JoinHostPort(cfg.Proxy.Host, strconv.Itoa(cfg.Proxy.Port))
		listen := addr
		if cfg.Proxy.Engine == "nginx" {
			listen = "127.0.0.1:0"
		}

		socket := control.Socket(cfg.Project())
		ctl, err := control.Listen(socket)
		if err != nil {
			return err
		}
		defer os.Remove(socket)

		ln, err := net.Listen("tcp", listen)
		if err != nil {
			return err
		}

		if cfg.Proxy.Engine == "nginx" {
			if a.reloadEngine, err = addNginx(cfg, rs, sup, addr, ln.Addr().(*net.TCPAddr).Port); err != nil {
				return err
			}
		}

		a.proxy = proxy.New(rs, cfg.Proxy.CORS, sup.Status)
		a.proxy.Public = func(url string) { ui.Log("reachable at %s", ui.Accent.Render(url)) }
		srv := &http.Server{Handler: a.proxy, ReadHeaderTimeout: 10 * time.Second}

		ui.Pad(sup.Names())
		if !ui.Raw && !dashboard {
			lipgloss.Println(ui.Routes(rs))
		}
		ui.Log("listening on %s %s", ui.Accent.Render("http://"+addr), ui.Faint.Render("("+cfg.Proxy.Engine+")"))

		ctx, cancel := context.WithCancel(cmd.Context())
		a.stop = cancel
		var g run.Group

		g.Add(func() error {
			return srv.Serve(ln)
		}, func(error) {
			shutdown, done := context.WithTimeout(context.Background(), 5*time.Second)
			defer done()
			srv.Shutdown(shutdown)
		})

		g.Add(func() error {
			return sup.Run(ctx)
		}, func(error) {
			cancel()
		})

		g.Add(func() error {
			a.proxy.Watch(ctx, time.Second, a.check, func(r routes.Route, ready bool, health string) {
				switch {
				case ready:
					ui.Log("%s %s", r.Service, ui.Good.Render(fmt.Sprintf("ready on :%d", r.Port)))
				case health == "unhealthy":
					ui.Log("%s %s", r.Service, ui.Bad.Render("unhealthy"))
				case r.Kind == config.KindAttached:
					ui.Log("%s %s", r.Service, ui.Bad.Render(fmt.Sprintf("down on :%d", r.Port)))
				}
			})
			return nil
		}, func(error) {
			cancel()
		})

		if watch {
			g.Add(func() error {
				return a.watch(ctx)
			}, func(error) {
				cancel()
			})
		}

		if dashboard {
			program := tui.New(tui.Options{Addr: addr, Snapshot: a.snapshot, Restart: sup.Restart})
			ui.SetSink(tui.Send(program))
			g.Add(func() error {
				_, err := program.Run()
				ui.SetSink(nil)
				return err
			}, func(error) {
				program.Quit()
			})
		}

		// The control socket outlives the group, so `portico down` only sees it go once everything stopped.
		ctlSrv := &http.Server{Handler: a.controlHandler()}
		go ctlSrv.Serve(ctl)
		defer ctlSrv.Close()

		g.Add(run.SignalHandler(ctx, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP))

		err = g.Run()
		if err == nil || errors.As(err, &run.SignalError{}) || errors.Is(err, context.Canceled) {
			return nil
		}

		return err
	},
}

func init() {
	addServiceFlags(upCmd)

	f := upCmd.Flags()
	f.Bool("raw", false, "print service output without prefixes or colors")
	f.Bool("timestamps", false, "prefix every line with the time")
	f.StringSlice("hide", nil, "services whose output is not printed (still logged to .portico/logs)")
	f.Bool("no-logs", false, "don't write service output to .portico/logs")
	f.Bool("tui", false, "show a live dashboard instead of the log stream")
	f.Bool("watch", false, "reload when portico.yaml changes, restarting only what changed")
}
