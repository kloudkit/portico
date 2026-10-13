package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"syscall"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
	"github.com/fsnotify/fsnotify"
	"github.com/kloudkit/portico/internal/config"
	"github.com/kloudkit/portico/internal/control"
	"github.com/kloudkit/portico/internal/engine"
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

		a := &app{cmd: cmd, cfg: cfg, rs: rs, sup: sup}

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
			ui.Sink = tui.Send(program)
			g.Add(func() error {
				_, err := program.Run()
				ui.Sink = nil
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

// app is what a running `portico up` holds; --watch swaps cfg and rs on reload.
type app struct {
	cmd          *cobra.Command
	sup          *supervisor.Supervisor
	proxy        *proxy.Proxy
	reloadEngine func([]routes.Route) error
	stop         func()

	mu  sync.RWMutex
	cfg *config.Config
	rs  []routes.Route
}

// check is ready when Docker says a container is healthy, or else when its port answers.
func (a *app) check(r routes.Route) (bool, string) {
	health := a.sup.Health(r)
	if health != "" {
		return health == "healthy", health
	}

	return proxy.Probe(r.Port), ""
}

func (a *app) snapshot() control.Status {
	a.mu.RLock()
	defer a.mu.RUnlock()

	status := control.Status{URL: a.proxy.URL()}
	for _, name := range a.cfg.Names() {
		svc := control.Service{Name: name, Kind: string(a.cfg.Services[name].Kind())}

		ready := false
		for _, r := range a.rs {
			if r.Name != name {
				continue
			}
			routeReady, health := a.proxy.Ready(r.Path)
			svc.Routes = append(svc.Routes, control.Route{Path: r.Path, Port: r.Port, Ready: routeReady})
			ready = ready || routeReady
			if health != "" {
				svc.Health = health
			}
		}

		st, owned := a.sup.Status(name)
		switch {
		case !owned && ready:
			st.State = "up"
		case !owned:
			st.State = "down"
		case st.State == "starting" && (ready || svc.Routes == nil):
			st.State = "running"
		}
		svc.State, svc.ExitCode = st.State, st.ExitCode

		status.Services = append(status.Services, svc)
	}

	return status
}

func (a *app) controlHandler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(a.snapshot())
	})

	mux.HandleFunc("POST /restart/{name}", func(w http.ResponseWriter, r *http.Request) {
		if err := a.sup.Restart(r.PathValue("name")); err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
		}
	})

	mux.HandleFunc("POST /stop", func(http.ResponseWriter, *http.Request) {
		ui.Log("stopping, requested by portico down")
		a.stop()
	})

	return mux
}

// watch reloads whenever portico.yaml changes. Editors often replace the file
// rather than write it, so it watches the directory.
func (a *app) watch(ctx context.Context) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()

	if err := w.Add(filepath.Dir(a.cfg.File)); err != nil {
		return err
	}
	ui.Log("watching %s", filepath.Base(a.cfg.File))

	var debounce <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev := <-w.Events:
			if ev.Name == a.cfg.File && ev.Has(fsnotify.Write|fsnotify.Create|fsnotify.Rename) {
				debounce = time.After(300 * time.Millisecond)
			}
		case err := <-w.Errors:
			ui.Log("%s", ui.Bad.Render("watch: "+err.Error()))
		case <-debounce:
			a.reload()
		}
	}
}

// reload restarts the services whose config changed and swaps the routes.
func (a *app) reload() {
	a.mu.RLock()
	prev := a.cfg
	a.mu.RUnlock()

	cfg, rs, err := load(a.cmd, prev)
	if err != nil {
		ui.Log("%s", ui.Bad.Render("not reloading: "+err.Error()))
		return
	}
	if !reflect.DeepEqual(cfg.Proxy, prev.Proxy) || cfg.Runtime != prev.Runtime {
		ui.Log("%s", ui.Warn.Render("proxy and runtime settings changed; restart portico to apply them"))
		return
	}

	changed := cfg.Changed(prev)
	if len(changed) == 0 {
		return
	}

	a.mu.Lock()
	a.cfg, a.rs = cfg, rs
	a.mu.Unlock()

	a.proxy.SetRoutes(rs)
	if a.reloadEngine != nil {
		if err := a.reloadEngine(rs); err != nil {
			ui.Log("%s", ui.Bad.Render("nginx reload: "+err.Error()))
		}
	}

	ui.Log("reloading %v", changed)
	if err := a.sup.Apply(cfg, changed); err != nil {
		ui.Log("%s", ui.Bad.Render(err.Error()))
	}
	ui.Pad(a.sup.Names())
	if !ui.Raw && ui.Sink == nil {
		lipgloss.Println(ui.Routes(rs))
	}
}

// addNginx writes the nginx config and supervises nginx, locally or in a container,
// returning a function that rewrites the config and reloads nginx.
// The builtin proxy on fallback answers whatever nginx can't: no route, or a service that's down.
func addNginx(cfg *config.Config, rs []routes.Route, sup *supervisor.Supervisor, addr string, fallback int) (func([]routes.Route) error, error) {
	dir := filepath.Join(cfg.Dir, ".portico", "nginx")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	n := engine.Nginx{Listen: addr, Fallback: fallback, CORS: cfg.Proxy.CORS}
	conf := filepath.Join(dir, "nginx.conf")

	binary := engine.NginxBinary()
	if binary != "" {
		n.Temp = filepath.Join(dir, "tmp")
	} else {
		n.Temp = "/tmp"
	}

	write := func(rs []routes.Route) error {
		n.Routes = rs
		rendered, err := n.Render()
		if err != nil {
			return err
		}
		return os.WriteFile(conf, []byte(rendered), 0o644)
	}
	if err := write(rs); err != nil {
		return nil, err
	}

	if binary != "" {
		if err := os.MkdirAll(n.Temp, 0o755); err != nil {
			return nil, err
		}
		args := []string{"-e", "stderr", "-p", n.Temp, "-c", conf}
		sup.Add("nginx", cfg.Proxy.Port, append([]string{binary}, append(args, "-g", "daemon off;")...), nil)

		return func(rs []routes.Route) error {
			if err := write(rs); err != nil {
				return err
			}
			return exec.Command(binary, append(args, "-s", "reload")...).Run()
		}, nil
	}

	runtime, err := sup.Runtime(cfg)
	if err != nil {
		return nil, fmt.Errorf("proxy.engine: nginx is not installed and %w", err)
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

	return func(rs []routes.Route) error {
		if err := write(rs); err != nil {
			return err
		}
		return exec.Command(runtime, "exec", name, "nginx", "-c", "/etc/portico/nginx.conf", "-s", "reload").Run()
	}, nil
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
