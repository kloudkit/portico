package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/kloudkit/portico/internal/config"
	"github.com/kloudkit/portico/internal/routes"
)

func upstream(t *testing.T) int {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"path":   r.URL.RequestURI(),
			"host":   r.Host,
			"prefix": r.Header.Get("X-Forwarded-Prefix"),
		})
	}))
	t.Cleanup(srv.Close)

	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())

	return port
}

func get(t *testing.T, h http.Handler, path string) (int, map[string]any) {
	t.Helper()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)

	return rec.Code, body
}

func ready(p *Proxy) {
	for _, t := range p.targets {
		t.ready.Store(true)
	}
}

func TestRouting(t *testing.T) {
	api, web := upstream(t), upstream(t)

	p := New([]routes.Route{
		{Path: "/", Service: "web", Kind: config.KindAttached, Port: web, Strip: true},
		{Path: "/api", Service: "api", Kind: config.KindAttached, Port: api, Strip: true},
		{Path: "/keep", Service: "keep", Kind: config.KindAttached, Port: api, Strip: false},
	})
	ready(p)

	tests := []struct {
		path, want, prefix string
	}{
		{"/api/users?x=1", "/users?x=1", "/api"},
		{"/api", "/", "/api"},
		{"/apix", "/apix", ""},
		{"/keep/a", "/keep/a", ""},
		{"/", "/", ""},
	}

	for _, tt := range tests {
		code, body := get(t, p, tt.path)
		if code != http.StatusOK || body["path"] != tt.want || body["prefix"] != tt.prefix {
			t.Errorf("%s: got %d %v, want path %s prefix %q", tt.path, code, body, tt.want, tt.prefix)
		}
	}

	_, body := get(t, p, "/api/x")
	if body["host"] != "localhost:"+strconv.Itoa(api) {
		t.Errorf("host: got %v", body["host"])
	}
}

func TestErrors(t *testing.T) {
	p := New([]routes.Route{{Path: "/api", Service: "api", Port: 1}})

	if code, body := get(t, p, "/other"); code != http.StatusNotFound || body["error"] != "no_route" {
		t.Errorf("no route: got %d %v", code, body)
	}

	if code, body := get(t, p, "/api/x"); code != http.StatusServiceUnavailable || body["state"] != "down" {
		t.Errorf("down: got %d %v", code, body)
	}

	ready(p)
	if code, body := get(t, p, "/api/x"); code != http.StatusBadGateway || body["error"] != "upstream_unavailable" {
		t.Errorf("unreachable: got %d %v", code, body)
	}
}
