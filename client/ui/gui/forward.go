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
// listener discovery and separated tunnel/remote/health step reporting.

type forwardState struct {
	open      bool
	input     string
	ports     []int
	detectJ   job
	detectErr error
	busy      bool
	active    bool
	display   string
	steps     []service.EntryStatus
	err       error
	cancel    context.CancelFunc
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
		st.err = err
		return
	}
	if st.cancel != nil {
		st.cancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	st.cancel = cancel
	st.busy, st.active, st.err, st.steps, st.display = true, false, nil, nil, ft.Display
	fn := a.forwardPageFn
	if fn == nil {
		fn = a.svc.ForwardPage
	}
	a.run(func() {
		err := fn(ctx, row.Server, ft, service.OpenOptions{
			Idle: service.DefaultEntryIdle,
			Status: func(s service.EntryStatus) {
				a.update(func() { st.steps = append(st.steps, s) })
			},
			Ready: func(open, display string) {
				a.update(func() { st.busy, st.active, st.display = false, true, display })
				if err := a.openURL(open); err != nil {
					a.update(func() { st.err = errors.New(T("entry.no_browser", "url", display)) })
				}
			},
		})
		a.update(func() {
			st.busy, st.active = false, false
			if err != nil && ctx.Err() == nil {
				st.err = err
			}
		})
	})
}

func (a *App) stopForward(st *forwardState) {
	if st == nil {
		return
	}
	if st.cancel != nil {
		st.cancel()
		st.cancel = nil
	}
	st.busy, st.active = false, false
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
	if st == nil || st.open || (!st.busy && !st.active) {
		return
	}
	reportPanel(ui.Row(c)).Gap(10).AlignItems(ui.Center).Wrap().Children(func() {
		if st.active {
			ui.Text(c, T("entry.opened", "url", st.display)).FontSize(13).Bold().Grow(1)
		} else {
			ui.Spinner(c).Size(14, 14)
			muted(ui.Text(c, T("entry.connecting"))).Grow(1)
		}
		quietButton(c, T("forward.manage")).OnClick(func() { st.open = true })
		ui.Button(c, T("forward.stop")).OnClick(func() { a.stopForward(st) })
	})
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
							st.err = nil
						})
					}
				})
			} else if !st.detectJ.busy && st.detectErr == nil {
				muted(ui.Text(c, T("forward.no_ports"))).FontSize(fsCaption).Wrap()
			}
			labeled(c, T("forward.input_label"), func() {
				ui.TextInput(c, &st.input).Label(T("forward.input_label")).Placeholder(T("forward.placeholder"))
			})
			if st.err != nil {
				errorBox(c, st.err, a.fold("fwd-err-"+key))
			}
			if st.busy || st.active || len(st.steps) > 0 {
				subPanel(ui.Column(c)).Gap(6).Children(func() {
					if st.active {
						ui.Text(c, T("entry.opened", "url", st.display)).FontSize(13).Bold()
					} else if st.busy {
						ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
							ui.Spinner(c).Size(14, 14)
							muted(ui.Text(c, T("entry.connecting")))
						})
					}
					if len(st.steps) > 0 {
						ui.Row(c).Wrap().Gap(spGroup).AlignItems(ui.Center).Children(func() {
							for i, s := range st.steps {
								tone := "ok"
								if !s.OK {
									tone = "warn"
								}
								ui.Row(c.Key("fs-" + itoa(i))).Gap(8).AlignItems(ui.Center).Children(func() {
									ui.Box(c).Size(6, 6).Radius(3).Background(toneColor(tone))
									ui.Text(c, T(entrySteps[s.Step])).FontSize(12)
									if s.Detail != "" {
										muted(ui.Text(c, s.Detail)).MaxLines(1)
									}
								})
							}
						})
					}
				})
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Justify(ui.End).Children(func() {
				if st.active || st.busy {
					ui.Button(c, T("forward.stop")).OnClick(func() { a.stopForward(st) })
				}
				ui.Button(c, T("common.close")).OnClick(func() { st.open = false })
				ui.PrimaryButton(c, T("forward.start")).
					Disabled(st.busy || strings.TrimSpace(st.input) == "").
					OnClick(func() { a.startForward(row, st, st.input) })
			})
		})
	}).Dismissed() {
		st.open = false
	}
}
