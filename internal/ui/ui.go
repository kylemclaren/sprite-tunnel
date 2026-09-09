// Package ui keeps terminal rendering separate from tunnel and installation logic.
package ui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

var purple = lipgloss.NewStyle().Foreground(lipgloss.Color("141"))
var green = lipgloss.NewStyle().Foreground(lipgloss.Color("84"))
var dim = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))

type event struct{ text string }
type finish struct{ err error }
type model struct {
	spinner       spinner.Model
	title, status string
	started       time.Time
	finished      bool
	err           error
}

func (m model) Init() tea.Cmd { return m.spinner.Tick }
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case event:
		m.status = v.text
	case finish:
		m.finished = true
		m.err = v.err
		return m, tea.Quit
	}
	var cmd tea.Cmd
	m.spinner, cmd = m.spinner.Update(msg)
	return m, cmd
}
func (m model) View() string {
	var b strings.Builder
	b.WriteString(purple.Bold(true).Render("✦ SPRITE TUNNEL") + dim.Render("  /  "+m.title) + "\n\n")
	icon := m.spinner.View()
	if m.finished || strings.HasPrefix(m.status, "tunnel up:") {
		icon = green.Render("✓")
		if m.err != nil {
			icon = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Render("✕")
		}
	}
	status := m.status
	if m.finished && m.err != nil {
		status = m.err.Error()
	}
	b.WriteString(icon + " " + status + "\n")
	footer := time.Since(m.started).Round(time.Second).String() + " elapsed"
	if !m.finished {
		footer += "  •  ctrl+c to stop"
	}
	b.WriteString(dim.Render(footer))
	return b.String() + "\n"
}

type UI struct {
	program *tea.Program
	done    chan struct{}
}

// Start reuses the display owned by the outer command. Only the owner finishes it.
func Start(ctx context.Context, title string) (context.Context, *UI, func(error)) {
	if display, ok := ctx.Value(displayKey{}).(*UI); ok {
		return ctx, display, func(error) {}
	}
	display := New(title)
	return context.WithValue(ctx, displayKey{}, display), display, display.Finish
}

type displayKey struct{}

func New(title string) *UI {
	u := &UI{}
	if term.IsTerminal(int(os.Stderr.Fd())) && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb" {
		s := spinner.New()
		s.Spinner = spinner.Dot
		s.Style = purple
		u.program = tea.NewProgram(model{spinner: s, title: title, started: time.Now(), status: "getting ready"}, tea.WithInput(nil), tea.WithOutput(os.Stderr), tea.WithoutSignalHandler())
		u.done = make(chan struct{})
		go func() { defer close(u.done); _, _ = u.program.Run() }()
	}
	return u
}
func (u *UI) Step(s string) {
	if u.program != nil {
		u.program.Send(event{s})
	} else {
		fmt.Fprintln(os.Stderr, s)
	}
}
func (u *UI) Write(p []byte) (int, error) { u.Step(strings.TrimSpace(string(p))); return len(p), nil }
func (u *UI) Finish(err error) {
	if u.program != nil {
		u.program.Send(finish{err})
		<-u.done
	}
}
