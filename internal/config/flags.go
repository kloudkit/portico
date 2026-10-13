package config

import (
	"fmt"
	"strconv"
	"strings"
)

// ApplyRuns adds a run service per "name=command" entry.
func (c *Config) ApplyRuns(runs []string) error {
	for _, entry := range runs {
		name, command, ok := strings.Cut(entry, "=")
		if !ok || name == "" || command == "" {
			return fmt.Errorf("--run %q: expected name=command", entry)
		}

		s := c.Services[name]
		s.Run = command
		c.Services[name] = s
	}

	return nil
}

// ApplyRoutes handles "path=port" (adds an attached service) and "path=name"
// (routes an existing service).
func (c *Config) ApplyRoutes(routes []string) error {
	for _, entry := range routes {
		path, target, ok := strings.Cut(entry, "=")
		if !ok || path == "" || target == "" {
			return fmt.Errorf("--route %q: expected path=port or path=service", entry)
		}

		port, err := strconv.Atoi(target)
		if err != nil {
			s, ok := c.Services[target]
			if !ok {
				return fmt.Errorf("--route %q: no service named %q", entry, target)
			}
			s.Route = path
			c.Services[target] = s
			continue
		}

		name := strings.Trim(strings.ReplaceAll(path, "/", "-"), "-")
		if name == "" {
			name = "root"
		}
		if _, taken := c.Services[name]; taken {
			name = fmt.Sprintf("%s-%d", name, port)
		}

		c.Services[name] = Service{Port: port, Route: path}
	}

	return nil
}
