package tui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
)

// confirmScreen asks a yes/no question; yes runs onYes after closing.
type confirmScreen struct {
	question string
	detail   []string
	onYes    func(m *Model) tea.Cmd
}

func (c *confirmScreen) title() string         { return "确认" }
func (c *confirmScreen) help() string          { return menu("y", "确认", "n", "取消") }
func (c *confirmScreen) init(m *Model) tea.Cmd { return nil }
func (c *confirmScreen) view(m *Model) string {
	var b strings.Builder
	for _, d := range c.detail {
		b.WriteString(d + "\n")
	}
	if len(c.detail) > 0 {
		b.WriteString("\n")
	}
	b.WriteString(warnStyle.Render(c.question) + dimStyle.Render("  [y/N]"))
	return b.String()
}
func (c *confirmScreen) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil, false
	}
	switch k.String() {
	case "y", "Y":
		m.pop()
		return c.onYes(m), true
	case "n", "N", "esc", "q", "enter":
		m.pop()
		m.flash, m.flashErr = "已取消，没有做任何改动。", false
		return nil, true
	}
	return nil, true
}

func confirm(question string, detail []string, onYes func(m *Model) tea.Cmd) *confirmScreen {
	return &confirmScreen{question: question, detail: detail, onYes: onYes}
}

// inputScreen reads one line; secret input is masked and never kept after
// submit.
type inputScreen struct {
	label  string
	input  textinput.Model
	submit func(m *Model, value string) tea.Cmd
}

func newInput(label, placeholder string, secret bool, submit func(m *Model, value string) tea.Cmd) *inputScreen {
	ti := textinput.New()
	ti.Placeholder = placeholder
	ti.Prompt = "> "
	if secret {
		ti.EchoMode = textinput.EchoPassword
	}
	return &inputScreen{label: label, input: ti, submit: submit}
}

func (s *inputScreen) title() string { return s.label }
func (s *inputScreen) help() string  { return menu("enter", "确定") }
func (s *inputScreen) init(m *Model) tea.Cmd {
	s.input.Focus() // a steady cursor: no blink ticks
	return nil
}
func (s *inputScreen) view(m *Model) string { return s.label + "\n\n" + s.input.View() }
func (s *inputScreen) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.String() {
		case "enter":
			v := s.input.Value()
			s.input.Reset()
			m.pop()
			return s.submit(m, v), true
		case "esc":
			s.input.Reset()
			m.pop()
			return nil, true
		}
	}
	var cmd tea.Cmd
	s.input, cmd = s.input.Update(msg)
	_, isKey := msg.(tea.KeyPressMsg)
	return cmd, isKey
}

// choice is one option of a pickScreen.
type choice struct {
	label string
	value string
	desc  string
}

// pickScreen chooses one option from a list.
type pickScreen struct {
	label   string
	choices []choice
	cursor  int
	pick    func(m *Model, value string) tea.Cmd
}

func pick(label string, choices []choice, f func(m *Model, value string) tea.Cmd) *pickScreen {
	return &pickScreen{label: label, choices: choices, pick: f}
}

func (p *pickScreen) title() string         { return p.label }
func (p *pickScreen) help() string          { return menu("↑↓", "选择", "enter", "确定") }
func (p *pickScreen) init(m *Model) tea.Cmd { return nil }
func (p *pickScreen) view(m *Model) string {
	var b strings.Builder
	b.WriteString(p.label + "\n\n")
	var rows []string
	for i, c := range p.choices {
		row := cursorLine(i == p.cursor, c.label)
		if c.desc != "" {
			row += "  " + dimStyle.Render(c.desc)
		}
		rows = append(rows, row)
	}
	b.WriteString(scrollLines(rows, p.cursor, m.bodyHeight()-3) + "\n")
	if len(p.choices) == 0 {
		b.WriteString(dimStyle.Render("（没有可选项）"))
	}
	return b.String()
}
func (p *pickScreen) update(m *Model, msg tea.Msg) (tea.Cmd, bool) {
	k, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return nil, false
	}
	switch k.String() {
	case "up", "k", "down", "j", "pgup", "pgdown", "home", "end":
		p.cursor, _ = moveList(p.cursor, len(p.choices), k.String())
	case "enter":
		if len(p.choices) == 0 {
			return nil, true
		}
		m.pop()
		return p.pick(m, p.choices[p.cursor].value), true
	default:
		return nil, false
	}
	return nil, true
}
