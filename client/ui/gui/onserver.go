package gui

import (
	"context"
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/ui/service"
)

// The agents on one server (docs/UI.md 7.5): no agent is presumed. What
// was found and recognized can be adopted; a harness the profiles do not
// know points to the guide; with none, the first agent can be installed.

// serverScan is one look at a server's agents, and each adoption.
type serverScan struct {
	busy   bool
	found  []service.DetectedInstance
	err    error
	adopts map[string]*adoption // by candidate id
}

// adoption is one agent being adopted: the report first, then accepting.
type adoption struct {
	busy    bool
	res     *service.EnrollResult
	err     error
	assign  map[string]string // "<root>:<prefix>" → category; absent: kept as sensitive
	written bool
}

// installable is a harness with an official install plan.
type installable struct{ ID, Name, Takes, Needs string }

// guideURL is the beginner guide to writing a profile (PROFILES.md 附录 A).
const guideURL = "https://github.com/NazukiMio/Valise/blob/main/docs/PROFILES.md#附录-a为-harness-写一份资料入门2026-11-08"

func (a *App) scanServer(host string) {
	sc := a.scans[host]
	if sc == nil {
		sc = &serverScan{adopts: map[string]*adoption{}}
		a.scans[host] = sc
	}
	sc.busy, sc.err = true, nil
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		found, err := a.svc.DetectInstances(ctx, host)
		a.update(func() { sc.busy, sc.found, sc.err = false, found, err })
	})
}

func (a *App) onServerPage(c *ui.Context, host string) {
	sc := a.scans[host]
	if sc == nil {
		a.scanServer(host)
		sc = a.scans[host]
	}
	if a.wiz != nil && a.wiz.host == host {
		stepper(c, len(wizardSteps)-1)
	} else {
		ui.Link(c, "‹ "+host, "/servers/"+host)
	}
	ui.Row(c).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Children(func() {
			pageHead(c, "这台服务器上的 Agent", "在「"+host+"」上按资料包认识的 harness 找了一遍。")
		})
		if sc.busy {
			ui.Spinner(c)
		} else {
			ui.Button(c, "重新扫描").OnClick(func() { a.scanServer(host) })
		}
	})
	if sc.err != nil {
		errorBox(c, sc.err, a.fold("scan-err-"+host))
		return
	}
	if sc.busy && sc.found == nil {
		return
	}
	if len(sc.found) > 0 {
		section(c, "找到了认识的 Agent")
		for _, d := range sc.found {
			a.foundCard(c.Key(d.ID+d.StateDir), host, sc, d)
		}
	}
	section(c, "资料包不认识的 harness")
	card(ui.Row(c)).Gap(14).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			ui.Text(c, "你的 Agent 不在上面？").Bold()
			muted(ui.Text(c, "Valise 只认资料包里登记过的 harness。还没登记的，可以自己为它写一份资料，写好后回来重新扫描。")).FontSize(13).Wrap()
		})
		ui.Link(c, "怎么为它写一份资料 ›", guideURL)
	})
	if len(sc.found) == 0 {
		a.installTiles(c, host)
	}
}

func section(c *ui.Context, title string) {
	muted(ui.Text(c, title)).FontSize(12).LetterSpacing(0.6)
}

// foundCard is one recognized agent: already adopted, or the report and
// accepting it.
func (a *App) foundCard(c *ui.Context, host string, sc *serverScan, d service.DetectedInstance) {
	ad := sc.adopts[d.ID]
	card(ui.Column(c)).Gap(10).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			a.avatar(c, agentRow{Server: host, Instance: orText(d.Enrolled, d.ID), Profile: d.ProfileID, Name: a.harness[d.ProfileID]}, 36)
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				name := orText(a.harness[d.ProfileID], d.ProfileID)
				if d.Name != "" {
					name += " · " + d.Name
				}
				ui.Text(c, name).FontSize(15).Bold()
				line := d.StateDir
				if d.Version != "" {
					line += " · " + d.Version
				}
				mono(muted(ui.Text(c, line))).MaxLines(1)
			})
			switch {
			case d.Enrolled != "" || ad != nil && ad.written:
				ui.Text(c, "已接入").FontSize(12).TextColor(palette.Ok)
				ui.Button(c, "查看").OnClick(func() { a.router.Push("/agents/" + host + "/" + orText(d.Enrolled, d.ID)) })
			case ad != nil && ad.busy:
				ui.Spinner(c)
			case ad == nil || ad.res == nil:
				ui.PrimaryButton(c, "接入…").OnClick(func() { a.adopt(host, sc, d, false) })
			}
		})
		if ad == nil {
			return
		}
		if ad.err != nil {
			errorBox(c, ad.err, a.fold("adopt-err-"+host+d.ID))
		}
		if ad.res != nil && ad.res.Report != nil && !ad.written {
			a.adoptReport(c, host, sc, d, ad)
		}
	})
}

func (a *App) adopt(host string, sc *serverScan, d service.DetectedInstance, accept bool) {
	ad := sc.adopts[d.ID]
	if ad == nil {
		ad = &adoption{assign: map[string]string{}}
		sc.adopts[d.ID] = ad
	}
	ad.busy, ad.err = true, nil
	opts := service.EnrollOptions{ProfileID: d.ProfileID, Name: d.Name, InstanceID: d.ID, Accept: accept}
	for k, cat := range ad.assign {
		opts.Assign = append(opts.Assign, assignRule(k, cat))
	}
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		res, err := a.svc.Enroll(ctx, host, opts)
		a.update(func() {
			ad.busy, ad.err = false, err
			if err != nil {
				return
			}
			ad.res = res
			if accept && res.Written != "" {
				ad.written = true
				a.refreshAgents()
			}
		})
	})
}

// assignRule is one decision as Enroll takes it: a folder group covers
// everything under it.
func assignRule(key, cat string) string {
	glob := key
	if len(key) > 0 && key[len(key)-1] == '/' {
		glob += "**"
	}
	return glob + "=" + cat
}

// categoryChoices are what an unrecognized file can be filed as; the first
// is the default, keeping it with the secrets (ARCHITECTURE.md 5).
var categoryChoices = []struct{ id, label string }{
	{"", "按敏感内容保留"}, {"memory", "记忆"}, {"config", "配置"}, {"sessions", "会话"}, {"state", "状态"},
	{"workspace", "工作区"}, {"cache", "缓存（不备份）"}, {"ignore", "忽略（不备份）"},
}

func (a *App) adoptReport(c *ui.Context, host string, sc *serverScan, d service.DetectedInstance, ad *adoption) {
	r := ad.res.Report
	ui.Column(c).Gap(8).Padding(12, 14).Radius(10).Background(palette.Board).Children(func() {
		var found int
		for _, t := range r.Categories {
			found += t.Files
		}
		line := fmt.Sprintf("找到 %d 个文件，已按资料归类", found)
		if r.Unclassified.Files > 0 {
			line += fmt.Sprintf("；%d 个文件用途尚未识别，默认按敏感内容保留", r.Unclassified.Files)
		}
		ui.Text(c, line).FontSize(13).Wrap()
		if len(r.Secrets) > 0 {
			muted(ui.Text(c, fmt.Sprintf("其中 %d 个是凭据文件（只列路径，不读内容）", len(r.Secrets))))
		}
		if ad.res.ProfileLocal {
			ui.Text(c, "这份资料来自你自己的电脑，未经审核").FontSize(12).TextColor(palette.Warn)
		}
		if len(r.Pending) > 0 {
			ui.Text(c, "此环境恢复尚未测试：资料里有未核实的条目").FontSize(12).TextColor(palette.Warn)
		}
		if len(r.UnclassGroups) > 0 {
			ui.Collapsible(c, "逐项改归类", a.fold("assign-"+host+d.ID), func() {
				labels := make([]string, len(categoryChoices))
				for i, ch := range categoryChoices {
					labels[i] = ch.label
				}
				for _, g := range r.UnclassGroups {
					key := g.Root + ":" + g.Prefix
					ui.Row(c.Key(key)).Gap(10).AlignItems(ui.Center).Children(func() {
						mono(ui.Text(c, g.Root+"/"+g.Prefix)).FontSize(12).Grow(1).MaxLines(1)
						muted(ui.Text(c, fmt.Sprintf("%d 个 · %s", g.Files, size(g.Bytes))))
						choice := labelOf(ad.assign[key])
						if ui.Select(c, &choice, labels).Width(160).Changed() {
							if id := idOf(choice); id == "" {
								delete(ad.assign, key)
							} else {
								ad.assign[key] = id
							}
						}
					})
				}
			})
		}
		muted(ui.Text(c, "接入只读取和记录，不改动 Agent 的任何文件，也不往 harness 里安装任何东西。")).Wrap()
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			ui.Button(c, "取消").OnClick(func() { delete(sc.adopts, d.ID) })
			ui.PrimaryButton(c, "确认接入").OnClick(func() { a.adopt(host, sc, d, true) })
		})
	})
}

func labelOf(id string) string {
	for _, ch := range categoryChoices {
		if ch.id == id {
			return ch.label
		}
	}
	return categoryChoices[0].label
}

func idOf(label string) string {
	for _, ch := range categoryChoices {
		if ch.label == label {
			return ch.id
		}
	}
	return ""
}

// installTiles offer the harnesses with an official install plan.
func (a *App) installTiles(c *ui.Context, host string) {
	section(c, "这台服务器上还没有 Agent")
	muted(ui.Text(c, "可以在这里装第一个，也可以先去服务器上自己装好再回来重新扫描。")).FontSize(13).Wrap()
	if len(a.installable) == 0 {
		muted(ui.Text(c, "资料包里还没有带官方安装方案的 harness。"))
		return
	}
	ui.Grid(c).Columns(3).Gap(12).Children(func() {
		for _, h := range a.installable {
			t := card(ui.Column(c.Key(h.ID))).Gap(8).Cursor(ui.CursorPointer).Role(ui.RoleButton).Label("安装 " + h.Name)
			if t.Hovered() {
				t.Border(1, palette.Brand)
			}
			t.OnClick(func() { a.router.Push("/servers/" + host + "/install/" + h.ID) }).Children(func() {
				ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
					a.avatar(c, agentRow{Profile: h.ID, Name: h.Name}, 28)
					ui.Text(c, h.Name).Bold()
				})
				line := "官方安装方案"
				if h.Takes != "" {
					line += " · 约 " + h.Takes
				}
				if h.Needs != "" {
					line += " · 需要 " + h.Needs
				}
				muted(ui.Text(c, line)).Wrap()
			})
		}
	})
}
