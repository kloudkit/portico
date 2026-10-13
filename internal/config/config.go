package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/goccy/go-yaml"
)

var FileNames = []string{"portico.yaml", ".portico.yaml"}

var (
	validName = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]*$`)
	reserved  = []string{"portico", "nginx"}
)

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

	// CORS origins to allow; "*" allows any, an empty list disables CORS.
	CORS []string `yaml:"cors"`
}

type Service struct {
	Run     string            `yaml:"run"`
	Dir     string            `yaml:"dir"`
	Image   string            `yaml:"image"`
	Tag     string            `yaml:"tag"`
	Compose string            `yaml:"compose"`
	Service string            `yaml:"service"`
	Port    Port              `yaml:"port"`
	Route   string            `yaml:"route"`
	Routes  map[string]string `yaml:"routes"`
	Strip   *bool             `yaml:"strip"`
	Host    string            `yaml:"host"`
	Quiet   bool              `yaml:"quiet"`
	Log     *bool             `yaml:"log"`

	// HostPort is where an image service's port is published.
	HostPort int `yaml:"-"`
}

// Port is a port number, or "none" for a run service that serves nothing.
type Port int

const NoPort Port = -1

func (p *Port) UnmarshalYAML(data []byte) error {
	value := strings.TrimSpace(string(data))
	if value == "none" {
		*p = NoPort
		return nil
	}

	n, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("port must be a number or none, got %s", value)
	}
	*p = Port(n)

	return nil
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

func (s Service) Logged() bool {
	return s.Log == nil || *s.Log
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
			// renovate: datasource=docker depName=nginx
			Tag:  "1.30.5-alpine",
			CORS: []string{"*"},
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
	for _, origin := range c.Proxy.CORS {
		if !validOrigin(origin) {
			fail("proxy.cors: %q must be *, or an http(s) origin with an optional leading *. in the host", origin)
		}
	}

	for _, name := range c.Names() {
		s := c.Services[name]
		at := "services." + name

		if !validName.MatchString(name) {
			fail("%s: names may only use a-z, 0-9, _, . and -", at)
		}
		if slices.Contains(reserved, name) {
			fail("%s: %s is reserved", at, name)
		}

		set := 0
		for _, v := range []string{s.Run, s.Image, s.Compose} {
			if v != "" {
				set++
			}
		}
		if set > 1 {
			fail("%s: only one of run, image or compose may be set", at)
		}

		if s.Port == NoPort && s.Kind() != KindRun {
			fail("%s.port: none is only valid with run", at)
		} else if s.Port != 0 && s.Port != NoPort && !validPort(int(s.Port)) {
			fail("%s.port: %d is not a valid port", at, s.Port)
		}
		if s.Port == NoPort && s.Route != "" {
			fail("%s.route: a service without a port has nothing to route", at)
		}
		if s.Host != "" && s.Host != "preserve" {
			fail("%s.host: %q must be preserve or unset", at, s.Host)
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
		if s.Dir != "" && kind != KindRun {
			fail("%s.dir: only valid with run", at)
		}
		if kind == KindCompose && s.Port != 0 && s.Service == "" {
			fail("%s.port: picks one of a compose service's ports, so it needs service", at)
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

// AssignFreePorts gives run services without a port one for {port}, and
// image services a host port to publish on.
func (c *Config) AssignFreePorts() error {
	for _, name := range c.Names() {
		s := c.Services[name]
		kind := s.Kind()
		if (kind != KindRun || s.Port != 0) && kind != KindImage {
			continue
		}

		port, err := freePort()
		if err != nil {
			return fmt.Errorf("services.%s: %w", name, err)
		}

		if kind == KindImage {
			s.HostPort = port
		} else {
			s.Port = Port(port)
		}
		c.Services[name] = s
	}

	return nil
}

// Project identifies this config's directory, for container labels and the control socket.
func (c *Config) Project() string {
	sum := sha256.Sum256([]byte(c.Dir))
	return hex.EncodeToString(sum[:4])
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

func validOrigin(origin string) bool {
	if origin == "*" {
		return true
	}

	scheme, host, ok := strings.Cut(origin, "://")
	if !ok || (scheme != "http" && scheme != "https") || host == "" || strings.Contains(host, "/") {
		return false
	}

	return !strings.Contains(strings.TrimPrefix(host, "*."), "*")
}

// MatchOrigin reports whether origin matches a CORS pattern: "*", an exact
// origin, or one with a leading "*." that matches any subdomain.
func MatchOrigin(pattern, origin string) bool {
	if pattern == "*" || pattern == origin {
		return origin != ""
	}

	scheme, host, _ := strings.Cut(pattern, "://")
	suffix, ok := strings.CutPrefix(host, "*")
	if !ok {
		return false
	}

	rest, ok := strings.CutPrefix(origin, scheme+"://")
	return ok && len(rest) > len(suffix) && strings.HasSuffix(rest, suffix) && !strings.Contains(rest, "/")
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
