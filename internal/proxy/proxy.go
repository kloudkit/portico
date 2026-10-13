package proxy

import (
	"context"
	"encoding/json"
	"fmt"
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
}

type target struct {
	routes.Route
	proxy *httputil.ReverseProxy
	ready atomic.Bool
}

func New(rs []routes.Route) *Proxy {
	p := &Proxy{}

	for _, r := range rs {
		t := &target{Route: r}
		upstream := &url.URL{Scheme: "http", Host: fmt.Sprintf("localhost:%d", r.Port)}

		t.proxy = &httputil.ReverseProxy{
			Rewrite: func(pr *httputil.ProxyRequest) {
				pr.Out.Header["X-Forwarded-For"] = pr.In.Header["X-Forwarded-For"]
				pr.SetURL(upstream)
				pr.SetXForwarded()

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
	t := p.match(r.URL.Path)
	if t == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no_route", "path": r.URL.Path})
		return
	}

	if !t.ready.Load() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":   "service_unavailable",
			"service": t.Service,
			"port":    t.Port,
			"state":   "down",
		})
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
