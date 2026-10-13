package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte(content), 0o644)
}

func TestDetect(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "compose.yaml"), "services:\n  db: {}\n  api: {}\n")
	write(t, filepath.Join(dir, "frontend", "package.json"), `{"scripts":{"dev":"vite"},"devDependencies":{"vite":"6"}}`)
	write(t, filepath.Join(dir, "frontend", "pnpm-lock.yaml"), "")
	write(t, filepath.Join(dir, "docs", "package.json"), `{"scripts":{"dev":"next dev"},"dependencies":{"next":"15"}}`)
	write(t, filepath.Join(dir, "lib", "package.json"), `{"scripts":{"build":"tsc"}}`)

	var got []string
	for _, c := range detect(dir) {
		got = append(got, c.yaml)
	}

	want := []string{
		"  stack:\n    compose: ./compose.yaml\n",
		"  frontend:\n    run: pnpm dev --port {port} --strictPort\n    dir: frontend\n    route: /\n",
		"  docs:\n    run: npm run dev -- -p {port}\n    dir: docs\n",
	}
	if strings.Join(got, "") != strings.Join(want, "") {
		t.Errorf("got\n%s\nwant\n%s", strings.Join(got, ""), strings.Join(want, ""))
	}
}
