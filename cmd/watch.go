package cmd

import (
	"context"
	"path/filepath"
	"reflect"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/fsnotify/fsnotify"
	"github.com/kloudkit/portico/internal/ui"
)

// watch reloads whenever portico.yaml changes. Editors often replace the file
// rather than write it, so it watches the directory.
func (a *app) watch(ctx context.Context) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()

	if err := w.Add(filepath.Dir(a.cfg.File)); err != nil {
		return err
	}
	ui.Log("watching %s", filepath.Base(a.cfg.File))

	var debounce <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return nil
		case ev := <-w.Events:
			if ev.Name == a.cfg.File && ev.Has(fsnotify.Write|fsnotify.Create|fsnotify.Rename) {
				debounce = time.After(300 * time.Millisecond)
			}
		case err := <-w.Errors:
			ui.Log("%s", ui.Bad.Render("watch: "+err.Error()))
		case <-debounce:
			a.reload()
		}
	}
}

// reload restarts the services whose config changed and swaps the routes.
func (a *app) reload() {
	a.mu.RLock()
	prev := a.cfg
	a.mu.RUnlock()

	cfg, rs, err := load(a.cmd, prev)
	if err != nil {
		ui.Log("%s", ui.Bad.Render("not reloading: "+err.Error()))
		return
	}
	if !reflect.DeepEqual(cfg.Proxy, prev.Proxy) || cfg.Runtime != prev.Runtime {
		ui.Log("%s", ui.Warn.Render("proxy and runtime settings changed; restart portico to apply them"))
		return
	}

	changed := cfg.Changed(prev)
	if len(changed) == 0 {
		return
	}

	a.mu.Lock()
	a.cfg, a.rs = cfg, rs
	a.mu.Unlock()

	a.proxy.SetRoutes(rs)
	if a.reloadEngine != nil {
		if err := a.reloadEngine(rs); err != nil {
			ui.Log("%s", ui.Bad.Render("nginx reload: "+err.Error()))
		}
	}

	ui.Log("reloading %v", changed)
	if err := a.sup.Apply(cfg, changed); err != nil {
		ui.Log("%s", ui.Bad.Render(err.Error()))
	}
	ui.Pad(a.sup.Names())
	if !ui.Raw && !a.dashboard {
		lipgloss.Println(ui.Routes(rs))
	}
}
