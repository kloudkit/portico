package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"
)

var FileNames = []string{"portico.yaml", ".portico.yaml"}

type Config struct {
	Runtime  string             `yaml:"runtime"`
	Proxy    Proxy              `yaml:"proxy"`
	Services map[string]Service `yaml:"services"`

	// Dir is where the config file lives; relative paths resolve against it.
	Dir string `yaml:"-"`
}

type Proxy struct {
	Port   int    `yaml:"port"`
	Host   string `yaml:"host"`
	Base   string `yaml:"base"`
	Engine string `yaml:"engine"`
	Image  string `yaml:"image"`
	Tag    string `yaml:"tag"`
}

type Service struct {
	Run     string            `yaml:"run"`
	Image   string            `yaml:"image"`
	Tag     string            `yaml:"tag"`
	Compose string            `yaml:"compose"`
	Service string            `yaml:"service"`
	Port    int               `yaml:"port"`
	Route   string            `yaml:"route"`
	Routes  map[string]string `yaml:"routes"`
	Strip   *bool             `yaml:"strip"`
	Quiet   bool              `yaml:"quiet"`
}

type Kind string

const (
	KindRun      Kind = "run"
	KindImage    Kind = "image"
	KindCompose  Kind = "compose"
	KindAttached Kind = "attached"
)

func (s Service) Kind() Kind {
	switch {
	case s.Run != "":
		return KindRun
	case s.Image != "":
		return KindImage
	case s.Compose != "":
		return KindCompose
	default:
		return KindAttached
	}
}

func (s Service) Stripped() bool {
	return s.Strip == nil || *s.Strip
}

func Default() *Config {
	return &Config{
		Runtime: "auto",
		Proxy: Proxy{
			Port:   4400,
			Host:   "0.0.0.0",
			Base:   "/",
			Engine: "builtin",
			Image:  "nginx",
			Tag:    "stable-alpine",
		},
		Services: map[string]Service{},
	}
}

// Discover walks up from dir looking for a config file, stopping at the git root.
func Discover(dir string) (string, error) {
	for {
		for _, name := range FileNames {
			path := filepath.Join(dir, name)
			if _, err := os.Stat(path); err == nil {
				return path, nil
			}
		}

		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return "", nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
		}
		dir = parent
	}
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	cfg := Default()
	if err := yaml.UnmarshalWithOptions(data, cfg, yaml.Strict()); err != nil {
		return nil, fmt.Errorf("%s:\n%s", path, yaml.FormatError(err, false, true))
	}

	cfg.Dir = filepath.Dir(path)
	if cfg.Services == nil {
		cfg.Services = map[string]Service{}
	}

	return cfg, nil
}

func (c *Config) Validate() error {
	var errs []error
	fail := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf(format, args...))
	}

	if !slices.Contains([]string{"auto", "docker", "podman"}, c.Runtime) {
		fail("runtime: %q must be auto, docker or podman", c.Runtime)
	}
	if !slices.Contains([]string{"builtin", "nginx"}, c.Proxy.Engine) {
		fail("proxy.engine: %q must be builtin or nginx", c.Proxy.Engine)
	}
	if !validPort(c.Proxy.Port) {
		fail("proxy.port: %d is not a valid port", c.Proxy.Port)
	}
	if !strings.HasPrefix(c.Proxy.Base, "/") {
		fail("proxy.base: %q must start with /", c.Proxy.Base)
	}

	for _, name := range c.Names() {
		s := c.Services[name]
		at := "services." + name

		set := 0
		for _, v := range []string{s.Run, s.Image, s.Compose} {
			if v != "" {
				set++
			}
		}
		if set > 1 {
			fail("%s: only one of run, image or compose may be set", at)
		}

		if s.Port != 0 && !validPort(s.Port) {
			fail("%s.port: %d is not a valid port", at, s.Port)
		}
		if s.Route != "" && !strings.HasPrefix(s.Route, "/") {
			fail("%s.route: %q must start with /", at, s.Route)
		}

		kind := s.Kind()
		if (kind == KindAttached || kind == KindImage) && s.Port == 0 {
			fail("%s: port is required for %s services", at, kind)
		}
		if s.Tag != "" && kind != KindImage {
			fail("%s.tag: only valid with image", at)
		}
		if (s.Service != "" || s.Routes != nil) && kind != KindCompose {
			fail("%s: service and routes are only valid with compose", at)
		}
		if s.Routes != nil && s.Service != "" {
			fail("%s.routes: not valid together with service", at)
		}
		for svc, route := range s.Routes {
			if !strings.HasPrefix(route, "/") {
				fail("%s.routes.%s: %q must start with /", at, svc, route)
			}
		}
	}

	return errors.Join(errs...)
}

// AssignFreePorts gives every run service without a port a free one, for {port}.
func (c *Config) AssignFreePorts() error {
	for _, name := range c.Names() {
		s := c.Services[name]
		if s.Kind() != KindRun || s.Port != 0 {
			continue
		}

		port, err := freePort()
		if err != nil {
			return fmt.Errorf("services.%s: %w", name, err)
		}

		s.Port = port
		c.Services[name] = s
	}

	return nil
}

func (c *Config) Names() []string {
	names := make([]string, 0, len(c.Services))
	for name := range c.Services {
		names = append(names, name)
	}
	slices.Sort(names)

	return names
}

func (c *Config) Path(p string) string {
	if filepath.IsAbs(p) {
		return p
	}

	return filepath.Join(c.Dir, p)
}

// RuntimeBinary resolves "auto" to docker, falling back to podman.
func (c *Config) RuntimeBinary() (string, error) {
	if c.Runtime != "auto" {
		return c.Runtime, nil
	}

	for _, candidate := range []string{"docker", "podman"} {
		if _, err := exec.LookPath(candidate); err == nil {
			return candidate, nil
		}
	}

	return "", errors.New("runtime: neither docker nor podman found in PATH")
}

func validPort(p int) bool {
	return p > 0 && p < 65536
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()

	return l.Addr().(*net.TCPAddr).Port, nil
}
