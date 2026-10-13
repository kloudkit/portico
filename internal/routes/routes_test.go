package routes

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/kloudkit/portico/internal/config"
)

const composeConfig = `{"services": {
  "api": {"ports": [{"target": 80, "published": "3200"}, {"target": 9090, "published": "9090"}]},
  "web": {"ports": [{"target": 80, "published": "5173"}]},
  "db":  {"expose": ["5432"]}
}}`

// fakeDocker puts a docker on PATH that answers `compose config` with composeConfig.
func fakeDocker(t *testing.T) {
	t.Helper()

	bin := t.TempDir()
	os.WriteFile(filepath.Join(bin, "compose.json"), []byte(composeConfig), 0o644)
	script := "#!/bin/sh\ncat " + filepath.Join(bin, "compose.json") + "\n"
	os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func resolve(t *testing.T, services map[string]config.Service) ([]Route, error) {
	t.Helper()
	fakeDocker(t)

	cfg := config.Default()
	cfg.Dir = t.TempDir()
	cfg.Runtime = "docker"
	cfg.Services = services

	return Resolve(context.Background(), cfg)
}

func table(rs []Route) string {
	var lines []string
	for _, r := range rs {
		lines = append(lines, r.Path+" "+r.Service+" "+strconv.Itoa(r.Port))
	}

	return strings.Join(lines, "\n")
}

func TestComposeAllServices(t *testing.T) {
	rs, err := resolve(t, map[string]config.Service{
		"stack": {Compose: "compose.yaml", Route: "/stack", Routes: map[string]string{"web": "/"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	want := "/ stack/web 5173\n/stack/api/3200 stack/api 3200\n/stack/api/9090 stack/api 9090"
	if got := table(rs); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestComposeOneService(t *testing.T) {
	rs, err := resolve(t, map[string]config.Service{
		"single": {Compose: "compose.yaml", Service: "web", Route: "/web"},
		"pinned": {Compose: "compose.yaml", Service: "api", Port: 9090, Route: "/api"},
	})
	if err != nil {
		t.Fatal(err)
	}

	want := "/api pinned/api 9090\n/web single/web 5173"
	if got := table(rs); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestComposeErrors(t *testing.T) {
	cases := map[string]config.Service{
		"unknown service":     {Compose: "compose.yaml", Service: "nope"},
		"unpublished service": {Compose: "compose.yaml", Service: "db"},
		"pin not published":   {Compose: "compose.yaml", Service: "api", Port: 1234},
	}

	for name, svc := range cases {
		if _, err := resolve(t, map[string]config.Service{"x": svc}); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestDuplicateRoute(t *testing.T) {
	_, err := resolve(t, map[string]config.Service{
		"a": {Port: 1000, Route: "/x"},
		"b": {Port: 1001, Route: "/x"},
	})
	if err == nil || !strings.Contains(err.Error(), "/x") {
		t.Errorf("got %v", err)
	}
}
