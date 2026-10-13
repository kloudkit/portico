package ui

import (
	"fmt"
	"image/color"
	"os"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
)

var (
	Raw        bool
	Timestamps bool

	// Sink, when set, receives every line instead of the terminal, e.g. for the TUI.
	Sink func(name string, style lipgloss.Style, text string)

	mu      sync.Mutex
	width   = len("portico")
	palette = []color.Color{Peach, Rosewater, Blue, Mauve, Green, Yellow, Red}
)

// Pad aligns every prefix to the longest of names.
func Pad(names []string) {
	for _, n := range names {
		width = max(width, len(n))
	}
}

func Color(i int) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(palette[i%len(palette)])
}

// Line prints one line of output under a name prefix.
func Line(name string, style lipgloss.Style, text string) {
	mu.Lock()
	defer mu.Unlock()

	if Sink != nil {
		Sink(name, style, text)
		return
	}

	if Raw {
		lipgloss.Println(text)
		return
	}

	prefix := style.Render(fmt.Sprintf("%-*s", width, name)) + Faint.Render(" │ ")
	if Timestamps {
		prefix = Faint.Render(time.Now().Format("15:04:05")) + " " + prefix
	}

	lipgloss.Println(prefix + text)
}

// Log prints a message from portico itself; in raw mode it goes to stderr.
func Log(format string, args ...any) {
	if Raw && Sink == nil {
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintf(os.Stderr, "portico: "+format+"\n", args...)
		return
	}

	Line("portico", Accent, fmt.Sprintf(format, args...))
}
