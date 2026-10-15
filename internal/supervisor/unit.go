package supervisor

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
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

type unit struct {
	name    string
	port    int
	quiet   bool
	logged  bool
	style   lipgloss.Style
	command func(ctx context.Context) *exec.Cmd
	cleanup []string

	restart  chan struct{}
	done     chan struct{}
	stopLife context.CancelFunc
	pidFile  string

	mu       sync.Mutex
	state    string
	code     int
	tail     []string
	log      *os.File
	logBytes int64
	cancel   context.CancelFunc
}

// stop ends the unit for good and waits until it's down.
func (u *unit) stop() {
	if u.stopLife != nil {
		u.stopLife()
		<-u.done
	}
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
		pid := cmd.Process.Pid
		os.WriteFile(u.pidFile, []byte(strconv.Itoa(pid)+" "+startTime(pid)), 0o644)
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

func (s *Supervisor) shell(svc config.Service, port int) func(context.Context) *exec.Cmd {
	command := svc.Run
	env := []string{"PYTHONUNBUFFERED=1"}
	if port != 0 {
		command = strings.ReplaceAll(command, "{port}", strconv.Itoa(port))
		env = append(env, "PORT="+strconv.Itoa(port))
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

func (s *Supervisor) container(name string, svc config.Service, hostPort int) (func(context.Context) *exec.Cmd, []string) {
	image := svc.Image
	if svc.Tag != "" {
		image += ":" + svc.Tag
	}
	container := fmt.Sprintf("portico-%s-%s", s.project, name)

	return func(ctx context.Context) *exec.Cmd {
		return exec.CommandContext(ctx, s.runtime, "run", "--rm",
			"--name", container,
			"--label", Label+"="+s.project,
			"-p", fmt.Sprintf("127.0.0.1:%d:%d", hostPort, svc.Port),
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
