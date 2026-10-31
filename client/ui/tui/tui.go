// Package tui is `valise tui`: a terminal UI over the service layer
// (docs/UI.md section 8). It holds no business logic: every action calls
// the service layer, and steps that need the raw terminal (login relay,
// web entry, passphrase prompts) run the matching CLI command while the
// TUI is paused.
package tui

import (
	"context"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"

	"valise/core/valise"
	"valise/ui/service"
)

// Options configure the TUI.
type Options struct {
	Service *service.Service
	// Command returns this valise binary with the same --config-dir and the
	// given arguments, for steps run while the TUI is paused.
	Command func(args ...string) *exec.Cmd
}

// screen is one view on the stack. update reports whether it consumed the
// message; unconsumed keys fall back to the global bindings (esc, q).
type screen interface {
	title() string
	help() string
	init(m *Model) tea.Cmd
	update(m *Model, msg tea.Msg) (tea.Cmd, bool)
	view(m *Model) string
}

// Model is the root of the TUI.
type Model struct {
	opt      Options
	svc      *service.Service
	ctx      context.Context
	stack    []screen
	width    int
	height   int
	flash    string
	flashErr bool
	// send delivers a message from a background goroutine (the revive
	// confirmation); set by Run.
	send func(tea.Msg)
}

// New returns the TUI model starting at the server overview.
func New(ctx context.Context, opt Options) *Model {
	m := &Model{opt: opt, svc: opt.Service, ctx: ctx, width: 100, height: 30, send: func(tea.Msg) {}}
	m.stack = []screen{newOverview()}
	return m
}

// Run starts the TUI and blocks until it quits.
func Run(ctx context.Context, opt Options) error {
	m := New(ctx, opt)
	p := tea.NewProgram(m, tea.WithContext(ctx))
	m.send = p.Send
	_, err := p.Run()
	return err
}

func (m *Model) top() screen { return m.stack[len(m.stack)-1] }

// push opens a screen on top of the stack.
func (m *Model) push(s screen) tea.Cmd {
	m.stack = append(m.stack, s)
	m.flash = ""
	return s.init(m)
}

// pop closes the top screen; the overview is never popped.
func (m *Model) pop() {
	if len(m.stack) > 1 {
		m.stack = m.stack[:len(m.stack)-1]
	}
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd { return m.top().init(m) }

// errMsg reports a failed background action on the status line.
type errMsg struct{ err error }

// flashMsg reports a finished background action on the status line.
type flashMsg string

func failed(err error) tea.Msg { return errMsg{err} }

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case errMsg:
		m.flash, m.flashErr = errText(msg.err), true
	case flashMsg:
		m.flash, m.flashErr = string(msg), false
	case tea.KeyPressMsg:
		if msg.String() == "ctrl+c" {
			return m, tea.Quit
		}
	}
	cmd, handled := m.top().update(m, msg)
	if handled {
		return m, cmd
	}
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.String() {
		case "esc":
			m.pop()
		case "q":
			if len(m.stack) == 1 {
				return m, tea.Quit
			}
			m.pop()
		}
	}
	return m, cmd
}

// View implements tea.Model.
func (m *Model) View() tea.View {
	var b strings.Builder
	var crumbs []string
	for _, s := range m.stack {
		crumbs = append(crumbs, s.title())
	}
	b.WriteString(titleStyle.Render("Valise") + " " + dimStyle.Render(strings.Join(crumbs, " › ")) + "\n\n")
	b.WriteString(m.top().view(m))
	b.WriteString("\n")
	if m.flash != "" {
		st := okStyle
		if m.flashErr {
			st = errStyle
		}
		b.WriteString("\n" + st.Render(m.flash))
	}
	b.WriteString("\n" + dimStyle.Render(m.top().help()+"  ·  esc 返回  ·  ctrl+c 退出"))
	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}

// errText renders a service error as "reason + suggestion"; technical
// details stay folded (they are in the CLI with -v).
func errText(err error) string {
	if ue, ok := valise.As(err); ok && ue != nil {
		s := "失败：" + ue.Reason
		if ue.Suggestion != "" {
			s += "\n建议：" + ue.Suggestion
		}
		return s
	}
	return "发生了未预期的错误：" + err.Error()
}

// execDone is sent when a CLI step run while paused has finished.
type execDone struct{ err error }

// runCLI pauses the TUI and runs `valise <args>` in the terminal.
func (m *Model) runCLI(args ...string) tea.Cmd {
	if m.opt.Command == nil {
		return func() tea.Msg {
			return flashMsg("这个操作需要在终端中运行：valise " + strings.Join(args, " "))
		}
	}
	return tea.ExecProcess(m.opt.Command(args...), func(err error) tea.Msg { return execDone{err} })
}
