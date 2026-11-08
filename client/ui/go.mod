module valise/ui

go 1.27.3

require (
	charm.land/bubbles/v2 v2.2.1
	charm.land/bubbletea/v2 v2.1.1
	charm.land/lipgloss/v2 v2.0.6
	filippo.io/age v1.3.1
	github.com/charmbracelet/ultraviolet v0.0.0-20261008173134-6b8d4baf91b4
	github.com/charmbracelet/x/ansi v0.11.9
	github.com/egoist/mygo v0.7.3
	golang.org/x/term v0.46.0
	valise/agentkit v0.0.0
	valise/client v0.0.0
	valise/core v0.0.0
	valise/testbed v0.0.0
)

require (
	filippo.io/hpke v0.4.0 // indirect
	github.com/atotto/clipboard v0.1.4 // indirect
	github.com/charmbracelet/colorprofile v0.4.3 // indirect
	github.com/charmbracelet/x/term v0.2.2 // indirect
	github.com/charmbracelet/x/termios v0.1.1 // indirect
	github.com/charmbracelet/x/windows v0.2.2 // indirect
	github.com/clipperhouse/displaywidth v0.11.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.7.0 // indirect
	github.com/ebitengine/purego v0.11.1 // indirect
	github.com/go-text/typesetting v0.3.5 // indirect
	github.com/klauspost/compress v1.20.3 // indirect
	github.com/lucasb-eyer/go-colorful v1.4.1 // indirect
	github.com/mattn/go-runewidth v0.0.30 // indirect
	github.com/muesli/cancelreader v0.2.2 // indirect
	github.com/restic/chunker v0.5.0 // indirect
	github.com/rivo/uniseg v0.4.7 // indirect
	github.com/xo/terminfo v1.0.0 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/crypto v0.57.0 // indirect
	golang.org/x/image v0.46.0 // indirect
	golang.org/x/net v0.59.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.43.0 // indirect
)

replace (
	valise/client => ..
	valise/core => ../../core
	valise/testbed => ../../testbed
)

replace valise/agentkit => ../../agent-kit

tool github.com/egoist/mygo/cmd/mygo
