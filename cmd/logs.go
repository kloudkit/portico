package cmd

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kloudkit/portico/internal/ui"
	"github.com/spf13/cobra"
)

var logsCmd = &cobra.Command{
	Use:   "logs [service...]",
	Short: "Print service output from .portico/logs",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg, err := locate(cmd)
		if err != nil {
			return err
		}

		dir := filepath.Join(cfg.Dir, ".portico", "logs")
		names := args
		if len(names) == 0 {
			names = logged(dir)
		}
		if len(names) == 0 {
			return fmt.Errorf("no logs in %s yet", dir)
		}
		slices.Sort(names)
		ui.Pad(names)

		f := cmd.Flags()
		follow, _ := f.GetBool("follow")
		lines, _ := f.GetInt("lines")

		done := make(chan error, len(names))
		for i, name := range names {
			go func() {
				done <- tail(filepath.Join(dir, name+".log"), name, i, lines, follow)
			}()
			if !follow {
				if err := <-done; err != nil {
					return err
				}
			}
		}

		if follow {
			return <-done
		}

		return nil
	},
}

// logged lists the services with a log in dir, skipping rotated copies like api.log.1.
func logged(dir string) []string {
	var names []string
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), ".log"); ok && !e.IsDir() {
			names = append(names, name)
		}
	}

	return names
}

// tail prints the last n lines of a log, and with follow keeps printing new ones.
func tail(path, name string, color, n int, follow bool) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("no logs for %s", name)
	}
	defer file.Close()

	style := ui.Color(color)
	var last []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		last = append(last, scanner.Text())
		if len(last) > n {
			last = last[1:]
		}
	}
	for _, line := range last {
		ui.Line(name, style, line)
	}

	if !follow {
		return nil
	}

	offset, _ := file.Seek(0, io.SeekCurrent)
	reader := bufio.NewReader(file)
	var partial string
	for {
		if info, err := os.Stat(path); err == nil && info.Size() < offset {
			file.Seek(0, io.SeekStart)
			reader.Reset(file)
			offset, partial = 0, ""
		}

		chunk, err := reader.ReadString('\n')
		offset += int64(len(chunk))
		if err != nil {
			partial += chunk
			time.Sleep(200 * time.Millisecond)
			continue
		}
		ui.Line(name, style, strings.TrimSuffix(partial+chunk, "\n"))
		partial = ""
	}
}

func init() {
	f := logsCmd.Flags()
	f.BoolP("follow", "f", false, "keep printing new output")
	f.IntP("lines", "n", 20, "lines to show from the end of each log")
}
