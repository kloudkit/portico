package supervisor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	style   lipgloss.Style
	command func(ctx context.Context) *exec.Cmd
	cleanup []string

	mu    sync.Mutex
	state string
	code  int
	tail  []string
	log   *os.File
}

// New prepares a unit for every service portico owns. only limits which ones start.
func New(cfg *config.Config, only, hide []string) (*Supervisor, error) {
	sum := sha256.Sum256([]byte(cfg.Dir))
	s := &Supervisor{dir: cfg.Dir, project: hex.EncodeToString(sum[:4])}

	for _, name := range only {
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
		if kind == config.KindAttached || (len(only) > 0 && !slices.Contains(only, name)) {
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
			name:  name,
			quiet: svc.Quiet || slices.Contains(hide, name),
			style: ui.Color(i),
		}

		switch kind {
		case config.KindRun:
			u.port = svc.Port
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
	names := make([]string, len(s.units))
	for i, u := range s.units {
		names[i] = u.name
	}

	return names
}

// Status reports an owned service's state, and its exit code and last lines once it exited.
func (s *Supervisor) Status(name string) (string, map[string]any) {
	for _, u := range s.units {
		if u.name != name {
			continue
		}

		u.mu.Lock()
		defer u.mu.Unlock()

		if u.state != "exited" {
			return u.state, nil
		}
		return u.state, map[string]any{"exit_code": u.code, "logs": slices.Clone(u.tail)}
	}

	return "", nil
}

// Run starts every unit and blocks until ctx ends, then stops them all.
func (s *Supervisor) Run(ctx context.Context) error {
	if len(s.units) == 0 {
		<-ctx.Done()
		return nil
	}

	logs := filepath.Join(s.dir, ".portico", "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		return err
	}
	os.WriteFile(filepath.Join(s.dir, ".portico", ".gitignore"), []byte("*\n"), 0o644)

	if s.runtime != "" {
		s.removeLeftovers()
	}

	var wg sync.WaitGroup
	for _, u := range s.units {
		f, err := os.Create(filepath.Join(logs, u.name+".log"))
		if err != nil {
			return err
		}
		u.log = f

		wg.Go(func() {
			defer f.Close()
			u.run(ctx)
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
		err = cmd.Wait()
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
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
	fmt.Fprintln(u.log, plain)
	u.mu.Unlock()

	if !u.quiet {
		ui.Line(u.name, u.style, text)
	}
}

func (s *Supervisor) shell(svc config.Service) func(context.Context) *exec.Cmd {
	command := strings.ReplaceAll(svc.Run, "{port}", strconv.Itoa(svc.Port))

	return func(ctx context.Context) *exec.Cmd {
		cmd := exec.CommandContext(ctx, "sh", "-c", command)
		cmd.Dir = s.dir
		cmd.Env = append(os.Environ(), "PORT="+strconv.Itoa(svc.Port), "PYTHONUNBUFFERED=1")
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

// lineWriter splits a stream into lines.
type lineWriter struct {
	emit func(string)
	buf  []byte
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			return len(p), nil
		}
		w.emit(strings.TrimRight(string(w.buf[:i]), "\r"))
		w.buf = w.buf[i+1:]
	}
}

func (w *lineWriter) flush() {
	if len(w.buf) > 0 {
		w.emit(string(w.buf))
		w.buf = nil
	}
}
