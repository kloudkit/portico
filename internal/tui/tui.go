package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/kloudkit/portico/internal/control"
	"github.com/kloudkit/portico/internal/ui"
)

const keep = 2000

type Options struct {
	Addr     string
	Snapshot func() control.Status
	Restart  func(name string) error
}

type line struct {
	name  string
	style lipgloss.Style
	text  string
}

type lineMsg line

type tickMsg struct{}

type model struct {
	opts     Options
	status   control.Status
	lines    []line
	selected int
	width    int
	height   int
	note     string
}

// New returns the dashboard program. Its lines come from ui.Sink, which the caller points at Send.
func New(opts Options) *tea.Program {
	return tea.NewProgram(&model{opts: opts, status: opts.Snapshot(), selected: -1})
}

// Send turns a ui line into a message for the program.
func Send(p *tea.Program) func(name string, style lipgloss.Style, text string) {
	return func(name string, style lipgloss.Style, text string) {
		p.Send(lineMsg{name: name, style: style, text: text})
	}
}

func (m *model) Init() tea.Cmd {
	return tick()
}

func tick() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height

	case tickMsg:
		m.status = m.opts.Snapshot()
		m.selected = min(m.selected, len(m.status.Services)-1)
		return m, tick()

	case lineMsg:
		m.lines = append(m.lines, line(msg))
		if len(m.lines) > keep {
			m.lines = m.lines[len(m.lines)-keep:]
		}

	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			m.selected = max(m.selected-1, -1)
		case "down", "j":
			m.selected = min(m.selected+1, len(m.status.Services)-1)
		case "a", "esc":
			m.selected = -1
		case "r":
			if svc, ok := m.current(); ok {
				if err := m.opts.Restart(svc.Name); err != nil {
					m.note = err.Error()
				} else {
					m.note = "restarting " + svc.Name
				}
			}
		}
	}

	return m, nil
}

func (m *model) current() (control.Service, bool) {
	if m.selected < 0 || m.selected >= len(m.status.Services) {
		return control.Service{}, false
	}

	return m.status.Services[m.selected], true
}

func (m *model) View() tea.View {
	header := ui.Accent.Render("portico") + ui.Faint.Render("  http://"+m.opts.Addr)
	if m.status.URL != "" {
		header += ui.Faint.Render("  ·  ") + ui.Accent.Render(m.status.URL)
	}

	t := ui.Table("", "SERVICE", "KIND", "STATE", "ROUTES")
	for i, svc := range m.status.Services {
		var routes []string
		for _, r := range svc.Routes {
			routes = append(routes, fmt.Sprintf("%s → :%d", r.Path, r.Port))
		}
		marker := " "
		if i == m.selected {
			marker = ui.Accent.Render("›")
		}
		t.Row(marker, svc.Name, svc.Kind, ui.State(svc), strings.Join(routes, ", "))
	}
	services := t.String()

	showing := "all services"
	if svc, ok := m.current(); ok {
		showing = svc.Name
	}
	footer := ui.Faint.Render("↑/↓ select · a all · r restart · q quit  ·  showing " + showing)
	if m.note != "" {
		footer += ui.Faint.Render("  ·  ") + m.note
	}

	room := m.height - lipgloss.Height(header) - lipgloss.Height(services) - lipgloss.Height(footer) - 2
	logs := m.logs(max(room, 0))

	v := tea.NewView(lipgloss.JoinVertical(lipgloss.Left, header, services, logs, "", footer))
	v.AltScreen = true

	return v
}

// logs renders the last n lines of the selected service, or of all of them.
func (m *model) logs(n int) string {
	svc, filtered := m.current()

	var out []string
	for i := len(m.lines) - 1; i >= 0 && len(out) < n; i-- {
		l := m.lines[i]
		if filtered && l.name != svc.Name {
			continue
		}

		text := l.text
		if !filtered {
			text = l.style.Render(fmt.Sprintf("%-12s", l.name)) + ui.Faint.Render(" │ ") + text
		}
		out = append(out, ansi.Truncate(text, max(m.width, 20), "…"))
	}

	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	for len(out) < n {
		out = append(out, "")
	}

	return strings.Join(out, "\n")
}
