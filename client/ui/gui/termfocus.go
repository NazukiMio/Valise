package gui

import "github.com/egoist/mygo/ui"

// modalOpen reports whether a global overlay modal (PIN, passphrase, server
// password, or key dialog) is open above the active page or terminal.
func (a *App) modalOpen() bool {
	return len(a.asks) > 0 || a.keyFlow != nil
}

// focusTerminal restores keyboard focus to an embedded terminal view once no
// overlay modal is open. While a modal is open, want is armed so focus returns
// on the first frame after the modal closes without scrolling the page.
func focusTerminal(_ *ui.Context, el ui.Element, want *bool, modalOpen bool) {
	if want == nil {
		return
	}
	if modalOpen {
		*want = true
		return
	}
	if *want {
		el.Focus(ui.FocusOptions{PreventScroll: true})
		*want = false
	}
}
