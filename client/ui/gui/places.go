package gui

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/ui/service"
)

// The two minimal handling pages of docs/UI.md 7.3: one server that could
// not be read, and the backup locations on this computer.

// serverPage is one server: whether it was read this time, why not, and
// reconnecting.
func (a *App) serverPage(c *ui.Context, name string) {
	ui.Link(c, "‹ "+text.navServers, "/servers")
	pageHead(c, name, "")
	var obs *service.AgentServerObservation
	for i := range a.agents.value.Observations {
		if a.agents.value.Observations[i].ConnectionID == name {
			obs = &a.agents.value.Observations[i]
		}
	}
	card(ui.Column(c)).Gap(10).Children(func() {
		switch {
		case a.agents.busy:
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() { ui.Spinner(c); ui.Text(c, "正在连接 "+name+"…") })
		case obs == nil:
			muted(ui.Text(c, "这次还没有读取这台服务器。")).FontSize(14)
		case obs.Status == "fresh":
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(8, 8).Radius(4).Background(toneColor("ok"))
				ui.Text(c, "这次读到了 "+name+" 上的 Agent 清单").FontSize(15).Bold()
			})
			muted(ui.Text(c, "读取于 "+when(a.agents.at, c.Now())))
		default:
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(8, 8).Radius(4).Background(palette.Warn)
				ui.Text(c, name+" 这次没有读到").FontSize(15).Bold().TextColor(palette.Warn)
			})
			if obs.Error != nil {
				fact(c, "原因", obs.Error.Reason)
				if obs.Error.Suggestion != "" {
					fact(c, "建议", obs.Error.Suggestion)
				}
			} else {
				fact(c, "原因", "没有回应")
				fact(c, "建议", "检查服务器是否开机、网络是否通畅")
			}
			if !obs.LastAttemptAt.IsZero() {
				fact(c, "尝试时间", when(obs.LastAttemptAt, c.Now()))
			}
			if obs.ObservedAt != nil {
				fact(c, "上次读到", when(*obs.ObservedAt, c.Now()))
			}
		}
		if !a.agents.busy {
			ui.Row(c).Children(func() {
				ui.PrimaryButton(c, "重新连接").OnClick(func() { a.refreshAgents() })
			})
		}
	})
	if obs != nil && obs.Error != nil && obs.Error.Detail != "" {
		ui.Collapsible(c, text.techInfo, a.fold("srv-detail-"+name), func() {
			muted(ui.Text(c, obs.Error.Code+"\n"+obs.Error.Detail)).Font(cur.Fonts.Mono).Wrap()
		})
	}
}

// syncState is one “现在补到本机” and what came of it.
type syncState struct {
	busy   bool
	result *service.RepoSyncResult
	err    error
}

// hosts are the servers the agent list knows, in name order.
func (a *App) hosts() []string {
	seen := map[string]bool{}
	var out []string
	add := func(h string) {
		if h != "" && !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	for _, it := range a.agents.value.Items {
		add(it.ConnectionID)
	}
	for _, o := range a.agents.value.Observations {
		add(o.ConnectionID)
	}
	sort.Strings(out)
	return out
}

// locationsPage lists the local repositories, whether each can be used,
// and copies what a server has and a repository lacks, one at a time. No
// key is needed (docs/UI.md 7.3).
func (a *App) locationsPage(c *ui.Context) {
	pageHead(c, "备份位置", "—— 服务器之外，这台电脑上的那一份。")
	repos := a.svc.Repos()
	if len(repos) == 0 {
		muted(ui.Text(c, "还没有登记本机仓库。")).FontSize(14)
		return
	}
	hosts := a.hosts()
	for _, r := range repos {
		card(ui.Column(c.Key(r.Name))).Gap(12).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icons.folder).FontSize(22).TextColor(palette.Brand)
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					ui.Text(c, r.Name).FontSize(16).Bold()
					muted(ui.Text(c, r.Path)).Font(cur.Fonts.Mono)
				})
			})
			if len(hosts) == 0 {
				muted(ui.Text(c, "还没有读到服务器。"))
			}
			for _, h := range hosts {
				a.repoLine(c.Key(h), r.Name, h)
			}
		})
	}
	muted(ui.Text(c, "补到本机：从服务器复制仓库里缺少的备份，并逐个周期校验；不需要钥匙。外接盘要先接上。")).FontSize(11).Wrap()
}

func (a *App) repoLine(c *ui.Context, repo, host string) {
	var st service.RepoStatus
	for _, s := range a.svc.RepoStatuses(host, "") {
		if s.Name == repo {
			st = s
		}
	}
	key := repo + "/" + host
	sy := a.syncs[key]
	ui.Column(c).Gap(6).Padding(12, 14).Radius(10).Background(palette.Board).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.server).FontSize(16).TextColor(palette.TextSub)
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				ui.Text(c, host).FontSize(14).Bold()
				muted(ui.Text(c, repoStateLine(st, c.Now()))).Wrap()
			})
			switch {
			case !st.Available:
				ui.Text(c, "仓库现在不可用").FontSize(12).TextColor(palette.Warn)
			case sy != nil && sy.busy:
				ui.Spinner(c)
			default:
				ui.Button(c, "现在补到本机").OnClick(func() { a.syncRepo(repo, host) })
			}
		})
		if sy == nil || sy.busy {
			return
		}
		if sy.err != nil {
			errorBox(c, sy.err, a.fold("sync-err-"+key))
			return
		}
		if r := sy.result; r != nil {
			ui.Text(c, fmt.Sprintf("完成：%d 个备份，新复制 %s，校验通过", len(r.Snapshots), size(r.Bytes))).FontSize(12)
			if t := service.CheckSummary(r.Checks); t != "" {
				muted(ui.Text(c, t)).Wrap()
			}
		}
	})
}

// repoStateLine is when this repository last received the server's
// backups, and the last failure, each said apart.
func repoStateLine(st service.RepoStatus, now time.Time) string {
	line := "还没有从这台服务器补过"
	if st.State.LastOK != nil {
		line = "上次补齐：" + when(*st.State.LastOK, now)
	}
	if st.State.LastError != "" {
		line += " · 上次失败：" + st.State.LastError
	}
	return line
}

func (a *App) syncRepo(repo, host string) {
	sy := &syncState{busy: true}
	a.syncs[repo+"/"+host] = sy
	a.run(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		r, err := a.svc.RepoSync(ctx, host, repo)
		a.update(func() {
			sy.busy, sy.result, sy.err = false, r, err
			a.refreshTimeline()
		})
	})
}
