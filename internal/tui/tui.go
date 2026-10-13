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

const keep = 5000

type Options struct {
	Addr     string
	Snapshot func() control.Status
	Restart  func(name string) error
}

type line struct {
	name  string
	style lipgloss.Style
	text  string

	// The line as shown, cached for the width it was rendered at.
	width    int
	all, own string
}

type lineMsg line

type tickMsg struct{}

type flushMsg struct{}

type model struct {
	opts     Options
	status   control.Status
	lines    []line
	selected int
	width    int
	rows     int
	note     string
	dirty    bool

	// The log pane: offset is how many lines it's scrolled up from the bottom.
	pane   string
	height int
	offset int
	shown  int

	// Header, table and footer, rendered only when they change; View runs on every message.
	top, bottom string
}

// New returns the dashboard program. Its lines come from ui.SetSink(Send(program)).
func New(opts Options) *tea.Program {
	// The log pane changes at most every 100ms, so drawing faster only burns CPU.
	return tea.NewProgram(&model{opts: opts, status: opts.Snapshot(), selected: -1}, tea.WithFPS(15))
}

// Send turns a ui line into a message for the program.
func Send(p *tea.Program) func(name string, style lipgloss.Style, text string) {
	return func(name string, style lipgloss.Style, text string) {
		p.Send(lineMsg{name: name, style: style, text: text})
	}
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(tick(), flush())
}

func tick() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(time.Time) tea.Msg { return tickMsg{} })
}

// flush redraws the log pane at most every 100ms, however fast lines arrive.
func flush() tea.Cmd {
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return flushMsg{} })
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.rows = msg.Width, msg.Height

	case tickMsg:
		m.status = m.opts.Snapshot()
		m.selected = min(m.selected, len(m.status.Services)-1)
		m.layout()
		return m, tick()

	case lineMsg:
		m.lines = append(m.lines, line(msg))
		if len(m.lines) > keep {
			m.lines = m.lines[len(m.lines)-keep:]
		}
		m.dirty = true
		return m, nil

	case flushMsg:
		if m.dirty {
			m.fill()
			m.dirty = false
		}
		return m, flush()

	case tea.KeyPressMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "up", "k":
			m.selected, m.offset = max(m.selected-1, -1), 0
		case "down", "j":
			m.selected, m.offset = min(m.selected+1, len(m.status.Services)-1), 0
		case "a", "esc":
			m.selected, m.offset = -1, 0
		case "pgup":
			m.offset += m.height
		case "pgdown":
			m.offset = max(m.offset-m.height, 0)
		case "home", "g":
			m.offset = len(m.lines)
		case "end", "G":
			m.offset = 0
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

	m.layout()

	return m, nil
}

// layout re-renders the header, table and footer, and gives the log pane what's left.
func (m *model) layout() {
	m.top = lipgloss.JoinVertical(lipgloss.Left, m.header(), m.table())
	m.bottom = m.footer()
	m.height = max(m.rows-lipgloss.Height(m.top)-lipgloss.Height(m.bottom), 1)
	m.fill()
}

// fill shows the selected service's lines, or everyone's, in the log pane. Only
// the visible lines are joined, and each is truncated once and cached.
func (m *model) fill() {
	width := max(m.width, 20)
	svc, filtered := m.current()
	var out []string
	for i := range m.lines {
		l := &m.lines[i]
		if filtered && l.name != svc.Name {
			continue
		}

		if l.width != width {
			l.width = width
			l.own = ansi.Truncate(l.text, width, "…")
			l.all = ansi.Truncate(ui.Prefix(l.name, l.style)+l.text, width, "…")
		}
		if filtered {
			out = append(out, l.own)
		} else {
			out = append(out, l.all)
		}
	}

	// Scrolled up, the pane stays on the same lines while new ones arrive below.
	if m.offset > 0 {
		m.offset += len(out) - m.shown
	}
	m.shown = len(out)
	m.offset = min(m.offset, max(len(out)-m.height, 0))

	end := len(out) - m.offset
	window := out[max(end-m.height, 0):end]
	for len(window) < m.height {
		window = append(window, "")
	}
	m.pane = strings.Join(window, "\n")
}

func (m *model) current() (control.Service, bool) {
	if m.selected < 0 || m.selected >= len(m.status.Services) {
		return control.Service{}, false
	}

	return m.status.Services[m.selected], true
}

func (m *model) header() string {
	header := ui.Accent.Render("portico") + ui.Faint.Render("  http://"+m.opts.Addr)
	if m.status.URL != "" {
		header += ui.Faint.Render("  ·  ") + ui.Accent.Render(m.status.URL)
	}

	return header
}

func (m *model) table() string {
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

	return t.String()
}

func (m *model) footer() string {
	showing := "all services"
	if svc, ok := m.current(); ok {
		showing = svc.Name
	}

	footer := ui.Faint.Render("↑/↓ select · a all · r restart · pgup/pgdn scroll · q quit  ·  showing " + showing)
	if m.note != "" {
		footer += ui.Faint.Render("  ·  ") + m.note
	}

	return footer
}

func (m *model) View() tea.View {
	v := tea.NewView(m.top + "\n" + m.pane + "\n" + m.bottom)
	v.AltScreen = true

	return v
}
