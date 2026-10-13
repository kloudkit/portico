package routes

import (
	"context"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/kloudkit/portico/internal/compose"
	"github.com/kloudkit/portico/internal/config"
)

type Route struct {
	Path    string
	Service string
	Kind    config.Kind
	Port    int
	Strip   bool

	PreserveHost bool
}

// Resolve expands every service into its routes, sorted by path.
func Resolve(ctx context.Context, cfg *config.Config) ([]Route, error) {
	var routes []Route
	resolved := map[string]map[string][]int{}

	for _, name := range cfg.Names() {
		s := cfg.Services[name]
		route := s.Route
		if route == "" {
			route = "/" + name
		}

		add := func(p, service string, port int) {
			routes = append(routes, Route{
				Path:    path.Join("/", cfg.Proxy.Base, p),
				Service: service,
				Kind:    s.Kind(),
				Port:    port,
				Strip:   s.Stripped(),

				PreserveHost: s.Host == "preserve",
			})
		}

		if s.Kind() != config.KindCompose {
			add(route, name, s.Port)
			continue
		}

		runtime, err := cfg.RuntimeBinary()
		if err != nil {
			return nil, err
		}

		file := cfg.Path(s.Compose)
		ports, ok := resolved[file]
		if !ok {
			if ports, err = compose.Ports(ctx, runtime, file); err != nil {
				return nil, fmt.Errorf("services.%s: %w", name, err)
			}
			resolved[file] = ports
		}

		if s.Service != "" {
			published, ok := ports[s.Service]
			if !ok {
				return nil, fmt.Errorf("services.%s: compose service %q not found or publishes no ports", name, s.Service)
			}
			if s.Port != 0 {
				published = []int{s.Port}
			}
			addPorts(add, route, name+"/"+s.Service, published)
			continue
		}

		services := make([]string, 0, len(ports))
		for svc := range ports {
			services = append(services, svc)
		}
		slices.Sort(services)

		for _, svc := range services {
			p := path.Join(route, svc)
			if override, ok := s.Routes[svc]; ok {
				p = override
			}
			addPorts(add, p, name+"/"+svc, ports[svc])
		}
	}

	slices.SortFunc(routes, func(a, b Route) int { return strings.Compare(a.Path, b.Path) })

	for i := 1; i < len(routes); i++ {
		if routes[i].Path == routes[i-1].Path {
			return nil, fmt.Errorf("route %s is used by both %s and %s", routes[i].Path, routes[i-1].Service, routes[i].Service)
		}
	}

	return routes, nil
}

// addPorts mounts a single port at p, or each of several ports at p/<port>.
func addPorts(add func(p, service string, port int), p, service string, ports []int) {
	if len(ports) == 1 {
		add(p, service, ports[0])
		return
	}

	for _, port := range ports {
		add(path.Join(p, strconv.Itoa(port)), service, port)
	}
}
