package compose

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Ports returns the published host ports of every service in a compose file,
// as resolved by `compose config` (env interpolation, overrides, profiles).
func Ports(ctx context.Context, runtime, file string) (map[string][]int, error) {
	out, err := exec.CommandContext(ctx, runtime, "compose", "-f", file, "config", "--format", "json").Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return nil, fmt.Errorf("%s compose config: %s", runtime, strings.TrimSpace(string(exit.Stderr)))
		}
		return nil, err
	}

	var doc struct {
		Services map[string]struct {
			Ports []struct {
				Published json.RawMessage `json:"published"`
			} `json:"ports"`
		} `json:"services"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		return nil, fmt.Errorf("%s compose config: %w", runtime, err)
	}

	ports := map[string][]int{}
	for name, svc := range doc.Services {
		for _, p := range svc.Ports {
			raw := strings.Trim(string(p.Published), `"`)
			if raw == "" || raw == "null" {
				continue
			}

			port, err := strconv.Atoi(raw)
			if err != nil {
				return nil, fmt.Errorf("service %s: published port %q is not supported", name, raw)
			}
			ports[name] = append(ports[name], port)
		}
	}

	return ports, nil
}
