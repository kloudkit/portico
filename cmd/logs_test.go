package cmd

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestLogged(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "api.log"), "")
	write(t, filepath.Join(dir, "api.log.1"), "")
	write(t, filepath.Join(dir, "web.log"), "")

	if got := logged(dir); !slices.Equal(got, []string{"api", "web"}) {
		t.Errorf("got %v, want [api web]", got)
	}
}
