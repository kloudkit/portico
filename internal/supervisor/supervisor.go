package supervisor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kloudkit/portico/internal/config"
	"github.com/kloudkit/portico/internal/ui"
)

const (
	maxLine     = 64 * 1024
	maxLog      = 10 * 1024 * 1024
	tailLines   = 20
	stopTimeout = 5 * time.Second
	Label       = "portico.project"
)

type Options struct {
	Only   []string
	Hide   []string
	NoLogs bool
}

type Supervisor struct {
	dir     string
	project string
	runtime string
	opts    Options
	health  health

	mu    sync.Mutex
	units []*unit
	files map[string]string
	ctx   context.Context
	wg    sync.WaitGroup
}

// Status is the state of a service portico runs; exit code and last lines are set once it exited.
type Status struct {
	State    string   `json:"state"`
	ExitCode *int     `json:"exit_code,omitempty"`
	Logs     []string `json:"logs,omitempty"`
}

// New prepares a unit for every service portico owns.
func New(cfg *config.Config, opts Options) (*Supervisor, error) {
	s := &Supervisor{dir: cfg.Dir, project: cfg.Project(), opts: opts, files: map[string]string{}}

	for _, name := range opts.Only {
		svc, ok := cfg.Services[name]
		if !ok {
			return nil, fmt.Errorf("no service named %q", name)
		}
		if svc.Kind() == config.KindAttached {
			return nil, fmt.Errorf("%s is attached, there is nothing to start", name)
		}
	}

	for i, name := range cfg.Names() {
		u, err := s.newUnit(cfg, i, name)
		if err != nil {
			return nil, err
		}
		if u != nil {
			s.units = append(s.units, u)
		}
	}

	return s, nil
}

// newUnit builds the unit for one service, or nil if portico doesn't start it.
func (s *Supervisor) newUnit(cfg *config.Config, i int, name string) (*unit, error) {
	svc := cfg.Services[name]
	kind := svc.Kind()
	if kind == config.KindAttached || (len(s.opts.Only) > 0 && !slices.Contains(s.opts.Only, name)) {
		return nil, nil
	}

	if kind != config.KindRun {
		if _, err := s.Runtime(cfg); err != nil {
			return nil, err
		}
	}

	u := &unit{
		name:   name,
		quiet:  svc.Quiet || slices.Contains(s.opts.Hide, name),
		logged: svc.Logged() && !s.opts.NoLogs,
		style:  ui.Color(i),
	}

	u.port = cfg.ListenPort(name)
	switch kind {
	case config.KindRun:
		u.command = s.shell(svc, u.port)
	case config.KindImage:
		u.command, u.cleanup = s.container(name, svc, u.port)
	case config.KindCompose:
		file := cfg.Path(svc.Compose)
		s.files[name] = file
		u.command, u.cleanup = s.compose(file, svc.Service)
	}

	return u, nil
}

// Add runs an extra command alongside the services, such as the proxy engine.
func (s *Supervisor) Add(name string, port int, args []string, cleanup []string) {
	s.units = append(s.units, &unit{
		name:    name,
		port:    port,
		style:   ui.Color(len(s.units)),
		cleanup: cleanup,
		logged:  true,
		command: func(ctx context.Context) *exec.Cmd {
			return exec.CommandContext(ctx, args[0], args[1:]...)
		},
	})
}

// Runtime is the container runtime, resolving it if no service needed one yet.
func (s *Supervisor) Runtime(cfg *config.Config) (string, error) {
	if s.runtime == "" {
		runtime, err := cfg.RuntimeBinary()
		if err != nil {
			return "", err
		}
		s.runtime = runtime
	}

	return s.runtime, nil
}

func (s *Supervisor) Project() string {
	return s.project
}

func (s *Supervisor) Names() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	names := make([]string, len(s.units))
	for i, u := range s.units {
		names[i] = u.name
	}

	return names
}

func (s *Supervisor) find(name string) *unit {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, u := range s.units {
		if u.name == name {
			return u
		}
	}

	return nil
}

// Status reports a service's state, or false for one portico doesn't run.
func (s *Supervisor) Status(name string) (Status, bool) {
	u := s.find(name)
	if u == nil {
		return Status{}, false
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	st := Status{State: u.state}
	if u.state == "exited" {
		code := u.code
		st.ExitCode, st.Logs = &code, slices.Clone(u.tail)
	}

	return st, true
}

// Restart stops a service if it's running and starts it again.
func (s *Supervisor) Restart(name string) error {
	u := s.find(name)
	if u == nil {
		return fmt.Errorf("%s is not a service portico runs", name)
	}

	select {
	case u.restart <- struct{}{}:
	default:
	}

	u.mu.Lock()
	if u.cancel != nil {
		u.cancel()
	}
	u.mu.Unlock()

	return nil
}

// Run starts every unit and blocks until ctx ends, then stops them all.
func (s *Supervisor) Run(ctx context.Context) error {
	for _, dir := range []string{"logs", "pids"} {
		if err := os.MkdirAll(filepath.Join(s.dir, ".portico", dir), 0o755); err != nil {
			return err
		}
	}
	os.WriteFile(filepath.Join(s.dir, ".portico", ".gitignore"), []byte("*\n"), 0o644)

	if s.runtime != "" {
		s.removeLeftovers()
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	s.mu.Lock()
	s.ctx = ctx
	for _, u := range s.units {
		if err := s.start(u); err != nil {
			s.mu.Unlock()
			cancel()
			s.wg.Wait()
			return err
		}
	}
	s.mu.Unlock()

	<-ctx.Done()
	s.wg.Wait()

	return nil
}

// Apply brings the named services in line with cfg: stops the ones that changed
// or are gone, and starts the ones that changed or are new.
func (s *Supervisor) Apply(cfg *config.Config, names []string) error {
	for _, name := range names {
		if u := s.find(name); u != nil {
			u.stop()
			s.mu.Lock()
			s.units = slices.DeleteFunc(s.units, func(other *unit) bool { return other == u })
			s.mu.Unlock()
		}
	}

	all := cfg.Names()
	for _, name := range names {
		if _, ok := cfg.Services[name]; !ok {
			continue
		}

		u, err := s.newUnit(cfg, slices.Index(all, name), name)
		if err != nil {
			return err
		}
		if u == nil {
			continue
		}

		s.mu.Lock()
		if s.ctx.Err() != nil {
			s.mu.Unlock()
			return nil
		}
		s.units = append(s.units, u)
		err = s.start(u)
		s.mu.Unlock()
		if err != nil {
			return err
		}
	}

	return nil
}

// start runs a unit until it's stopped or ctx ends, starting it again on restart.
// Callers hold s.mu.
func (s *Supervisor) start(u *unit) error {
	path := filepath.Join(s.dir, ".portico", "logs", u.name+".log")
	os.Remove(path + ".1")
	if u.logged {
		f, err := os.Create(path)
		if err != nil {
			return err
		}
		u.log = f
	} else {
		os.Remove(path)
	}
	u.pidFile = filepath.Join(s.dir, ".portico", "pids", u.name)
	u.restart = make(chan struct{}, 1)
	u.done = make(chan struct{})

	life, stop := context.WithCancel(s.ctx)
	u.stopLife = stop

	s.wg.Go(func() {
		defer close(u.done)
		defer func() {
			if u.log != nil {
				u.log.Close()
			}
		}()

		for {
			run, cancel := context.WithCancel(life)
			u.mu.Lock()
			u.cancel = cancel
			u.mu.Unlock()

			u.run(run)
			cancel()

			select {
			case <-life.Done():
				return
			case <-u.restart:
			}
		}
	})

	return nil
}

func (s *Supervisor) removeLeftovers() {
	out, err := exec.Command(s.runtime, "ps", "-aq", "--filter", "label="+Label+"="+s.project).Output()
	if ids := strings.Fields(string(out)); err == nil && len(ids) > 0 {
		exec.Command(s.runtime, append([]string{"rm", "-f"}, ids...)...).Run()
		ui.Log("removed %d leftover container(s)", len(ids))
	}
}
