package proxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/kloudkit/portico/internal/config"
	"github.com/kloudkit/portico/internal/routes"
	"github.com/kloudkit/portico/internal/supervisor"
)

type Proxy struct {
	targets atomic.Pointer[[]*target]
	cors    []string
	status  StatusFunc
	public  atomic.Pointer[string]

	// Public is called once, with the URL the first forwarded request came in on.
	Public func(url string)
}

// StatusFunc reports the state of a service portico runs, or false for one it doesn't.
type StatusFunc func(name string) (supervisor.Status, bool)

type target struct {
	routes.Route
	proxy  *httputil.ReverseProxy
	ready  atomic.Bool
	health atomic.Value
}

// Check reports whether a route's upstream is ready, and Docker's healthcheck status if it has one.
type Check func(routes.Route) (ready bool, health string)

// New builds the proxy. cors lists the origins it answers CORS for; "*" allows any.
func New(rs []routes.Route, cors []string, status StatusFunc) *Proxy {
	p := &Proxy{cors: cors, status: status}
	p.SetRoutes(rs)

	return p
}

// SetRoutes replaces the routes; requests in flight finish on the old ones.
func (p *Proxy) SetRoutes(rs []routes.Route) {
	var targets []*target
	for _, r := range rs {
		t := &target{Route: r}
		upstream := &url.URL{Scheme: "http", Host: fmt.Sprintf("localhost:%d", r.Port)}

		t.proxy = &httputil.ReverseProxy{
			Rewrite: func(pr *httputil.ProxyRequest) {
				pr.Out.Header["X-Forwarded-For"] = pr.In.Header["X-Forwarded-For"]
				pr.SetURL(upstream)
				pr.SetXForwarded()
				if t.PreserveHost {
					pr.Out.Host = pr.In.Host
				}

				// Keep what the gateway in front of us saw.
				for _, h := range []string{"X-Forwarded-Host", "X-Forwarded-Proto"} {
					if v := pr.In.Header.Get(h); v != "" {
						pr.Out.Header.Set(h, v)
					}
				}

				if t.Strip && t.Path != "/" {
					pr.Out.URL.Path = trim(pr.Out.URL.Path, t.Path)
					if pr.Out.URL.RawPath != "" {
						pr.Out.URL.RawPath = trim(pr.Out.URL.RawPath, t.Path)
					}
					pr.Out.Header.Set("X-Forwarded-Prefix", t.Path)
				}
			},
			FlushInterval: -1,
			ModifyResponse: func(resp *http.Response) error {
				// Portico already answered CORS for this origin; don't let the app add a second set.
				if p.allowed(resp.Request.Header.Get("Origin")) {
					for h := range resp.Header {
						if strings.HasPrefix(h, "Access-Control-") {
							resp.Header.Del(h)
						}
					}
				}
				return nil
			},
			ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
				if !errors.Is(err, syscall.ECONNREFUSED) {
					writeJSON(w, http.StatusBadGateway, map[string]any{
						"error":   "upstream_error",
						"service": t.Service,
						"port":    t.Port,
						"detail":  err.Error(),
					})
					return
				}

				body := map[string]any{"error": "service_unavailable", "service": t.Service, "port": t.Port, "state": "down"}
				if p.status != nil {
					if st, ok := p.status(t.Name); ok {
						body["state"] = st.State
						if st.ExitCode != nil {
							body["exit_code"], body["logs"] = *st.ExitCode, st.Logs
						}
					}
				}
				writeJSON(w, http.StatusServiceUnavailable, body)
			},
		}

		t.health.Store("")
		targets = append(targets, t)
	}

	slices.SortFunc(targets, func(a, b *target) int { return len(b.Path) - len(a.Path) })
	p.targets.Store(&targets)
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.learn(r)

	if origin := r.Header.Get("Origin"); p.allowed(origin) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", origin)
		h.Set("Access-Control-Allow-Credentials", "true")
		h.Add("Vary", "Origin")

		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			h.Set("Access-Control-Allow-Methods", r.Header.Get("Access-Control-Request-Method"))
			if headers := r.Header.Get("Access-Control-Request-Headers"); headers != "" {
				h.Set("Access-Control-Allow-Headers", headers)
			}
			h.Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}

	t := p.match(r.URL.Path)
	if t == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no_route", "path": r.URL.Path})
		return
	}

	if h := t.health.Load().(string); h == "starting" || h == "unhealthy" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "service_unavailable", "service": t.Service, "port": t.Port, "state": h})
		return
	}

	t.proxy.ServeHTTP(w, r)
}

// Watch checks every upstream until ctx ends, reporting readiness changes.
// A nil check only probes the port.
func (p *Proxy) Watch(ctx context.Context, interval time.Duration, check Check, changed func(r routes.Route, ready bool, health string)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	if check == nil {
		check = func(r routes.Route) (bool, string) { return Probe(r.Port), "" }
	}

	seen := map[*target]bool{}
	for {
		for _, t := range *p.targets.Load() {
			ready, health := check(t.Route)
			t.health.Store(health)
			if t.ready.Swap(ready) != ready || !seen[t] {
				changed(t.Route, ready, health)
				seen[t] = true
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Ready reports whether the upstream behind a route path passed its last check, and its health.
func (p *Proxy) Ready(path string) (bool, string) {
	for _, t := range *p.targets.Load() {
		if t.Path == path {
			return t.ready.Load(), t.health.Load().(string)
		}
	}

	return false, ""
}

// URL is the public URL learned from the gateway in front, if any request came through one.
func (p *Proxy) URL() string {
	if u := p.public.Load(); u != nil {
		return *u
	}

	return ""
}

func (p *Proxy) learn(r *http.Request) {
	host := r.Header.Get("X-Forwarded-Host")
	if host == "" || p.public.Load() != nil {
		return
	}

	proto := r.Header.Get("X-Forwarded-Proto")
	if proto == "" {
		proto = "http"
	}
	url := proto + "://" + host
	if p.public.CompareAndSwap(nil, &url) && p.Public != nil {
		p.Public(url)
	}
}

func (p *Proxy) allowed(origin string) bool {
	return slices.ContainsFunc(p.cors, func(pattern string) bool { return config.MatchOrigin(pattern, origin) })
}

func (p *Proxy) match(path string) *target {
	for _, t := range *p.targets.Load() {
		if t.Path == "/" || path == t.Path || strings.HasPrefix(path, t.Path+"/") {
			return t
		}
	}

	return nil
}

// Probe reports whether something accepts connections on a local port.
func Probe(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), 500*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()

	return true
}

func trim(p, prefix string) string {
	p = strings.TrimPrefix(p, prefix)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}

	return p
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(body)
}
