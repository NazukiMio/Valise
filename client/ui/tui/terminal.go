package tui

import (
	"bufio"
	tea "charm.land/bubbletea/v2"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"valise/core/valise"
	"valise/ui/service"
)

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
type execDone struct {
	err  error
	then tea.Cmd
}

// runCLI pauses the TUI and runs `valise <args>` in the terminal, then
// waits for Enter so its output can be read before the TUI redraws.
func (m *Model) runCLI(args ...string) tea.Cmd {
	if m.opt.Command == nil {
		return func() tea.Msg {
			return flashMsg("这个操作需要在终端中运行：valise " + strings.Join(args, " "))
		}
	}
	return tea.Exec(&pausedStep{cmd: m.opt.Command(args...)}, func(err error) tea.Msg { return execDone{err: err} })
}

// runCLIAccepted is runCLI after the user accepted a stale-backup warning
// here: the CLI shows it again but does not ask a second time (the gate
// itself still runs there).
func (m *Model) runCLIAccepted(args ...string) tea.Cmd {
	return m.cliThen(nil, true, args...)
}

// cliThen is runCLI (runCLIAccepted when accepted) followed by next once
// the command has finished: tea.Sequence would start next while the
// command still runs, and a refresh would read the server too early.
func (m *Model) cliThen(next tea.Cmd, accepted bool, args ...string) tea.Cmd {
	if m.opt.Command == nil {
		return m.runCLI(args...)
	}
	cmd := m.opt.Command(args...)
	if accepted {
		cmd.Env = append(cmd.Environ(), service.StaleAcceptedEnv+"=1")
	}
	if m.riskOnce {
		// Accepted on the gate's confirmation, for this command only.
		cmd.Env = append(cmd.Environ(), service.RiskAcceptedEnv+"=1")
		m.riskOnce = false
	}
	return tea.Exec(&pausedStep{cmd: cmd}, func(err error) tea.Msg { return execDone{err, next} })
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
