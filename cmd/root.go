package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"

	"charm.land/fang/v2"
	"github.com/charmbracelet/x/term"
	"github.com/kloudkit/portico/internal/config"
	"github.com/kloudkit/portico/internal/routes"
	"github.com/kloudkit/portico/internal/ui"
	"github.com/spf13/cobra"
)

var version = "dev"

var rootCmd = &cobra.Command{
	Use:   "portico",
	Short: "Many services, one front door",
	Long:  "Many services, one front door.\n\nPortico runs your local services and serves them through a single port, so the browser only ever talks to one origin.",
}

func Execute() {
	if info, ok := debug.ReadBuildInfo(); ok && version == "dev" && strings.HasPrefix(info.Main.Version, "v") {
		version = strings.TrimPrefix(info.Main.Version, "v")
	}

	opts := []fang.Option{
		fang.WithColorSchemeFunc(ui.FrappeColorScheme),
		fang.WithVersion(version),
		fang.WithoutManpage(),
		fang.WithErrorHandler(printError),
	}
	if err := fang.Execute(context.Background(), rootCmd, opts...); err != nil {
		os.Exit(1)
	}
}

// printError is fang's default handler without title-casing, which mangles paths.
func printError(w io.Writer, styles fang.Styles, err error) {
	if f, ok := w.(term.File); ok && !term.IsTerminal(f.Fd()) {
		fmt.Fprintln(w, err)
		return
	}

	fmt.Fprintln(w, styles.ErrorHeader.String())
	fmt.Fprintln(w, styles.ErrorText.UnsetTransform().Render(err.Error()))
	fmt.Fprintln(w)
}

func init() {
	rootCmd.PersistentFlags().StringP("config", "c", "", "config file (default: portico.yaml, searched upwards)")
	rootCmd.AddCommand(initCmd, upCmd, routesCmd, statusCmd, logsCmd, restartCmd, downCmd)
}

func addServiceFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringArray("run", nil, "run a service: name=command ({port} is substituted)")
	f.StringArray("route", nil, "route a path: /path=port or /path=service")
	f.Int("port", 0, "proxy port")
	f.String("host", "", "proxy host")
	f.String("base", "", "prefix for all routes")
}

// locate finds and reads the config file, or falls back to defaults in the current directory.
func locate(cmd *cobra.Command) (*config.Config, error) {
	path, _ := cmd.Flags().GetString("config")
	if path == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		if path, err = config.Discover(cwd); err != nil {
			return nil, err
		}
	}

	if path == "" {
		cfg := config.Default()
		cfg.Dir, _ = os.Getwd()
		return cfg, nil
	}

	return config.Load(path)
}

// load reads the config and resolves its routes. prev, when reloading, keeps the
// free ports of services that didn't change.
func load(cmd *cobra.Command, prev *config.Config) (*config.Config, []routes.Route, error) {
	cfg, err := locate(cmd)
	if err != nil {
		return nil, nil, err
	}

	f := cmd.Flags()
	runs, _ := f.GetStringArray("run")
	if err := cfg.ApplyRuns(runs); err != nil {
		return nil, nil, err
	}
	rs, _ := f.GetStringArray("route")
	if err := cfg.ApplyRoutes(rs); err != nil {
		return nil, nil, err
	}
	if f.Changed("port") {
		cfg.Proxy.Port, _ = f.GetInt("port")
	}
	if f.Changed("host") {
		cfg.Proxy.Host, _ = f.GetString("host")
	}
	if f.Changed("base") {
		cfg.Proxy.Base, _ = f.GetString("base")
	}

	if err := cfg.Validate(); err != nil {
		return nil, nil, err
	}
	if prev != nil {
		cfg.CarryPorts(prev)
	}
	if err := cfg.AssignFreePorts(); err != nil {
		return nil, nil, err
	}

	resolved, err := routes.Resolve(cmd.Context(), cfg)
	if err != nil {
		return nil, nil, err
	}

	return cfg, resolved, nil
}
