package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverStopsAtGitRoot(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	deep := filepath.Join(repo, "a", "b")
	os.MkdirAll(deep, 0o755)
	os.Mkdir(filepath.Join(repo, ".git"), 0o755)
	os.WriteFile(filepath.Join(root, "portico.yaml"), nil, 0o644)

	if got, _ := Discover(deep); got != "" {
		t.Fatalf("found %s above the git root", got)
	}

	want := filepath.Join(repo, "a", ".portico.yaml")
	os.WriteFile(want, nil, 0o644)

	if got, _ := Discover(deep); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFlags(t *testing.T) {
	cfg := Default()

	if err := cfg.ApplyRuns([]string{"web=npm run dev -- --port {port}"}); err != nil {
		t.Fatal(err)
	}
	if err := cfg.ApplyRoutes([]string{"/=web", "/api=3200", "/=5173"}); err != nil {
		t.Fatal(err)
	}

	if s := cfg.Services["web"]; s.Route != "/" || s.Kind() != KindRun {
		t.Errorf("web: %+v", s)
	}
	if s := cfg.Services["api"]; s.Port != 3200 || s.Route != "/api" {
		t.Errorf("api: %+v", s)
	}
	if s := cfg.Services["root"]; s.Port != 5173 {
		t.Errorf("root: %+v", s)
	}

	if err := cfg.ApplyRoutes([]string{"/x=missing"}); err == nil {
		t.Error("expected an error for an unknown service")
	}
}

func TestAssignFreePorts(t *testing.T) {
	cfg := Default()
	cfg.Services["web"] = Service{Run: "serve --port {port}"}
	cfg.Services["api"] = Service{Run: "serve", Port: 3200}

	if err := cfg.AssignFreePorts(); err != nil {
		t.Fatal(err)
	}

	if cfg.Services["web"].Port == 0 {
		t.Error("web did not get a port")
	}
	if cfg.Services["api"].Port != 3200 {
		t.Error("api port was overwritten")
	}
}

func TestPortNone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "portico.yaml")
	os.WriteFile(path, []byte("services:\n  worker:\n    run: ./work\n    port: none\n  api:\n    port: 3200\n"), 0o644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Services["worker"].Port != NoPort || cfg.Services["api"].Port != 3200 {
		t.Fatalf("ports: %+v", cfg.Services)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	cfg.Services["api"] = Service{Port: NoPort}
	if err := cfg.Validate(); err == nil {
		t.Error("port: none should be rejected for an attached service")
	}
}
