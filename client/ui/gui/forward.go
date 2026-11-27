package gui

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/ui/service"
)

// WP7: loopback port forwarding modal ("打开本地页面…") with live loopback
// listener discovery, multiple concurrent custom port forwards, and separated
// tunnel/remote/health step reporting.

type forwardItem struct {
	id      string
	busy    bool
	active  bool
	display string
	steps   []service.EntryStatus
	err     error
	cancel  context.CancelFunc
}

type forwardState struct {
	open      bool
	input     string
	ports     []int
	detectJ   job
	detectErr error
	inputErr  error
	items     []*forwardItem
}

func (a *App) forwardOf(key string) *forwardState {
	if a.forwards == nil {
		a.forwards = map[string]*forwardState{}
	}
	st := a.forwards[key]
	if st == nil {
		st = &forwardState{}
		a.forwards[key] = st
	}
	return st
}

func (a *App) openForwardModal(row *agentRow) {
	key := row.Server + "/" + row.Instance
	st := a.forwardOf(key)
	st.open = true
	a.detectLoopbackPorts(row.Server, st)
}

func (a *App) detectLoopbackPorts(host string, st *forwardState) {
	if st.detectJ.busy {
		return
	}
	fn := a.loopbackListenersFn
	if fn == nil {
		fn = a.svc.LoopbackListeners
	}
	var ports []int
	a.start(&st.detectJ, 30*time.Second, func(ctx context.Context) error {
		var err error
		ports, err = fn(ctx, host)
		return err
	}, func(err error) {
		st.detectErr = err
		if err == nil {
			st.ports = ports
		}
	})
}

func (a *App) startForward(row *agentRow, st *forwardState, raw string) {
	ft, err := service.ParseForward(raw)
	if err != nil {
		st.inputErr = err
		return
	}
	st.inputErr = nil
	id := ft.Display
	var it *forwardItem
	for _, existing := range st.items {
		if existing.id == id {
			if existing.cancel != nil {
				existing.cancel()
			}
			it = existing
			break
		}
	}
	if it == nil {
		it = &forwardItem{id: id}
		st.items = append(st.items, it)
	}
	ctx, cancel := context.WithCancel(context.Background())
	it.cancel = cancel
	it.busy, it.active, it.err, it.steps, it.display = true, false, nil, nil, ft.Display
	fn := a.forwardPageFn
	if fn == nil {
		fn = a.svc.ForwardPage
	}
	a.run(func() {
		err := fn(ctx, row.Server, ft, service.OpenOptions{
			Idle: service.DefaultEntryIdle,
			Status: func(s service.EntryStatus) {
				a.update(func() { it.steps = append(it.steps, s) })
			},
			Ready: func(open, display string) {
				a.update(func() { it.busy, it.active, it.display = false, true, display })
				if err := a.openURL(open); err != nil {
					a.update(func() { it.err = errors.New(T("entry.no_browser", "url", display)) })
				}
			},
		})
		a.update(func() {
			it.busy, it.active = false, false
			if err != nil && ctx.Err() == nil {
				it.err = err
			}
		})
	})
}

func (a *App) stopForwardItem(st *forwardState, it *forwardItem) {
	if st == nil || it == nil {
		return
	}
	if it.cancel != nil {
		it.cancel()
		it.cancel = nil
	}
	it.busy, it.active = false, false
	out := st.items[:0]
	for _, x := range st.items {
		if x != it {
			out = append(out, x)
		}
	}
	st.items = out
}

func (a *App) stopForward(st *forwardState) {
	if st == nil {
		return
	}
	for _, it := range st.items {
		if it.cancel != nil {
			it.cancel()
			it.cancel = nil
		}
		it.busy, it.active = false, false
	}
	st.items = nil
}

func (a *App) closeForward(key string) {
	if st := a.forwards[key]; st != nil {
		a.stopForward(st)
	}
	delete(a.forwards, key)
}

func (a *App) forwardBanner(c *ui.Context, row *agentRow) {
	key := row.Server + "/" + row.Instance
	st := a.forwards[key]
	if st == nil || st.open || len(st.items) == 0 {
		return
	}
	for _, item := range st.items {
		it := item
		if !it.busy && !it.active {
			continue
		}
		reportPanel(ui.Row(c.Key("fwd-ban-" + it.id))).Gap(10).AlignItems(ui.Center).Wrap().Children(func() {
			if it.active {
				ui.Text(c, T("entry.opened", "url", it.display)).FontSize(13).Bold().Grow(1)
			} else {
				ui.Spinner(c).Size(14, 14)
				muted(ui.Text(c, T("entry.connecting"))).Grow(1)
			}
			quietButton(c.Key("fwd-mgr-"+it.id), T("forward.manage")).OnClick(func() { st.open = true })
			ui.Button(c.Key("fwd-stop-"+it.id), T("forward.stop")).OnClick(func() { a.stopForwardItem(st, it) })
		})
	}
}

func (a *App) forwardModal(c *ui.Context, row *agentRow) {
	key := row.Server + "/" + row.Instance
	st := a.forwards[key]
	if st == nil || !st.open {
		return
	}
	open := true
	if ui.Modal(c, &open, func() {
		dialog(ui.Column(c), 520).Children(func() {
			dialogTitle(ui.Text(c, T("forward.title")))
			muted(ui.Text(c, T("forward.hint"))).FontSize(fsSub).Wrap()
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				muted(ui.Text(c, T("forward.detected"))).FontSize(fsSub).Grow(1)
				if st.detectJ.busy {
					ui.Spinner(c).Size(14, 14)
				} else {
					quietButton(c, T("common.refresh")).OnClick(func() { a.detectLoopbackPorts(row.Server, st) })
				}
			})
			if len(st.ports) > 0 {
				ui.Row(c).Gap(6).Wrap().Children(func() {
					for _, p := range st.ports {
						port := p
						ui.Button(c.Key("fp-"+strconv.Itoa(port)), ":"+strconv.Itoa(port)).OnClick(func() {
							st.input = strconv.Itoa(port)
							st.inputErr = nil
						})
					}
				})
			} else if !st.detectJ.busy && st.detectErr == nil {
				muted(ui.Text(c, T("forward.no_ports"))).FontSize(fsCaption).Wrap()
			}
			labeled(c, T("forward.input_label"), func() {
				ui.TextInput(c, &st.input).Label(T("forward.input_label")).Placeholder(T("forward.placeholder"))
			})
			if st.inputErr != nil {
				errorBox(c, st.inputErr, a.fold("fwd-err-"+key))
			}
			for _, item := range st.items {
				it := item
				a.renderForwardItem(c, key, st, it)
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Justify(ui.End).Children(func() {
				ui.Button(c, T("common.close")).OnClick(func() { st.open = false })
				ui.PrimaryButton(c, T("forward.start")).
					Disabled(strings.TrimSpace(st.input) == "").
					OnClick(func() { a.startForward(row, st, st.input) })
			})
		})
	}).Dismissed() {
		st.open = false
	}
}

func (a *App) renderForwardItem(c *ui.Context, key string, st *forwardState, it *forwardItem) {
	subPanel(ui.Column(c.Key("fi-" + it.id))).Gap(6).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
			if it.active {
				ui.Text(c, T("entry.opened", "url", it.display)).FontSize(13).Bold().Grow(1)
			} else if it.busy {
				ui.Spinner(c).Size(14, 14)
				muted(ui.Text(c, T("entry.connecting"))).Grow(1)
			} else {
				muted(ui.Text(c, it.display)).FontSize(13).Grow(1)
			}
			if it.active || it.busy {
				ui.Button(c.Key("fi-stop-"+it.id), T("forward.stop")).OnClick(func() { a.stopForwardItem(st, it) })
			}
		})
		if len(it.steps) > 0 {
			ui.Row(c).Wrap().Gap(spGroup).AlignItems(ui.Center).Children(func() {
				for i, s := range it.steps {
					tone := "ok"
					if !s.OK {
						tone = "warn"
					}
					ui.Row(c.Key("fs-" + it.id + "-" + itoa(i))).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Box(c).Size(6, 6).Radius(3).Background(toneColor(tone))
						ui.Text(c, T(entrySteps[s.Step])).FontSize(12)
						if s.Detail != "" {
							muted(ui.Text(c, s.Detail)).MaxLines(1)
						}
					})
				}
			})
		}
		if it.err != nil {
			errorBox(c, it.err, a.fold("fwd-item-err-"+key+"-"+it.id))
		}
	})
}
