package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/kloudkit/portico/internal/config"
	"github.com/kloudkit/portico/internal/routes"
)

// health caches one `ps` of the container runtime, so every route can ask for
// its container's healthcheck status without running the CLI each time.
type health struct {
	mu      sync.Mutex
	checked time.Time
	status  map[string]string
}

type container struct {
	Names  any `json:"Names"`
	Labels any `json:"Labels"`
	Status string
}

// Health is Docker's healthcheck status of the container behind a route:
// healthy, unhealthy, starting, or "" when it has none or isn't a container.
func (s *Supervisor) Health(r routes.Route) string {
	var key string
	switch r.Kind {
	case config.KindImage:
		key = "portico-" + s.project + "-" + r.Name
	case config.KindCompose:
		s.mu.Lock()
		file := s.files[r.Name]
		s.mu.Unlock()
		key = strings.TrimPrefix(r.Service, r.Name+"/") + "|" + file
	default:
		return ""
	}

	s.health.mu.Lock()
	defer s.health.mu.Unlock()

	if time.Since(s.health.checked) > 900*time.Millisecond {
		s.health.status = s.listHealth()
		s.health.checked = time.Now()
	}

	return s.health.status[key]
}

func (s *Supervisor) listHealth() map[string]string {
	status := map[string]string{}
	if s.runtime == "" {
		return status
	}

	// A daemon that hangs mustn't stall the readiness checks behind it.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, s.runtime, "ps", "--format", "json").Output()
	if err != nil {
		return status
	}

	// docker prints one object per line, podman a single array.
	var containers []container
	if err := json.Unmarshal(out, &containers); err != nil {
		for _, line := range bytes.Split(out, []byte("\n")) {
			var c container
			if json.Unmarshal(line, &c) == nil {
				containers = append(containers, c)
			}
		}
	}

	for _, c := range containers {
		h := parseHealth(c.Status)
		if h == "" {
			continue
		}

		labels := labelMap(c.Labels)
		for _, name := range nameList(c.Names) {
			status[name] = h
		}
		if svc := labels["com.docker.compose.service"]; svc != "" {
			for _, file := range strings.Split(labels["com.docker.compose.project.config_files"], ",") {
				status[svc+"|"+file] = h
			}
		}
	}

	return status
}

func parseHealth(status string) string {
	switch {
	case strings.Contains(status, "(healthy)"):
		return "healthy"
	case strings.Contains(status, "(unhealthy)"):
		return "unhealthy"
	case strings.Contains(status, "starting)"):
		return "starting"
	}

	return ""
}

// labelMap reads labels as docker prints them ("a=b,c=d") or as podman does (an object).
func labelMap(v any) map[string]string {
	labels := map[string]string{}
	switch v := v.(type) {
	case string:
		for _, pair := range strings.Split(v, ",") {
			if k, val, ok := strings.Cut(pair, "="); ok {
				labels[k] = val
			}
		}
	case map[string]any:
		for k, val := range v {
			labels[k], _ = val.(string)
		}
	}

	return labels
}

func nameList(v any) []string {
	switch v := v.(type) {
	case string:
		return strings.Split(v, ",")
	case []any:
		var names []string
		for _, n := range v {
			if s, ok := n.(string); ok {
				names = append(names, s)
			}
		}
		return names
	}

	return nil
}
