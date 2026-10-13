package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/kloudkit/portico/internal/routes"
)

type Proxy struct {
	targets []*target
	cors    []string
	status  StatusFunc
}

// StatusFunc reports the state of a service portico runs, or "" for one it doesn't.
type StatusFunc func(name string) (string, map[string]any)

type target struct {
	routes.Route
	proxy *httputil.ReverseProxy
	ready atomic.Bool
}

// New builds the proxy. cors lists the origins it answers CORS for; "*" allows any.
func New(rs []routes.Route, cors []string, status StatusFunc) *Proxy {
	p := &Proxy{cors: cors, status: status}

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
				writeJSON(w, http.StatusBadGateway, map[string]any{
					"error":   "upstream_unavailable",
					"service": t.Service,
					"port":    t.Port,
					"detail":  err.Error(),
				})
			},
		}

		p.targets = append(p.targets, t)
	}

	slices.SortFunc(p.targets, func(a, b *target) int { return len(b.Path) - len(a.Path) })

	return p
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

	if !t.ready.Load() {
		body := map[string]any{
			"error":   "service_unavailable",
			"service": t.Service,
			"port":    t.Port,
			"state":   "down",
		}
		if p.status != nil {
			if state, detail := p.status(t.Name); state != "" {
				body["state"] = state
				maps.Copy(body, detail)
			}
		}
		writeJSON(w, http.StatusServiceUnavailable, body)
		return
	}

	t.proxy.ServeHTTP(w, r)
}

// Watch probes every upstream until ctx ends, reporting readiness changes.
func (p *Proxy) Watch(ctx context.Context, interval time.Duration, changed func(routes.Route, bool)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for first := true; ; first = false {
		for _, t := range p.targets {
			ready := probe(t.Port)
			if t.ready.Swap(ready) != ready || first {
				changed(t.Route, ready)
			}
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Ready reports whether the upstream behind a route path answered its last probe.
func (p *Proxy) Ready(path string) bool {
	for _, t := range p.targets {
		if t.Path == path {
			return t.ready.Load()
		}
	}

	return false
}

func (p *Proxy) allowed(origin string) bool {
	return origin != "" && (slices.Contains(p.cors, "*") || slices.Contains(p.cors, origin))
}

func (p *Proxy) match(path string) *target {
	for _, t := range p.targets {
		if t.Path == "/" || path == t.Path || strings.HasPrefix(path, t.Path+"/") {
			return t
		}
	}

	return nil
}

func probe(port int) bool {
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
