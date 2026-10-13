package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"charm.land/huh/v2"
	"github.com/charmbracelet/x/term"
	"github.com/goccy/go-yaml"
	"github.com/kloudkit/portico/internal/ui"
	"github.com/spf13/cobra"
)

const schemaLine = "# yaml-language-server: $schema=https://raw.githubusercontent.com/kloudkit/portico/main/portico.schema.json\n"

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Write a starter portico.yaml from what's in this directory",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		force, _ := cmd.Flags().GetBool("force")
		yes, _ := cmd.Flags().GetBool("yes")

		if _, err := os.Stat("portico.yaml"); err == nil && !force {
			return errors.New("portico.yaml already exists; use --force to replace it")
		}

		found := detect(".")
		picked := found
		if len(found) > 0 && !yes && term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd()) {
			var chosen []int
			options := make([]huh.Option[int], len(found))
			for i, c := range found {
				options[i] = huh.NewOption(c.name+"  "+ui.Faint.Render(c.what), i).Selected(true)
			}

			err := huh.NewForm(huh.NewGroup(
				huh.NewMultiSelect[int]().Title("Add these to portico.yaml?").Options(options...).Value(&chosen),
			)).WithTheme(huh.ThemeFunc(huh.ThemeCatppuccin)).Run()
			if err != nil {
				return err
			}

			picked = nil
			for _, i := range chosen {
				picked = append(picked, found[i])
			}
		}

		var out strings.Builder
		out.WriteString(schemaLine + "\nservices:\n")
		for _, c := range picked {
			out.WriteString(c.yaml)
		}
		if len(picked) == 0 {
			out.WriteString("  web:\n    run: npm run dev -- --port {port}\n    route: /\n")
		}

		if err := os.WriteFile("portico.yaml", []byte(out.String()), 0o644); err != nil {
			return err
		}

		if len(picked) == 0 {
			ui.Log("wrote portico.yaml with an example service; edit it, then run portico up")
		} else {
			ui.Log("wrote portico.yaml with %d service(s); check it with portico routes", len(picked))
		}

		return nil
	},
}

type candidate struct {
	name string
	what string
	yaml string
}

var unsafeName = regexp.MustCompile(`[^a-z0-9_.-]+`)

// detect finds services in dir: compose files, and package.json dev scripts in it and its subdirectories.
func detect(dir string) []candidate {
	var found []candidate
	taken := map[string]bool{}
	name := func(want string) string {
		n := strings.Trim(unsafeName.ReplaceAllString(strings.ToLower(want), "-"), "-.")
		if n == "" || n == "portico" || n == "nginx" {
			n = "app-" + n
		}
		for base, i := n, 2; taken[n]; i++ {
			n = fmt.Sprintf("%s-%d", base, i)
		}
		taken[n] = true
		return n
	}

	for _, file := range []string{"compose.yaml", "compose.yml", "docker-compose.yaml", "docker-compose.yml"} {
		data, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil {
			continue
		}

		var doc struct {
			Services map[string]any `yaml:"services"`
		}
		yaml.Unmarshal(data, &doc)
		services := make([]string, 0, len(doc.Services))
		for s := range doc.Services {
			services = append(services, s)
		}
		slices.Sort(services)

		n := name("stack")
		found = append(found, candidate{
			name: n,
			what: file + " (" + strings.Join(services, ", ") + ")",
			yaml: fmt.Sprintf("  %s:\n    compose: ./%s\n", n, file),
		})
		break
	}

	// The likeliest frontend comes first, so it gets the / route.
	frontends := []string{".", "web", "frontend", "client", "app", "ui"}
	var rest []string
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") && e.Name() != "node_modules" && !slices.Contains(frontends, e.Name()) {
			rest = append(rest, e.Name())
		}
	}
	dirs := append(frontends, rest...)

	root := true
	for _, sub := range dirs {
		run, ok := devCommand(filepath.Join(dir, sub))
		if !ok {
			continue
		}

		want := sub
		if sub == "." {
			want = "web"
		}
		n := name(want)

		var b strings.Builder
		fmt.Fprintf(&b, "  %s:\n    run: %s\n", n, run)
		if sub != "." {
			fmt.Fprintf(&b, "    dir: %s\n", sub)
		}
		if root {
			b.WriteString("    route: /\n")
			root = false
		}

		found = append(found, candidate{name: n, what: filepath.Join(sub, "package.json") + " dev script", yaml: b.String()})
	}

	return found
}

// devCommand is the command that runs a package.json dev script on {port}, using
// the package manager the lock file points at.
func devCommand(dir string) (string, bool) {
	data, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return "", false
	}

	var pkg struct {
		Scripts         map[string]string `json:"scripts"`
		Dependencies    map[string]string `json:"dependencies"`
		DevDependencies map[string]string `json:"devDependencies"`
	}
	if json.Unmarshal(data, &pkg) != nil || pkg.Scripts["dev"] == "" {
		return "", false
	}

	has := func(dep string) bool {
		_, a := pkg.Dependencies[dep]
		_, b := pkg.DevDependencies[dep]
		return a || b
	}

	var flags string
	switch {
	case has("next"):
		flags = "-p {port}"
	case has("vite"):
		flags = "--port {port} --strictPort"
	case has("nuxt"), has("astro"):
		flags = "--port {port}"
	}

	run := "npm run dev"
	for lock, pm := range map[string]string{"pnpm-lock.yaml": "pnpm dev", "yarn.lock": "yarn dev", "bun.lockb": "bun run dev", "bun.lock": "bun run dev"} {
		if _, err := os.Stat(filepath.Join(dir, lock)); err == nil {
			run = pm
		}
	}

	switch {
	case flags == "":
		return run, true
	case run == "npm run dev":
		return run + " -- " + flags, true
	default:
		return run + " " + flags, true
	}
}

func init() {
	initCmd.Flags().Bool("force", false, "replace an existing portico.yaml")
	initCmd.Flags().BoolP("yes", "y", false, "add everything found without asking")
}
