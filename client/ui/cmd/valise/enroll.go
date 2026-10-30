package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"

	"valise/core/enroll"
	"valise/core/profile"
	"valise/ui/service"
)

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func (c *cli) enroll(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("enroll", flag.ContinueOnError)
	var opts service.EnrollOptions
	var assign multiFlag
	fs.StringVar(&opts.ProfileID, "profile", "", "")
	fs.StringVar(&opts.InstanceID, "id", "", "")
	fs.Var(&assign, "assign", "")
	fs.BoolVar(&opts.Accept, "accept", false, "")
	asJSON := fs.Bool("report-json", false, "")
	pos, err := parseArgs(fs, args, 1)
	if err != nil {
		return err
	}
	opts.Assign = assign
	res, err := c.svc.Enroll(ctx, pos[0], opts)
	if err != nil {
		if res != nil && len(res.Candidates) > 0 && !*asJSON {
			printCandidates(c.out, res.Candidates)
		}
		return err
	}
	if *asJSON {
		return c.printJSON(res)
	}
	printReport(c.out, res)
	return nil
}

func printCandidates(w io.Writer, cands []enroll.Candidate) {
	fmt.Fprintln(w, "发现的实例：")
	for _, c := range cands {
		v := c.Version
		if v == "" {
			v = "版本未知"
		}
		fmt.Fprintf(w, "  %-10s %s（%s）\n", c.ProfileID, c.StateDir, v)
		for _, ch := range c.Children {
			fmt.Fprintf(w, "             符合子 profile：%s\n", ch)
		}
	}
	fmt.Fprintln(w)
}

func size(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// categoryOrder follows docs/ARCHITECTURE.md section 5.
var categoryOrder = profile.Categories

var categoryNames = map[string]string{
	"identity": "人格", "memory": "记忆", "skills": "技能", "config": "配置", "sessions": "会话",
	"state": "状态库", "body_patch": "身体补丁", "secrets": "凭据", "workspace": "工作区",
	"body": "身体（按来源重建；只进 raw）", "cache": "缓存（只进 raw）", "ignore": "忽略（只进 raw）",
	"unclassified": "未分类（按凭据层，不进 soul）",
}

func printReport(w io.Writer, res *service.EnrollResult) {
	r := res.Report
	verified := "已核实"
	if r.ProfileStatus != profile.StatusVerified {
		verified = fmt.Sprintf("未核实（%d 个条目待核实）", len(r.Pending))
	}
	origin := "官方"
	if res.ProfileLocal {
		origin = "本地，未经审核"
	}
	fmt.Fprintf(w, "登记报告：%s 上的实例 %s\n", res.Host, r.InstanceID)
	fmt.Fprintf(w, "profile：%s v%d（%s，%s）\n\n", r.ProfileID, r.ProfileVersion, origin, verified)

	fmt.Fprintln(w, "目录：")
	for _, rt := range r.Roots {
		note := map[string]string{"detect": "探测", "default": "profile 默认", "discover": "harness 命令读出"}[rt.Source]
		if !rt.Exists {
			note += "，不存在"
		}
		if rt.Resolved != "" {
			note += "，符号链接 → " + rt.Resolved
		}
		fmt.Fprintf(w, "  %-10s %s（%s）\n", rt.ID, rt.Path, note)
	}

	fmt.Fprintln(w, "\n分类：")
	for _, cat := range categoryOrder {
		if t, ok := r.Categories[cat]; ok {
			fmt.Fprintf(w, "  %-28s %6d 个文件  %10s\n", categoryNames[cat], t.Files, size(t.Bytes))
		}
	}
	fmt.Fprintf(w, "  %-28s %6d 个文件  %10s\n", categoryNames["unclassified"], r.Unclassified.Files, size(r.Unclassified.Bytes))
	if r.Symlinks > 0 {
		fmt.Fprintf(w, "  符号链接 %d 个（只记录，不跟随）\n", r.Symlinks)
	}

	fmt.Fprintf(w, "\n凭据文件（%d 个，只列路径；仅 soul-secrets / full 档封存）：\n", len(r.Secrets))
	for _, s := range r.Secrets {
		fmt.Fprintf(w, "  %s:%s\n", s.Root, s.Path)
	}
	if len(r.SQLite) > 0 {
		fmt.Fprintln(w, "\nSQLite 数据库（封存时用 VACUUM INTO 取一致副本）：")
		for _, s := range r.SQLite {
			fmt.Fprintf(w, "  %s:%s  [%s]", s.Root, s.Path, categoryNames[s.Category])
			if len(s.SecretTables) > 0 {
				fmt.Fprintf(w, "  含凭据表 %s，soul 档封存时在副本中清空", strings.Join(s.SecretTables, "、"))
			}
			fmt.Fprintln(w)
		}
	}
	if len(r.UnclassGroups) > 0 {
		groups := append([]enroll.UnclassifiedGroup(nil), r.UnclassGroups...)
		sort.SliceStable(groups, func(i, j int) bool { return groups[i].Bytes > groups[j].Bytes })
		fmt.Fprintln(w, "\n未分类文件（未知即敏感：按凭据层封存，不进 soul 档；按目录汇总，可用 --assign <root>:<glob>=<类别> 归类）：")
		for i, g := range groups {
			if i == 15 {
				fmt.Fprintf(w, "  …… 另有 %d 组\n", len(groups)-15)
				break
			}
			fmt.Fprintf(w, "  %s:%-36s %6d 个文件  %10s\n", g.Root, g.Prefix, g.Files, size(g.Bytes))
		}
	}

	fmt.Fprintln(w, "\n身体来源：")
	printProvenance(w, r.Provenance)

	if len(r.Service) > 0 {
		fmt.Fprintln(w, "\n服务：")
		for _, s := range r.Service {
			if !s.Exists() {
				fmt.Fprintf(w, "  %s：不存在\n", s.Unit)
				continue
			}
			fmt.Fprintf(w, "  %s：%s，开机启动 %s\n", s.Unit, s.Active, s.Enabled)
		}
	}
	if len(r.Entries) > 0 {
		fmt.Fprintln(w, "\n入口（服务器上的端口）：")
		for _, e := range r.Entries {
			state := "未在监听"
			if e.Listening {
				state = "在监听"
			}
			fmt.Fprintf(w, "  %s  %s:%d  %s\n", e.ID, e.Host, e.Port, state)
		}
	}
	if len(r.Changes) > 0 {
		fmt.Fprintln(w, "\n与上次登记相比的变化：")
		for _, ch := range r.Changes {
			fmt.Fprintf(w, "  %s：%s → %s\n", ch.Field, ch.From, ch.To)
		}
	}
	if len(r.Warnings) > 0 || len(r.Unreadable) > 0 {
		fmt.Fprintln(w, "\n提醒：")
		for _, s := range r.Warnings {
			fmt.Fprintf(w, "  · %s\n", s)
		}
		if len(r.Unreadable) > 0 {
			fmt.Fprintf(w, "  · %d 个路径无法读取（权限不足），它们不会被封存\n", len(r.Unreadable))
		}
	}
	for _, rej := range res.RejectedLocalProfiles {
		fmt.Fprintf(w, "  · 已忽略未通过检查的本地 profile：%s\n", rej)
	}
	fmt.Fprintln(w)
	if res.Written != "" {
		fmt.Fprintf(w, "已写入实例记录：%s\n", res.Written)
	} else {
		fmt.Fprintln(w, "这只是报告，尚未写入任何东西。确认无误后，加上 --accept 写入实例记录。")
	}
}

func printProvenance(w io.Writer, p enroll.Provenance) {
	switch {
	case p.NPM != nil:
		fmt.Fprintf(w, "  npm 全局包 %s@%s\n", p.NPM.Package, orUnknown(p.NPM.Version))
		if p.NPM.Prefix != "" {
			fmt.Fprintf(w, "  安装前缀 %s\n", p.NPM.Prefix)
		}
		if p.NPM.Registry != "" {
			fmt.Fprintf(w, "  registry %s\n", p.NPM.Registry)
		}
		if p.NPM.Integrity != "" {
			fmt.Fprintf(w, "  integrity %s\n", p.NPM.Integrity)
		}
	case p.Git != nil:
		fmt.Fprintf(w, "  git %s\n  commit %s\n", orUnknown(p.Git.Remote), orUnknown(p.Git.Commit))
		if p.Git.Dirty {
			fmt.Fprintf(w, "  有本地修改：%s 的补丁将作为 body_patch 随灵魂封存\n", size(p.Git.PatchBytes))
		} else {
			fmt.Fprintln(w, "  工作树干净")
		}
	case p.Tool != nil:
		fmt.Fprintf(w, "  %s %s %s\n", p.Kind, p.Tool.Package, orUnknown(p.Tool.Version))
	case p.Binary != nil:
		fmt.Fprintf(w, "  可执行文件 %s\n  SHA-256 %s\n", p.Binary.Path, p.Binary.SHA256)
	default:
		fmt.Fprintln(w, "  未能记录")
	}
	for _, s := range p.Warnings {
		fmt.Fprintf(w, "  · %s\n", s)
	}
}

func orUnknown(s string) string {
	if s == "" {
		return "（未知）"
	}
	return s
}
