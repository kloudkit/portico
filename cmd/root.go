package cmd

import (
	"context"
	"fmt"
	"io"
	"os"

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
	Short: "One origin for all your local services",
	Long:  "Portico runs your local services and serves them through a single proxy port, so the browser only ever talks to one origin.",
}

func Execute() {
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
	rootCmd.AddCommand(upCmd, routesCmd)
}

func addServiceFlags(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringArray("run", nil, "run a service: name=command ({port} is substituted)")
	_ = f.MarkHidden("run")
	f.StringArray("route", nil, "route a path: /path=port or /path=service")
	f.Int("port", 0, "proxy port")
	f.String("host", "", "proxy host")
	f.String("base", "", "prefix for all routes")
}

func load(cmd *cobra.Command) (*config.Config, []routes.Route, error) {
	path, _ := cmd.Flags().GetString("config")
	if path == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, nil, err
		}
		if path, err = config.Discover(cwd); err != nil {
			return nil, nil, err
		}
	}

	cfg := config.Default()
	if path != "" {
		var err error
		if cfg, err = config.Load(path); err != nil {
			return nil, nil, err
		}
	} else {
		cfg.Dir, _ = os.Getwd()
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
	if err := cfg.AssignFreePorts(); err != nil {
		return nil, nil, err
	}

	resolved, err := routes.Resolve(cmd.Context(), cfg)
	if err != nil {
		return nil, nil, err
	}

	return cfg, resolved, nil
}
