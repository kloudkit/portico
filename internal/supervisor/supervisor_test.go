package supervisor

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kloudkit/portico/internal/config"
)

func start(t *testing.T, services map[string]config.Service) (*Supervisor, context.CancelFunc, chan struct{}) {
	t.Helper()

	cfg := config.Default()
	cfg.Dir = t.TempDir()
	cfg.Services = services

	s, err := New(cfg, Options{Hide: []string{"crash", "child"}})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(done)
	}()

	return s, cancel, done
}

func eventually(t *testing.T, cond func() bool) {
	t.Helper()

	for range 50 {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func TestCrashKeepsStateAndTail(t *testing.T) {
	s, cancel, done := start(t, map[string]config.Service{
		"crash": {Run: "echo one; echo two >&2; exit 3", Port: 1},
	})
	defer func() { cancel(); <-done }()

	eventually(t, func() bool {
		st, _ := s.Status("crash")
		return st.State == "exited"
	})

	st, _ := s.Status("crash")
	if st.ExitCode == nil || *st.ExitCode != 3 {
		t.Errorf("exit code: %v", st.ExitCode)
	}
	if len(st.Logs) != 2 || st.Logs[0] != "one" || st.Logs[1] != "two" {
		t.Errorf("logs: %v", st.Logs)
	}

	data, _ := os.ReadFile(filepath.Join(s.dir, ".portico", "logs", "crash.log"))
	if string(data) != "one\ntwo\n" {
		t.Errorf("log file: %q", data)
	}
}

func TestStopKillsTheProcessGroup(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	_, cancel, done := start(t, map[string]config.Service{
		"child": {Run: "trap '' TERM; sleep 60 & echo $! > " + pidFile + "; wait", Port: 1},
	})

	var pid int
	eventually(t, func() bool {
		data, _ := os.ReadFile(pidFile)
		pid, _ = strconv.Atoi(strings.TrimSpace(string(data)))
		return pid > 0
	})

	cancel()
	<-done

	eventually(t, func() bool {
		stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		return err != nil || strings.Contains(string(stat), ") Z ")
	})
}

func TestRestart(t *testing.T) {
	s, cancel, done := start(t, map[string]config.Service{
		"crash": {Run: "echo started; sleep 60", Port: 1},
	})
	defer func() { cancel(); <-done }()

	started := func() int {
		data, _ := os.ReadFile(filepath.Join(s.dir, ".portico", "logs", "crash.log"))
		return strings.Count(string(data), "started")
	}

	eventually(t, func() bool { return started() == 1 })

	if err := s.Restart("crash"); err != nil {
		t.Fatal(err)
	}
	eventually(t, func() bool { return started() == 2 })

	if err := s.Restart("missing"); err == nil {
		t.Error("expected an error for an unknown service")
	}
}

func TestLineWriter(t *testing.T) {
	var lines []string
	w := &lineWriter{emit: func(s string) { lines = append(lines, s) }}

	w.Write([]byte("plain\r\nprogress 10%\rprogress 50%"))
	w.Write([]byte("\rprogress 100%\ndone\n"))
	w.Write([]byte(strings.Repeat("x", maxLine+1)))

	want := []string{"plain", "progress 100%", "done", strings.Repeat("x", maxLine+1)}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Errorf("got %q", lines)
	}
	if len(w.buf) != 0 {
		t.Errorf("buffer kept %d bytes", len(w.buf))
	}
}

func TestDir(t *testing.T) {
	s, cancel, done := start(t, map[string]config.Service{
		"crash": {Run: "pwd", Dir: "sub", Port: 1},
	})
	defer func() { cancel(); <-done }()
	os.MkdirAll(filepath.Join(s.dir, "sub"), 0o755)

	eventually(t, func() bool {
		st, _ := s.Status("crash")
		return st.State == "exited"
	})

	if st, _ := s.Status("crash"); len(st.Logs) != 1 || !strings.HasSuffix(st.Logs[0], "/sub") {
		t.Errorf("ran in %v", st.Logs)
	}
}

func TestLogRotation(t *testing.T) {
	dir := t.TempDir()
	f, _ := os.Create(filepath.Join(dir, "api.log"))
	u := &unit{name: "api", quiet: true, logged: true, log: f}

	line := strings.Repeat("x", 1023)
	for range 11 * 1024 {
		u.line(line)
	}
	u.log.Close()

	old, _ := os.Stat(filepath.Join(dir, "api.log.1"))
	cur, _ := os.Stat(filepath.Join(dir, "api.log"))
	if old == nil || old.Size() <= maxLog || cur == nil || cur.Size() >= maxLog {
		t.Errorf("api.log.1: %v, api.log: %v", old, cur)
	}
}

func TestNoLogFile(t *testing.T) {
	no := false
	s, cancel, done := start(t, map[string]config.Service{
		"crash": {Run: "echo hi; exit 1", Port: 1, Log: &no},
	})
	defer func() { cancel(); <-done }()

	eventually(t, func() bool {
		st, _ := s.Status("crash")
		return st.State == "exited"
	})

	if _, err := os.Stat(filepath.Join(s.dir, ".portico", "logs", "crash.log")); err == nil {
		t.Error("log file was written")
	}
	if st, _ := s.Status("crash"); len(st.Logs) != 1 {
		t.Errorf("tail: %v", st.Logs)
	}
}
