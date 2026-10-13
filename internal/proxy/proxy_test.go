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
	}, nil, nil)
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
	p := New([]routes.Route{{Path: "/api", Service: "api", Port: 1}}, nil, nil)

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

func TestCORS(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "https://app.example")
		w.Write([]byte("{}"))
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())

	rs := []routes.Route{{Path: "/api", Service: "api", Port: port, Strip: true}}
	const origin = "https://other.example"

	send := func(p *Proxy, method string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/x", nil)
		req.Header.Set("Origin", origin)
		if method == http.MethodOptions {
			req.Header.Set("Access-Control-Request-Method", "POST")
			req.Header.Set("Access-Control-Request-Headers", "content-type")
		}
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, req)
		return rec
	}

	for _, cors := range [][]string{{"*"}, {origin}} {
		p := New(rs, cors, nil)
		ready(p)

		pre := send(p, http.MethodOptions)
		if pre.Code != http.StatusNoContent || pre.Header().Get("Access-Control-Allow-Headers") != "content-type" {
			t.Errorf("%v preflight: %d %v", cors, pre.Code, pre.Header())
		}

		got := send(p, http.MethodGet).Header().Values("Access-Control-Allow-Origin")
		if len(got) != 1 || got[0] != origin {
			t.Errorf("%v allow-origin: %v", cors, got)
		}
	}

	p := New(rs, []string{"https://only.example"}, nil)
	ready(p)

	if got := send(p, http.MethodGet).Header().Values("Access-Control-Allow-Origin"); len(got) != 1 || got[0] != "https://app.example" {
		t.Errorf("disallowed origin should get the app's own headers, got %v", got)
	}
}

func TestPreserveHost(t *testing.T) {
	port := upstream(t)
	p := New([]routes.Route{{Path: "/", Service: "web", Port: port, PreserveHost: true}}, nil, nil)
	ready(p)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "4400-ws.example.dev"
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	var body map[string]any
	json.Unmarshal(rec.Body.Bytes(), &body)
	if body["host"] != "4400-ws.example.dev" {
		t.Errorf("host: got %v", body["host"])
	}
}
