package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/kloudkit/portico/internal/config"
	"github.com/kloudkit/portico/internal/routes"
)

var sample = []routes.Route{
	{Path: "/", Service: "web", Port: 5173, Strip: true},
	{Path: "/api", Service: "api", Port: 3200, Strip: true},
	{Path: "/legacy", Service: "legacy", Port: 3200},
	{Path: "/app/v1.2", Service: "dotted", Port: 3300, Strip: true, PreserveHost: true},
}

func TestNginxConfigIsValid(t *testing.T) {
	runtime, err := exec.LookPath("docker")
	if err != nil {
		t.Skip("docker not available")
	}

	cases := map[string]Nginx{
		"any origin": {CORS: []string{"*"}, Routes: sample},
		"allowlist":  {CORS: []string{"https://a.example", "https://*.b.example"}, Routes: sample},
		"no root":    {Routes: sample[1:]},
	}

	for name, n := range cases {
		t.Run(name, func(t *testing.T) {
			n.Listen, n.Fallback, n.Temp = "4400", 4401, "/tmp"

			rendered, err := n.Render()
			if err != nil {
				t.Fatal(err)
			}
			conf := filepath.Join(t.TempDir(), "nginx.conf")
			os.WriteFile(conf, []byte(rendered), 0o644)

			tag := config.Default().Proxy.Tag
			out, err := exec.Command(runtime, "run", "--rm", "-v", conf+":/etc/portico/nginx.conf:ro",
				"nginx:"+tag, "nginx", "-t", "-c", "/etc/portico/nginx.conf").CombinedOutput()
			if err != nil {
				t.Fatalf("nginx -t: %v\n%s\n%s", err, out, rendered)
			}
		})
	}
}
