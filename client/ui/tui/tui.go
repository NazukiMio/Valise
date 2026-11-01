// Package tui is `valise tui`: a terminal UI over the service layer
// (docs/UI.md sections 8 and 9). It holds no business logic: every action
// calls the service layer, and steps that need the raw terminal (login,
// the management page, a server shell, passphrase prompts) run the
// matching CLI command while the TUI is paused.
package tui

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"valise/client/profiles"
	"valise/core/enroll"
	"valise/core/profile"
	"valise/core/valise"
	"valise/core/vaultrpc"
	"valise/ui/service"
)

// Options configure the TUI.
type Options struct {
	Service *service.Service
	// Command returns this valise binary with the same --config-dir and the
	// given arguments, for steps run while the TUI is paused.
	Command func(args ...string) *exec.Cmd
}

// screen is one page on the stack. update reports whether it consumed the
// message; unconsumed keys fall back to the global bindings (esc, q).
type screen interface {
	title() string
	help() string
	init(m *Model) tea.Cmd
	update(m *Model, msg tea.Msg) (tea.Cmd, bool)
	view(m *Model) string
}

// srvData is what the TUI knows about one recorded server.
type srvData struct {
	host      string
	loading   bool
	loaded    bool
	err       error // the server could not be reached
	instances []enroll.InstanceSummary
	status    *vaultrpc.StatusResult
}

// lastSeal returns the newest snapshot of an instance.
func (d *srvData) lastSeal(inst string) (time.Time, bool) {
	var t time.Time
	if d.status == nil {
		return t, false
	}
	for _, s := range d.status.Snapshots {
		if s.InstanceID == inst && s.CreatedAt.After(t) {
			t = s.CreatedAt
		}
	}
	return t, !t.IsZero()
}

// synced reports whether a snapshot has reached any cloud backup.
func (d *srvData) synced(sid string) bool {
	if d.status == nil {
		return false
	}
	for _, t := range d.status.Targets {
		for _, s := range t.LastSynced {
			if s == sid {
				return true
			}
		}
	}
	return false
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
	data     map[string]*srvData
	prof     *profiles.Set
	now      func() time.Time
	// send delivers a message from a background goroutine (the landing
	// confirmation); set by Run.
	send func(tea.Msg)
	// tick is how often the top bar redraws for the admin window; 0 off.
	tick time.Duration
	// animOn: an animation frame tick is pending (anim.go).
	animOn bool
}

// New returns the TUI model: the first-run guide when there is no key,
// otherwise the home page.
func New(ctx context.Context, opt Options) *Model {
	m := &Model{opt: opt, svc: opt.Service, ctx: ctx, width: 100, height: 32, send: func(tea.Msg) {},
		data: map[string]*srvData{}, now: time.Now, tick: 30 * time.Second}
	m.prof, _ = opt.Service.Profiles()
	if _, err := opt.Service.KeyShow(); err != nil {
		m.stack = []screen{newWizard()}
	} else {
		m.stack = []screen{newHome()}
	}
	return m
}

// Run starts the TUI and blocks until it quits.
func Run(ctx context.Context, opt Options) error {
	m := New(ctx, opt)
	p := tea.NewProgram(m, tea.WithContext(ctx))
	m.send = p.Send
	// Admin logins (docs/ARCHITECTURE.md 2.1): the admin passphrase is
	// asked in the TUI; OpenSSH cannot ask for a server password while the
	// TUI owns the terminal.
	opt.Service.NoTerminalLogin = true
	opt.Service.AdminAuth.Passphrase = func(host string) (string, error) { return askAdminPassphrase(m.send, host) }
	defer opt.Service.Close()
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

// pop closes the top screen; the root is never popped.
func (m *Model) pop() {
	if len(m.stack) > 1 {
		m.stack = m.stack[:len(m.stack)-1]
	}
}

// replaceRoot clears the stack to one screen (after the first-run guide).
func (m *Model) replaceRoot(s screen) tea.Cmd {
	m.stack = []screen{s}
	return s.init(m)
}

// display returns a profile's card look, nil when unknown.
func (m *Model) display(profileID string) *profile.Display {
	if m.prof == nil {
		return nil
	}
	if l := m.prof.ByID[profileID]; l != nil {
		return l.Effective.Display
	}
	return nil
}

// srvLoaded carries one server's data.
type srvLoaded struct{ d *srvData }

// server returns the cached data of a host (possibly not loaded yet).
func (m *Model) server(host string) *srvData {
	d := m.data[host]
	if d == nil {
		d = &srvData{host: host}
		m.data[host] = d
	}
	return d
}

// loadServer refreshes one server in the background.
func (m *Model) loadServer(host string) tea.Cmd {
	d := m.server(host)
	d.loading = true
	ctx, svc := m.ctx, m.svc
	return func() tea.Msg {
		nd := &srvData{host: host, loaded: true}
		nd.instances, nd.err = svc.Instances(ctx, host)
		if nd.err == nil {
			nd.status, _ = svc.VaultStatus(ctx, host)
		}
		return srvLoaded{nd}
	}
}

// Init implements tea.Model.
func (m *Model) Init() tea.Cmd { return tea.Batch(m.top().init(m), m.tickAdmin()) }

// adminTick redraws the top bar so the admin window's minutes stay true.
type adminTick struct{}

func (m *Model) tickAdmin() tea.Cmd {
	if m.tick <= 0 {
		return nil
	}
	return tea.Tick(m.tick, func(time.Time) tea.Msg { return adminTick{} })
}

// errMsg reports a failed background action on the status line.
type errMsg struct{ err error }

// flashMsg reports a finished background action on the status line.
type flashMsg string

func failed(err error) tea.Msg { return errMsg{err} }

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(animTick); ok {
		m.animOn = false
	}
	_, cmd := m.update(msg)
	return m, tea.Batch(cmd, m.ensureAnim())
}

func (m *Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case errMsg:
		m.flash, m.flashErr = errText(msg.err), true
	case flashMsg:
		m.flash, m.flashErr = string(msg), false
	case srvLoaded:
		m.data[msg.d.host] = msg.d
	case pushMsg:
		return m, m.push(msg.s)
	case refreshed:
		m.flash, m.flashErr = msg.text, false
		return m, m.loadServer(msg.host)
	case adminTick:
		return m, m.tickAdmin()
	case tea.KeyPressMsg:
		if msg.String() == "L" && !m.typing() && m.accessState() != "" && m.svc.FullAccessHost() == "" {
			if _, ok := m.svc.AdminRemaining(m.currentHost()); ok {
				m.svc.LockAdmin()
				m.flash, m.flashErr = "管理权限已锁定；下一次管理操作会再询问管理口令", false
				return m, nil
			}
		}
		if msg.String() == "U" && !m.typing() {
			return m, m.unlockAdmin()
		}
		if msg.String() == "ctrl+c" {
			for _, s := range m.stack {
				if b, ok := s.(interface{ busy() string }); ok && b.busy() != "" {
					m.flash, m.flashErr = b.busy(), true
					return m, nil
				}
			}
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
		}
	}
	return m, cmd
}

// bodyHeight is the number of lines a screen may use.
func (m *Model) bodyHeight() int {
	h := m.height - 6
	if m.flash != "" {
		h -= strings.Count(m.flash, "\n") + 2
	}
	return max(3, h)
}

// View implements tea.Model.
func (m *Model) View() tea.View {
	var crumbs []string
	for _, s := range m.stack {
		crumbs = append(crumbs, s.title())
	}
	if len(crumbs) > 0 {
		crumbs[len(crumbs)-1] = headStyle.Render(crumbs[len(crumbs)-1])
	}
	var b strings.Builder
	left := titleStyle.Render("◢ Valise") + "  " + dimStyle.Render(strings.Join(crumbs, " › "))
	b.WriteString(topBar(left, m.accessState(), m.width) + "\n\n")
	b.WriteString(clip(m.top().view(m), m.bodyHeight()))
	body := b.String()
	foot := wrapMenu(m.top().help(), m.width, len(m.stack) > 1)
	if pad := m.height - 2 - lipgloss.Height(foot) - lipgloss.Height(body); pad > 0 && m.flash == "" {
		body += strings.Repeat("\n", pad)
		// The flourish takes the last spare line, right-aligned near the
		// frame, with a blank line above it; otherwise it is left out.
		if f, ok := m.top().(flourisher); ok && pad >= 2 {
			if line := f.flourish(m); line != "" && lipgloss.Width(line)+2 <= m.width {
				lines := strings.Split(body, "\n")
				lines[len(lines)-1] = lipgloss.NewStyle().Width(min(m.width, 120) - 1).Align(lipgloss.Right).Render(flourishStyle.Render(line))
				body = strings.Join(lines, "\n")
			}
		}
	}
	var f strings.Builder
	f.WriteString(body + "\n")
	if m.flash != "" {
		st := okStyle
		if m.flashErr {
			st = errStyle
		}
		f.WriteString("\n" + st.Render(m.flash) + "\n")
	}
	f.WriteString(dimStyle.Render(strings.Repeat("─", max(10, min(m.width, 120)))) + "\n" + foot)
	v := tea.NewView(f.String())
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

// runCLI pauses the TUI and runs `valise <args>` in the terminal, then
// waits for Enter so its output can be read before the TUI redraws.
func (m *Model) runCLI(args ...string) tea.Cmd {
	if m.opt.Command == nil {
		return func() tea.Msg {
			return flashMsg("这个操作需要在终端中运行：valise " + strings.Join(args, " "))
		}
	}
	return tea.Exec(&pausedStep{cmd: m.opt.Command(args...)}, func(err error) tea.Msg { return execDone{err} })
}

// pausedStep runs a CLI command on the real terminal and holds its output
// on screen until Enter.
type pausedStep struct {
	cmd    *exec.Cmd
	stdin  io.Reader
	stdout io.Writer
}

func (p *pausedStep) SetStdin(r io.Reader)  { p.stdin = r; p.cmd.Stdin = r }
func (p *pausedStep) SetStdout(w io.Writer) { p.stdout = w; p.cmd.Stdout = w }
func (p *pausedStep) SetStderr(w io.Writer) { p.cmd.Stderr = w }

func (p *pausedStep) Run() error {
	err := p.cmd.Run()
	if p.stdout != nil && p.stdin != nil {
		fmt.Fprint(p.stdout, "\n[按回车返回 Valise] ")
		bufio.NewReader(p.stdin).ReadString('\n')
	}
	return err
}
