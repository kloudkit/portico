package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/kloudkit/portico/internal/config"
	"github.com/kloudkit/portico/internal/control"
	"github.com/kloudkit/portico/internal/engine"
	"github.com/kloudkit/portico/internal/proxy"
	"github.com/kloudkit/portico/internal/routes"
	"github.com/kloudkit/portico/internal/supervisor"
	"github.com/kloudkit/portico/internal/ui"
	"github.com/oklog/run"
	"github.com/spf13/cobra"
)

var upCmd = &cobra.Command{
	Use:   "up [service...]",
	Short: "Start your services and the proxy in front of them",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, rs, err := load(cmd)
		if err != nil {
			return err
		}

		f := cmd.Flags()
		ui.Raw, _ = f.GetBool("raw")
		ui.Timestamps, _ = f.GetBool("timestamps")
		hide, _ := f.GetStringSlice("hide")

		sup, err := supervisor.New(cfg, args, hide)
		if err != nil {
			return err
		}

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
			if err := addNginx(cfg, rs, sup, addr, ln.Addr().(*net.TCPAddr).Port); err != nil {
				return err
			}
		}

		p := proxy.New(rs, cfg.Proxy.CORS, sup.Status)
		srv := &http.Server{Handler: p, ReadHeaderTimeout: 10 * time.Second}

		ui.Pad(sup.Names())
		if !ui.Raw {
			lipgloss.Println(ui.Routes(rs))
		}
		ui.Log("listening on %s %s", ui.Accent.Render("http://"+addr), ui.Faint.Render("("+cfg.Proxy.Engine+")"))

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
			return sup.Run(ctx)
		}, func(error) {
			cancel()
		})

		g.Add(func() error {
			p.Watch(ctx, time.Second, func(r routes.Route, ready bool) {
				switch {
				case ready:
					ui.Log("%s %s", r.Service, ui.Good.Render(fmt.Sprintf("ready on :%d", r.Port)))
				case r.Kind == config.KindAttached:
					ui.Log("%s %s", r.Service, ui.Bad.Render(fmt.Sprintf("down on :%d", r.Port)))
				}
			})
			return nil
		}, func(error) {
			cancel()
		})

		// The control socket outlives the group, so `portico down` only sees it go once everything stopped.
		ctlSrv := &http.Server{Handler: controlHandler(cfg, rs, sup, p, cancel)}
		go ctlSrv.Serve(ctl)
		defer ctlSrv.Close()

		g.Add(run.SignalHandler(ctx, os.Interrupt, syscall.SIGTERM))

		err = g.Run()
		if errors.As(err, &run.SignalError{}) || errors.Is(err, context.Canceled) {
			return nil
		}

		return err
	},
}

func controlHandler(cfg *config.Config, rs []routes.Route, sup *supervisor.Supervisor, p *proxy.Proxy, stop func()) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		var services []control.Service
		for _, name := range cfg.Names() {
			svc := control.Service{Name: name, Kind: string(cfg.Services[name].Kind())}

			ready := false
			for _, r := range rs {
				if r.Name == name {
					svc.Routes = append(svc.Routes, control.Route{Path: r.Path, Port: r.Port, Ready: p.Ready(r.Path)})
					ready = ready || p.Ready(r.Path)
				}
			}

			state, detail := sup.Status(name)
			switch {
			case state == "" && ready:
				state = "up"
			case state == "":
				state = "down"
			case state == "starting" && (ready || svc.Routes == nil):
				state = "running"
			}
			svc.State = state
			if code, ok := detail["exit_code"].(int); ok {
				svc.ExitCode = &code
			}

			services = append(services, svc)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(services)
	})

	mux.HandleFunc("POST /restart/{name}", func(w http.ResponseWriter, r *http.Request) {
		if err := sup.Restart(r.PathValue("name")); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
		}
	})

	mux.HandleFunc("POST /stop", func(http.ResponseWriter, *http.Request) {
		ui.Log("stopping, requested by portico down")
		stop()
	})

	return mux
}

// addNginx writes the nginx config and supervises nginx, locally or in a container.
// The builtin proxy on fallback answers whatever nginx can't: no route, or a service that's down.
func addNginx(cfg *config.Config, rs []routes.Route, sup *supervisor.Supervisor, addr string, fallback int) error {
	dir := filepath.Join(cfg.Dir, ".portico", "nginx")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	n := engine.Nginx{Listen: addr, Fallback: fallback, CORS: cfg.Proxy.CORS, Routes: rs}
	conf := filepath.Join(dir, "nginx.conf")

	binary := engine.NginxBinary()
	if binary != "" {
		n.Temp = filepath.Join(dir, "tmp")
	} else {
		n.Temp = "/tmp"
	}

	rendered, err := n.Render()
	if err != nil {
		return err
	}
	if err := os.WriteFile(conf, []byte(rendered), 0o644); err != nil {
		return err
	}

	if binary != "" {
		if err := os.MkdirAll(n.Temp, 0o755); err != nil {
			return err
		}
		sup.Add("nginx", cfg.Proxy.Port, []string{binary, "-e", "stderr", "-p", n.Temp, "-c", conf, "-g", "daemon off;"}, nil)
		return nil
	}

	runtime, err := sup.Runtime(cfg)
	if err != nil {
		return fmt.Errorf("proxy.engine: nginx is not installed and %w", err)
	}

	name := "portico-" + sup.Project() + "-nginx"
	sup.Add("nginx", cfg.Proxy.Port, []string{
		runtime, "run", "--rm",
		"--name", name,
		"--entrypoint", "nginx",
		"--label", supervisor.Label + "=" + sup.Project(),
		"--network", "host",
		"-v", conf + ":/etc/portico/nginx.conf:ro",
		cfg.Proxy.Image + ":" + cfg.Proxy.Tag,
		"-c", "/etc/portico/nginx.conf", "-g", "daemon off;",
	}, []string{runtime, "rm", "-f", name})

	return nil
}

func init() {
	addServiceFlags(upCmd)

	f := upCmd.Flags()
	f.Bool("raw", false, "print service output without prefixes or colors")
	f.Bool("timestamps", false, "prefix every line with the time")
	f.StringSlice("hide", nil, "services whose output is not printed (still logged to .portico/logs)")
}
