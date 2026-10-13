package cmd

import (
	"encoding/json"
	"net/http"
	"sync"

	"github.com/kloudkit/portico/internal/config"
	"github.com/kloudkit/portico/internal/control"
	"github.com/kloudkit/portico/internal/proxy"
	"github.com/kloudkit/portico/internal/routes"
	"github.com/kloudkit/portico/internal/supervisor"
	"github.com/kloudkit/portico/internal/ui"
	"github.com/spf13/cobra"
)

// app is what a running `portico up` holds; --watch swaps cfg and rs on reload.
type app struct {
	cmd          *cobra.Command
	sup          *supervisor.Supervisor
	proxy        *proxy.Proxy
	reloadEngine func([]routes.Route) error
	stop         func()
	dashboard    bool

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
