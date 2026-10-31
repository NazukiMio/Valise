package main

import (
	"context"
	"flag"
	"fmt"
	"sort"
	"strings"
	"time"

	"valise/core/vault"
	"valise/core/vaultrpc"
	"valise/ui/service"
)

func (c *cli) keyRegister(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("register", flag.ContinueOnError)
	label := fs.String("label", "", "")
	pos, err := parseArgs(fs, args, 1)
	if err != nil {
		return err
	}
	res, info, err := c.svc.KeyRegister(ctx, pos[0], *label)
	if err != nil {
		return err
	}
	if res.Added {
		fmt.Fprintf(c.out, "已把公钥（指纹 %s）登记到 %s。之后的封存会加密给它。\n", info.Fingerprint, pos[0])
	} else {
		fmt.Fprintf(c.out, "公钥（指纹 %s）已经登记在 %s 上。\n", info.Fingerprint, pos[0])
	}
	fmt.Fprintf(c.out, "服务器上的收件人指纹：%v\n", res.Fingerprints)
	return nil
}

var categoryOrderSeal = []string{"identity", "memory", "skills", "config", "sessions", "state", "body_patch", "secrets",
	"unclassified", "workspace", "body", "cache", "ignore"}

func (c *cli) seal(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("seal", flag.ContinueOnError)
	var p vaultrpc.SealParams
	fs.StringVar(&p.Tier, "tier", vault.TierSoul, "")
	fs.StringVar(&p.Reason, "reason", "", "")
	asJSON := fs.Bool("json", false, "")
	pos, err := parseArgs(fs, args, 2)
	if err != nil {
		return err
	}
	p.InstanceID = pos[1]
	rep, err := c.svc.Seal(ctx, pos[0], p)
	if err != nil {
		return err
	}
	if *asJSON {
		return c.printJSON(rep)
	}
	w := c.out
	fmt.Fprintf(w, "已封存 %s 上的实例 %s（%s 档）\n快照：%s（epoch %d", pos[0], rep.InstanceID, rep.Tier, rep.SnapshotID, rep.Epoch)
	if rep.NewEpoch {
		fmt.Fprint(w, "，新 epoch")
	}
	fmt.Fprintln(w, "）")
	for _, cat := range categoryOrderSeal {
		if t, ok := rep.Categories[cat]; ok {
			fmt.Fprintf(w, "  %-28s %6d 个文件  %10s\n", categoryNames[cat], t.Files, size(t.Bytes))
		}
	}
	fmt.Fprintf(w, "合计 %d 个文件，%s；本次新写入金库 %s，用时 %.1f 秒\n", rep.Files, size(rep.PlainBytes), size(rep.AddedBytes), float64(rep.DurationMS)/1000)
	for _, s := range rep.SQLite {
		if len(s.StrippedTables) > 0 {
			fmt.Fprintf(w, "  %s:%s 的副本已清空凭据表 %v\n", s.Root, s.Path, s.StrippedTables)
		}
	}
	if rep.BodyPatch {
		fmt.Fprintln(w, "  身体有本地修改，已封存 body_patch")
	}
	if rep.UnclassifiedCount > 0 && rep.Tier == vault.TierSoul {
		fmt.Fprintf(w, "未分类文件 %d 个，按\"未知即敏感\"不进 soul 档（soul-secrets / full / raw 档会封存；可在登记时用 --assign 归类）\n", rep.UnclassifiedCount)
	}
	if rep.Tier == vault.TierRaw {
		fmt.Fprintln(w, "raw 档：实例目录下的全部文件，整体按凭据层对待；其中的身体只能在同架构、同路径下原样使用")
	}
	if len(rep.Unstable) > 0 {
		fmt.Fprintf(w, "封存期间仍在变化的文件（unstable，已封存第二次读到的内容）：\n")
		for _, u := range rep.Unstable {
			fmt.Fprintf(w, "  %s\n", u)
		}
	}
	for _, s := range rep.Warnings {
		fmt.Fprintf(w, "  · %s\n", s)
	}
	fmt.Fprintln(w, "状态：已写入服务器金库；尚未同步到异地（valise sync <服务器> <目标>）")
	return nil
}

func (c *cli) vaultCmd(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errUsage
	}
	if args[0] == "delete" {
		return c.vaultDelete(ctx, args[1:])
	}
	fs := flag.NewFlagSet("vault", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "")
	pos, err := parseArgs(fs, args[1:], 1)
	if err != nil {
		return err
	}
	switch args[0] {
	case "status":
		st, err := c.svc.VaultStatus(ctx, pos[0])
		if err != nil {
			return err
		}
		if *asJSON {
			return c.printJSON(st)
		}
		if !st.Exists {
			fmt.Fprintf(c.out, "%s 上还没有逃生舱。收件人指纹：%v\n", pos[0], st.Recipients)
			return nil
		}
		name := st.ServerName
		if name == "" {
			name = "（还没有名字，云备份前需要 valise server name 起名）"
		}
		fmt.Fprintf(c.out, "服务器名：%s　收件人指纹：%v\n", name, st.Recipients)
		synced := map[string][]string{}
		for _, t := range st.Targets {
			for _, sid := range t.LastSynced {
				synced[sid] = append(synced[sid], t.Name)
			}
		}
		metas := map[string]vault.Meta{}
		for _, m := range st.Snapshots {
			metas[m.SnapshotID] = m
		}
		for _, l := range st.Leaves {
			label := l.Leaf
			if l.Leaf == vault.LegacyLeaf {
				label = "分档之前的旧金库（-）"
			}
			extra := ""
			switch {
			case l.Deleting:
				extra = "，删除未完成"
			case l.Current:
				extra = "，当前周期"
			}
			fmt.Fprintf(c.out, "\n周期 %s（%d 个逃生舱，%s%s）\n", label, len(l.Snapshots), size(l.Bytes), extra)
			ids := append([]string(nil), l.Snapshots...)
			sort.Sort(sort.Reverse(sort.StringSlice(ids)))
			for _, id := range ids {
				m := metas[id]
				where := "服务器"
				if t := synced[id]; len(t) > 0 {
					where += " + " + strings.Join(t, "、")
				} else {
					where += "（尚未上云）"
				}
				fmt.Fprintf(c.out, "  %s  %-10s %-13s %s  %5d 个文件 %10s  %s\n", id, m.InstanceID, m.Tier,
					m.CreatedAt.Local().Format("2006-01-02 15:04"), m.FileCount, size(m.PlainBytes), where)
			}
		}
		fmt.Fprintln(c.out)
		for _, t := range st.Targets {
			last := "从未成功"
			if t.LastOK != nil {
				last = t.LastOK.Local().Format(time.DateTime)
			}
			auto := "自动同步：关"
			if t.Auto != nil {
				auto = "自动同步：每 " + t.Auto.Every
			}
			fmt.Fprintf(c.out, "云备份 %s（%s %s，%s）：最后成功 %s", t.Name, t.Kind, t.Dest, auto, last)
			if t.LastError != "" {
				fmt.Fprint(c.out, "；上次失败")
			}
			fmt.Fprintln(c.out)
		}
		return nil
	case "verify":
		res, err := c.svc.VaultVerifyRemote(ctx, pos[0])
		if err != nil {
			return err
		}
		return c.printProblems("服务器金库（不需要私钥的校验）", res.Problems)
	}
	return errUsage
}

func (c *cli) printProblems(what string, probs []vault.Problem) error {
	if len(probs) == 0 {
		fmt.Fprintf(c.out, "%s：校验通过，没有发现问题。\n", what)
		return nil
	}
	fmt.Fprintf(c.out, "%s：发现 %d 个问题：\n", what, len(probs))
	for _, p := range probs {
		fmt.Fprintf(c.out, "  %s\n", p)
	}
	return errProblems
}

func (c *cli) target(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errUsage
	}
	switch args[0] {
	case "list":
		return c.targetList(ctx, args[1:])
	case "delete":
		return c.targetDelete(ctx, args[1:])
	case "auto":
		return c.targetAuto(ctx, args[1:])
	case "add":
	default:
		return errUsage
	}
	fs := flag.NewFlagSet("target", flag.ContinueOnError)
	dest := fs.String("rsync", "", "")
	auto := fs.String("auto", "", "")
	pos, err := parseArgs(fs, args[1:], 2)
	if err != nil || *dest == "" {
		return errUsage
	}
	t := vaultrpc.Target{Name: pos[1], Kind: "rsync", Dest: *dest}
	if *auto != "" {
		t.Auto = &vaultrpc.AutoSync{Every: *auto}
	}
	if err := c.svc.TargetSet(ctx, pos[0], t); err != nil {
		return err
	}
	fmt.Fprintf(c.out, "已在 %s 上添加云备份 %s（rsync → %s）。服务器需要能用自己的 SSH 配置登录该主机。\n", pos[0], pos[1], *dest)
	if t.Auto != nil {
		fmt.Fprintf(c.out, "自动同步：每次建立逃生舱后，距上次同步超过 %s 就顺带同步一次。\n", t.Auto.Every)
	}
	return nil
}

func (c *cli) sync(ctx context.Context, args []string) error {
	pos, err := parseArgs(flag.NewFlagSet("sync", flag.ContinueOnError), args, 2)
	if err != nil {
		return err
	}
	res, err := c.svc.Sync(ctx, pos[0], pos[1])
	if err != nil {
		return err
	}
	fmt.Fprintf(c.out, "已同步到 %s（%s），%d 个周期、%d 个逃生舱。\n", res.Target, res.Dest, len(res.Leaves), len(res.Snapshots))
	return nil
}

func (c *cli) pull(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("pull", flag.ContinueOnError)
	to := fs.String("to", "", "")
	leaf := fs.String("leaf", "", "")
	pos, err := parseArgs(fs, args, 1)
	if err != nil {
		return err
	}
	var results []*service.PullResult
	if *leaf != "" {
		var r *service.PullResult
		r, err = c.svc.Pull(ctx, pos[0], *leaf, *to)
		if r != nil {
			results = append(results, r)
		}
	} else {
		results, err = c.svc.PullAll(ctx, pos[0], *to)
	}
	var probs []vault.Problem
	for _, res := range results {
		fmt.Fprintf(c.out, "已把 %s 的周期 %s 拉取到 %s：新下载 %d 个文件（%s），已有 %d 个。\n",
			pos[0], res.Leaf, res.Dir, res.Downloaded, size(res.Bytes), res.Present)
		for _, p := range res.Problems {
			p.Where = res.Leaf + " " + p.Where
			probs = append(probs, p)
		}
	}
	if err != nil {
		return err
	}
	return c.printProblems("本机副本（不需要私钥的校验）", probs)
}

func (c *cli) verify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	dir := fs.String("vault", "", "")
	withKey := fs.Bool("key", false, "")
	if _, err := parseArgs(fs, args, 0); err != nil || *dir == "" {
		return errUsage
	}
	var pass *string
	if *withKey {
		p, err := c.passphrase("口令：")
		if err != nil {
			return err
		}
		pass = &p
	}
	res, err := c.svc.VerifyLocal(*dir, pass)
	if err != nil {
		return err
	}
	what := "本机金库（不需要私钥的校验）"
	if res.WithKey {
		what = fmt.Sprintf("本机金库（用私钥解密并校验了 %d 个快照中的 %d 个文件）", res.Snapshots, res.Files)
	}
	return c.printProblems(what, res.Problems)
}
