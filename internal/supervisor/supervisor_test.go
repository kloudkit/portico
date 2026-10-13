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

	s, err := New(cfg, nil, []string{"crash", "child"})
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
		state, _ := s.Status("crash")
		return state == "exited"
	})

	_, detail := s.Status("crash")
	if detail["exit_code"] != 3 {
		t.Errorf("exit code: %v", detail["exit_code"])
	}
	if logs := detail["logs"].([]string); len(logs) != 2 || logs[0] != "one" || logs[1] != "two" {
		t.Errorf("logs: %v", logs)
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
