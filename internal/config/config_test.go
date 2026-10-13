package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

	if cfg.ListenPort("web") == 0 {
		t.Error("web did not get a port")
	}
	if cfg.ListenPort("api") != 3200 {
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

func TestValidateNames(t *testing.T) {
	for name, ok := range map[string]bool{"api": true, "web-1.v2_x": true, "Api": false, "a/b": false, "-x": false, "nginx": false, "portico": false} {
		cfg := Default()
		cfg.Services[name] = Service{Port: 3200}
		if err := cfg.Validate(); (err == nil) != ok {
			t.Errorf("%q: valid=%v, err=%v", name, ok, err)
		}
	}
}

func TestMatchOrigin(t *testing.T) {
	cases := []struct {
		pattern, origin string
		want            bool
	}{
		{"*", "https://a.example", true},
		{"*", "", false},
		{"https://a.example", "https://a.example", true},
		{"https://a.example", "https://b.example", false},
		{"https://*.cloudworkstations.dev", "https://4400-ws.cluster.cloudworkstations.dev", true},
		{"https://*.cloudworkstations.dev", "http://4400-ws.cloudworkstations.dev", false},
		{"https://*.cloudworkstations.dev", "https://cloudworkstations.dev", false},
		{"https://*.cloudworkstations.dev", "https://evil.dev/.cloudworkstations.dev", false},
		{"https://*.cloudworkstations.dev", "https://cloudworkstations.dev.evil.dev", false},
	}

	for _, c := range cases {
		if got := MatchOrigin(c.pattern, c.origin); got != c.want {
			t.Errorf("MatchOrigin(%q, %q) = %v", c.pattern, c.origin, got)
		}
	}
}

func TestValidOrigin(t *testing.T) {
	for origin, ok := range map[string]bool{"*": true, "https://a.dev": true, "https://*.a.dev": true, "http://localhost:3000": true, "a.dev": false, "https://a.*.dev": false, "https://a.dev/x": false, "ftp://a.dev": false} {
		if validOrigin(origin) != ok {
			t.Errorf("%q: want %v", origin, ok)
		}
	}
}

func TestSchemaCoversConfig(t *testing.T) {
	data, err := os.ReadFile("../../portico.schema.json")
	if err != nil {
		t.Fatal(err)
	}

	var schema struct {
		Properties struct {
			Proxy struct {
				Properties map[string]any `json:"properties"`
			} `json:"proxy"`
			Services struct {
				AdditionalProperties struct {
					Properties map[string]any `json:"properties"`
				} `json:"additionalProperties"`
			} `json:"services"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}

	check := func(typ reflect.Type, documented map[string]any) {
		for field := range typ.Fields() {
			tag, _, _ := strings.Cut(field.Tag.Get("yaml"), ",")
			if tag != "-" && tag != "" {
				if _, ok := documented[tag]; !ok {
					t.Errorf("%s.%s is missing from portico.schema.json", typ.Name(), tag)
				}
			}
		}
	}

	check(reflect.TypeFor[Proxy](), schema.Properties.Proxy.Properties)
	check(reflect.TypeFor[Service](), schema.Properties.Services.AdditionalProperties.Properties)
}

func TestCarryPortsAndChanged(t *testing.T) {
	prev := Default()
	prev.Services = map[string]Service{
		"web":  {Run: "serve --port {port}"},
		"api":  {Run: "api --port {port}"},
		"gone": {Port: 3000},
	}
	prev.AssignFreePorts()

	next := Default()
	next.Services = map[string]Service{
		"web": {Run: "serve --port {port}"},
		"api": {Run: "api --verbose --port {port}"},
		"new": {Port: 4000},
	}
	next.CarryPorts(prev)
	next.AssignFreePorts()

	if next.ListenPort("web") != prev.ListenPort("web") {
		t.Error("unchanged web lost its port")
	}
	if next.ListenPort("api") == prev.ListenPort("api") {
		t.Error("changed api kept its port")
	}
	if got := strings.Join(next.Changed(prev), ","); got != "api,new,gone" {
		t.Errorf("changed: %s", got)
	}
}
