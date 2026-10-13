package supervisor

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"
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

type Supervisor struct {
	units   []*unit
	dir     string
	project string
	runtime string
}

type unit struct {
	name    string
	port    int
	quiet   bool
	logged  bool
	style   lipgloss.Style
	command func(ctx context.Context) *exec.Cmd
	cleanup []string

	restart chan struct{}
	pidFile string

	mu       sync.Mutex
	state    string
	code     int
	tail     []string
	log      *os.File
	logBytes int64
	cancel   context.CancelFunc
}

type Options struct {
	Only   []string
	Hide   []string
	NoLogs bool
}

// New prepares a unit for every service portico owns.
func New(cfg *config.Config, opts Options) (*Supervisor, error) {
	s := &Supervisor{dir: cfg.Dir, project: cfg.Project()}

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
		svc := cfg.Services[name]
		kind := svc.Kind()
		if kind == config.KindAttached || (len(opts.Only) > 0 && !slices.Contains(opts.Only, name)) {
			continue
		}

		if kind != config.KindRun && s.runtime == "" {
			runtime, err := cfg.RuntimeBinary()
			if err != nil {
				return nil, err
			}
			s.runtime = runtime
		}

		u := &unit{
			name:   name,
			quiet:  svc.Quiet || slices.Contains(opts.Hide, name),
			logged: svc.Logged() && !opts.NoLogs,
			style:  ui.Color(i),
		}

		switch kind {
		case config.KindRun:
			if svc.Port != config.NoPort {
				u.port = int(svc.Port)
			}
			u.command = s.shell(svc)
		case config.KindImage:
			u.port = svc.HostPort
			u.command, u.cleanup = s.container(name, svc)
		case config.KindCompose:
			u.command, u.cleanup = s.compose(cfg.Path(svc.Compose), svc.Service)
		}

		s.units = append(s.units, u)
	}

	return s, nil
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

// Restart stops a service if it's running and starts it again.
func (s *Supervisor) Restart(name string) error {
	for _, u := range s.units {
		if u.name != name {
			continue
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

	return fmt.Errorf("%s is not a service portico runs", name)
}

func (s *Supervisor) Names() []string {
	names := make([]string, len(s.units))
	for i, u := range s.units {
		names[i] = u.name
	}

	return names
}

// Status is the state of a service portico runs; exit code and last lines are set once it exited.
type Status struct {
	State    string   `json:"state"`
	ExitCode *int     `json:"exit_code,omitempty"`
	Logs     []string `json:"logs,omitempty"`
}

// Status reports a service's state, or false for one portico doesn't run.
func (s *Supervisor) Status(name string) (Status, bool) {
	for _, u := range s.units {
		if u.name != name {
			continue
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

	return Status{}, false
}

// Run starts every unit and blocks until ctx ends, then stops them all.
func (s *Supervisor) Run(ctx context.Context) error {
	if len(s.units) == 0 {
		<-ctx.Done()
		return nil
	}

	logs := filepath.Join(s.dir, ".portico", "logs")
	pids := filepath.Join(s.dir, ".portico", "pids")
	for _, dir := range []string{logs, pids} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	os.WriteFile(filepath.Join(s.dir, ".portico", ".gitignore"), []byte("*\n"), 0o644)

	if s.runtime != "" {
		s.removeLeftovers()
	}

	var wg sync.WaitGroup
	for _, u := range s.units {
		path := filepath.Join(logs, u.name+".log")
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
		u.pidFile = filepath.Join(pids, u.name)
		u.restart = make(chan struct{}, 1)

		wg.Go(func() {
			defer func() {
				if u.log != nil {
					u.log.Close()
				}
			}()
			for {
				run, cancel := context.WithCancel(ctx)
				u.mu.Lock()
				u.cancel = cancel
				u.mu.Unlock()

				u.run(run)
				cancel()

				select {
				case <-ctx.Done():
					return
				case <-u.restart:
				}
			}
		})
	}

	<-ctx.Done()
	wg.Wait()

	return nil
}

func (u *unit) run(ctx context.Context) {
	if u.port != 0 && listening(u.port) {
		ui.Log("%s %s", u.name, ui.Warn.Render(fmt.Sprintf("port %d is already in use, another process may answer its route", u.port)))
	}

	cmd := u.command(ctx)
	out := &lineWriter{emit: u.line}
	cmd.Stdout, cmd.Stderr = out, out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM) }
	cmd.WaitDelay = stopTimeout

	u.setState("starting", 0)
	ui.Log("%s %s", u.name, ui.Faint.Render("starting: "+strings.Join(cmd.Args, " ")))

	err := cmd.Start()
	if err == nil {
		os.WriteFile(u.pidFile, []byte(strconv.Itoa(cmd.Process.Pid)), 0o644)
		err = cmd.Wait()
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		os.Remove(u.pidFile)
	}
	out.flush()

	code := -1
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}

	if len(u.cleanup) > 0 {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		exec.CommandContext(cleanup, u.cleanup[0], u.cleanup[1:]...).Run()
		cancel()
	}

	if ctx.Err() != nil {
		u.setState("stopped", code)
		ui.Log("%s %s", u.name, ui.Faint.Render("stopped"))
		return
	}

	u.setState("exited", code)
	if cmd.ProcessState == nil {
		ui.Log("%s %s", u.name, ui.Bad.Render(fmt.Sprintf("failed to start: %v", err)))
	} else {
		ui.Log("%s %s", u.name, ui.Bad.Render(fmt.Sprintf("exited with code %d", code)))
	}
}

func (u *unit) setState(state string, code int) {
	u.mu.Lock()
	defer u.mu.Unlock()

	u.state, u.code = state, code
	if state == "starting" {
		u.tail = nil
	}
}

func (u *unit) line(text string) {
	plain := ansi.Strip(text)

	u.mu.Lock()
	u.tail = append(u.tail, plain)
	if len(u.tail) > tailLines {
		u.tail = u.tail[len(u.tail)-tailLines:]
	}
	if u.log != nil {
		n, _ := fmt.Fprintln(u.log, plain)
		u.logBytes += int64(n)
		if u.logBytes > maxLog {
			u.rotate()
		}
	}
	u.mu.Unlock()

	if !u.quiet {
		ui.Line(u.name, u.style, text)
	}
}

// rotate moves the log aside as <name>.log.1, keeping one, and starts a new one.
func (u *unit) rotate() {
	path := u.log.Name()
	u.log.Close()
	os.Rename(path, path+".1")

	f, err := os.Create(path)
	if err != nil {
		u.log = nil
		return
	}
	u.log, u.logBytes = f, 0
}

func (s *Supervisor) shell(svc config.Service) func(context.Context) *exec.Cmd {
	command := svc.Run
	env := []string{"PYTHONUNBUFFERED=1"}
	if svc.Port != config.NoPort {
		port := strconv.Itoa(int(svc.Port))
		command = strings.ReplaceAll(command, "{port}", port)
		env = append(env, "PORT="+port)
	}

	return func(ctx context.Context) *exec.Cmd {
		cmd := exec.CommandContext(ctx, "sh", "-c", command)
		cmd.Dir = filepath.Join(s.dir, svc.Dir)
		cmd.Env = append(os.Environ(), env...)
		if !ui.Raw && term.IsTerminal(os.Stdout.Fd()) {
			cmd.Env = append(cmd.Env, "FORCE_COLOR=1")
		}
		return cmd
	}
}

func (s *Supervisor) container(name string, svc config.Service) (func(context.Context) *exec.Cmd, []string) {
	image := svc.Image
	if svc.Tag != "" {
		image += ":" + svc.Tag
	}
	container := fmt.Sprintf("portico-%s-%s", s.project, name)

	return func(ctx context.Context) *exec.Cmd {
		return exec.CommandContext(ctx, s.runtime, "run", "--rm",
			"--name", container,
			"--label", Label+"="+s.project,
			"-p", fmt.Sprintf("127.0.0.1:%d:%d", svc.HostPort, svc.Port),
			image,
		)
	}, []string{s.runtime, "rm", "-f", container}
}

func (s *Supervisor) compose(file, service string) (func(context.Context) *exec.Cmd, []string) {
	up := []string{"compose", "-f", file, "up"}
	stop := []string{s.runtime, "compose", "-f", file, "stop"}
	if service != "" {
		up = append(up, "--no-log-prefix", service)
		stop = append(stop, service)
	}

	return func(ctx context.Context) *exec.Cmd {
		cmd := exec.CommandContext(ctx, s.runtime, up...)
		cmd.Dir = s.dir
		return cmd
	}, stop
}

func (s *Supervisor) removeLeftovers() {
	out, err := exec.Command(s.runtime, "ps", "-aq", "--filter", "label="+Label+"="+s.project).Output()
	if ids := strings.Fields(string(out)); err == nil && len(ids) > 0 {
		exec.Command(s.runtime, append([]string{"rm", "-f"}, ids...)...).Run()
		ui.Log("removed %d leftover container(s)", len(ids))
	}
}

func listening(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", port), 200*time.Millisecond)
	if err != nil {
		return false
	}
	conn.Close()

	return true
}

// lineWriter splits a stream into lines. A carriage return overwrites the line
// so far, the way a terminal shows a progress bar, and overlong lines are cut.
type lineWriter struct {
	emit func(string)
	buf  []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			if j := bytes.LastIndexByte(w.buf, '\r'); j >= 0 && j < len(w.buf)-1 {
				w.buf = w.buf[j+1:]
			}
			if len(w.buf) > maxLine {
				w.emit(string(w.buf))
				w.buf = nil
			}
			return len(p), nil
		}

		line := bytes.TrimRight(w.buf[:i], "\r")
		if j := bytes.LastIndexByte(line, '\r'); j >= 0 {
			line = line[j+1:]
		}
		w.emit(string(line))
		w.buf = w.buf[i+1:]
	}
}

func (w *lineWriter) flush() {
	if len(w.buf) > 0 {
		w.emit(string(w.buf))
		w.buf = nil
	}
}
