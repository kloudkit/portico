package supervisor

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/kloudkit/portico/internal/config"
)

// Cleanup stops what an earlier `portico up` left behind: process groups from
// its pid files, its labelled containers, and the compose services it started.
func Cleanup(cfg *config.Config) []string {
	var stopped []string

	pids := filepath.Join(cfg.Dir, ".portico", "pids")
	entries, _ := os.ReadDir(pids)
	for _, e := range entries {
		file := filepath.Join(pids, e.Name())
		data, _ := os.ReadFile(file)
		os.Remove(file)

		if pid, _ := strconv.Atoi(strings.TrimSpace(string(data))); pid > 0 && stopGroup(pid) {
			stopped = append(stopped, e.Name())
		}
	}

	needsRuntime := cfg.Proxy.Engine == "nginx"
	for _, svc := range cfg.Services {
		needsRuntime = needsRuntime || svc.Kind() == config.KindImage || svc.Kind() == config.KindCompose
	}
	if !needsRuntime {
		return stopped
	}

	runtime, err := cfg.RuntimeBinary()
	if err != nil {
		return stopped
	}

	out, _ := exec.Command(runtime, "ps", "-aq", "--filter", "label="+Label+"="+cfg.Project()).Output()
	if ids := strings.Fields(string(out)); len(ids) > 0 {
		exec.Command(runtime, append([]string{"rm", "-f"}, ids...)...).Run()
		stopped = append(stopped, strconv.Itoa(len(ids))+" container(s)")
	}

	for _, name := range cfg.Names() {
		svc := cfg.Services[name]
		if svc.Kind() != config.KindCompose {
			continue
		}

		compose := []string{"compose", "-f", cfg.Path(svc.Compose)}
		target := []string{}
		if svc.Service != "" {
			target = append(target, svc.Service)
		}

		running, _ := exec.Command(runtime, slices.Concat(compose, []string{"ps", "-q"}, target)...).Output()
		if len(strings.TrimSpace(string(running))) == 0 {
			continue
		}
		if exec.Command(runtime, slices.Concat(compose, []string{"stop"}, target)...).Run() == nil && !slices.Contains(stopped, name) {
			stopped = append(stopped, name)
		}
	}

	return stopped
}

func stopGroup(pid int) bool {
	if syscall.Kill(-pid, syscall.SIGTERM) != nil {
		return false
	}

	for range 50 {
		if errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH) {
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	syscall.Kill(-pid, syscall.SIGKILL)

	return true
}
