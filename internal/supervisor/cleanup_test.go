package supervisor

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"

	"github.com/kloudkit/portico/internal/config"
)

func TestCleanupSparesReusedPids(t *testing.T) {
	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill() }()

	pid := cmd.Process.Pid
	cfg := config.Default()
	cfg.Dir = t.TempDir()
	pids := filepath.Join(cfg.Dir, ".portico", "pids")
	write := func(name, recorded string) {
		t.Helper()
		if err := os.MkdirAll(pids, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(pids, name), []byte(strconv.Itoa(pid)+" "+recorded), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	for _, recorded := range []string{"", "1"} {
		write("stale", recorded)
		if stopped := Cleanup(cfg); len(stopped) > 0 {
			t.Fatalf("recorded %q: stopped %v, a process that only reuses the pid", recorded, stopped)
		}
	}

	write("ours", startTime(pid))
	if stopped := Cleanup(cfg); len(stopped) != 1 || stopped[0] != "ours" {
		t.Fatalf("stopped %v, want [ours]", stopped)
	}
	if err := cmd.Wait(); err == nil {
		t.Error("process was not stopped")
	}
}
