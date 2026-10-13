package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/kloudkit/portico/internal/config"
	"github.com/kloudkit/portico/internal/engine"
	"github.com/kloudkit/portico/internal/routes"
	"github.com/kloudkit/portico/internal/supervisor"
)

// addNginx writes the nginx config and supervises nginx, locally or in a container,
// returning a function that rewrites the config and reloads nginx.
// The builtin proxy on fallback answers whatever nginx can't: no route, or a service that's down.
func addNginx(cfg *config.Config, rs []routes.Route, sup *supervisor.Supervisor, addr string, fallback int) (func([]routes.Route) error, error) {
	dir := filepath.Join(cfg.Dir, ".portico", "nginx")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	n := engine.Nginx{Listen: addr, Fallback: fallback, CORS: cfg.Proxy.CORS}
	conf := filepath.Join(dir, "nginx.conf")

	binary := engine.NginxBinary()
	if binary != "" {
		n.Temp = filepath.Join(dir, "tmp")
	} else {
		n.Temp = "/tmp"
	}

	write := func(rs []routes.Route) error {
		n.Routes = rs
		rendered, err := n.Render()
		if err != nil {
			return err
		}
		return os.WriteFile(conf, []byte(rendered), 0o644)
	}
	if err := write(rs); err != nil {
		return nil, err
	}

	if binary != "" {
		if err := os.MkdirAll(n.Temp, 0o755); err != nil {
			return nil, err
		}
		args := []string{"-e", "stderr", "-p", n.Temp, "-c", conf}
		sup.Add("nginx", cfg.Proxy.Port, append([]string{binary}, append(args, "-g", "daemon off;")...), nil)

		return func(rs []routes.Route) error {
			if err := write(rs); err != nil {
				return err
			}
			return exec.Command(binary, append(args, "-s", "reload")...).Run()
		}, nil
	}

	runtime, err := sup.Runtime(cfg)
	if err != nil {
		return nil, fmt.Errorf("proxy.engine: nginx is not installed and %w", err)
	}

	name := "portico-" + sup.Project() + "-nginx"
	sup.Add("nginx", cfg.Proxy.Port, []string{
		runtime, "run", "--rm",
		"--name", name,
		"--entrypoint", "nginx",
		"--label", supervisor.Label + "=" + sup.Project(),
		"--network", "host",
		"-v", conf + ":/etc/portico/nginx.conf:ro",
		cfg.Proxy.Image + ":" + cfg.Proxy.Tag,
		"-c", "/etc/portico/nginx.conf", "-g", "daemon off;",
	}, []string{runtime, "rm", "-f", name})

	return func(rs []routes.Route) error {
		if err := write(rs); err != nil {
			return err
		}
		return exec.Command(runtime, "exec", name, "nginx", "-c", "/etc/portico/nginx.conf", "-s", "reload").Run()
	}, nil
}
