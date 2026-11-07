package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"strings"

	"valise/core/revive"
	"valise/core/valise"
	"valise/ui/service"
)

type mapFlag map[string]string

func (m mapFlag) String() string { return fmt.Sprint(map[string]string(m)) }
func (m mapFlag) Set(v string) error {
	k, val, ok := strings.Cut(v, "=")
	if !ok || k == "" || val == "" {
		return fmt.Errorf("格式为 <id>=<路径>")
	}
	m[k] = val
	return nil
}

// confirm asks a yes/no question (one line from stdin).
func (c *cli) confirm(prompt string) bool {
	fmt.Fprint(c.errOut, prompt+" [y/N] ")
	if c.lines == nil {
		c.lines = bufio.NewReader(c.in)
	}
	line, _ := c.lines.ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes", "是":
		return true
	}
	return false
}

func (c *cli) revive(ctx context.Context, args []string) error {
	if len(args) > 0 && args[0] == "resume" {
		return c.reviveResume(ctx, args[1:])
	}
	fs := flag.NewFlagSet("revive", flag.ContinueOnError)
	o := service.ReviveOptions{Roots: map[string]string{}}
	fs.StringVar(&o.SnapshotID, "snapshot", "", "")
	fs.StringVar(&o.Mode, "mode", revive.ModeRollback, "")
	fs.StringVar(&o.From, "from", "", "")
	fs.StringVar(&o.InstanceID, "id", "", "")
	fs.Var(mapFlag(o.Roots), "root", "")
	fs.BoolVar(&o.AllowDraft, "allow-draft-profile", false, "")
	fs.BoolVar(&o.AllowSnapshotProfile, "allow-snapshot-profile", false, "")
	dry := fs.Bool("dry-run", false, "")
	yes := fs.Bool("yes", false, "")
	backupFirst := fs.Bool("backup-first", false, "")
	fs.StringVar(&o.Fault, "inject-fault", "", "")
	pos, err := parseArgs(fs, args, 1)
	if err != nil || o.SnapshotID == "" {
		return errUsage
	}
	o.Target = pos[0]
	pass, err := c.passphrase("口令（只在这台电脑上用来打开备份）：")
	if err != nil {
		return err
	}
	rp, err := c.svc.PlanRevive(c.withProgress(ctx), o, pass)
	if err != nil {
		return err
	}
	defer rp.Close()
	printPlan(c, rp)
	if len(rp.Blockers) > 0 {
		fmt.Fprintln(c.out, "\n计划无法执行：")
		for _, b := range rp.Blockers {
			fmt.Fprintf(c.out, "  · %s\n    建议：%s\n", b.Reason, b.Suggestion)
		}
		return errProblems
	}
	if *dry {
		fmt.Fprintln(c.out, "\n（--dry-run：只输出计划，没有做任何改动）")
		return nil
	}
	if *backupFirst && (rp.UnverifiedSource == nil || !rp.UnverifiedSource.BackupFirst) {
		return valise.New("backup_first_unavailable", "--backup-first 只用于来源无法核实、且目标上已有同一实例的恢复", "去掉 --backup-first 后重试")
	}
	decision := service.SourceCancel
	if u := rp.UnverifiedSource; u != nil {
		decision = c.sourceDecision(u, *yes, *backupFirst)
		if decision == service.SourceCancel {
			fmt.Fprintln(c.out, "已取消，没有做任何改动。")
			return nil
		}
	}
	if !*yes && !c.confirm("按上述计划暂存？（活动目录在第二次确认前不会被改动）") {
		fmt.Fprintln(c.out, "已取消，没有做任何改动。")
		return nil
	}
	res, err := c.svc.ExecuteReviveConfirmed(c.withProgress(ctx), rp, o.Fault, service.ReviveConfirm{
		// The choice was made interactively above (or by --yes, as for the
		// other confirmations of this command); no request field can set it.
		UnverifiedSource: func(service.UnverifiedSource) service.SourceDecision { return decision },
		Swap: func(v *revive.StageVerifyResult) bool {
			if b := rp.PreRestoreBackup; b != nil {
				fmt.Fprintf(c.out, "恢复前已备份当前状态：%s（full，%d 个文件）。\n", b.SnapshotID, b.Files)
			}
			fmt.Fprintf(c.out, "暂存校验通过：%d 个文件，%s，SHA-256 与 SQLite 完整性检查全部通过。\n", v.Files, size(v.Bytes))
			return *yes || c.confirm("开始交换？（会停止服务；失败会自动退回）")
		},
	})
	if res != nil && res.State != "" {
		printSwap(c, res)
	}
	return err
}

// sourceDecision asks how to treat a snapshot whose source is unverified.
// --backup-first chooses the backup; --yes, like this command's other
// confirmations, stands for "我清楚，继续".
func (c *cli) sourceDecision(u *service.UnverifiedSource, yes, backupFirst bool) service.SourceDecision {
	if backupFirst {
		return service.SourceBackupFirst
	}
	if yes {
		return service.SourceProceed
	}
	fmt.Fprintln(c.errOut, "\n这个快照的"+u.Notice+"。请选择：")
	fmt.Fprintln(c.errOut, "  1) 我清楚，继续")
	if u.BackupFirst {
		fmt.Fprintf(c.errOut, "  2) 先对目标上的实例 %s 做一次 full 备份，成功后再恢复\n", u.BackupInstance)
	}
	fmt.Fprint(c.errOut, "  其他) 取消\n选择：")
	if c.lines == nil {
		c.lines = bufio.NewReader(c.in)
	}
	line, _ := c.lines.ReadString('\n')
	switch strings.TrimSpace(line) {
	case "1":
		return service.SourceProceed
	case "2":
		if u.BackupFirst {
			return service.SourceBackupFirst
		}
	}
	return service.SourceCancel
}

func printPlan(c *cli, rp *service.RevivePlan) {
	w, p := c.out, rp.Plan
	mode := map[string]string{revive.ModeRollback: "恢复当前实例", revive.ModeMigrate: "迁移到另一台服务器", revive.ModeClone: "创建副本（新的 agent）"}[p.Mode]
	fmt.Fprintf(w, "恢复计划（%s）：op %s\n", mode, p.OpID)
	fmt.Fprintf(w, "  备份：%s（%s，%s，来自 %s）\n", p.SnapshotID, tierLabel(p.Tier), rp.Snapshot.CreatedAt.Local().Format("2006-01-02 15:04"), rp.From)
	fmt.Fprintf(w, "  目标：%s（%s）上的实例 %s\n", rp.Target, rp.TargetArch, p.InstanceID)
	if l := transferLine(rp.Transfer); l != "" {
		fmt.Fprintln(w, "  "+l)
	}
	origin := "官方"
	if rp.ProfileLocal {
		origin = "本地，未经审核"
	}
	if rp.ProfileFromSnapshot {
		origin = "来自备份，未经当前客户端审核"
	}
	status := "已核实"
	if p.Profile.Status != "verified" {
		status = "未核实"
	}
	fmt.Fprintf(w, "  profile：%s v%d（%s，%s）\n", p.Profile.ID, p.Profile.ProfileVersion, origin, status)
	for _, r := range p.Roots {
		fmt.Fprintf(w, "  目录 %-10s → %s\n", r.ID, r.Path)
	}
	fmt.Fprintf(w, "  将恢复 %d 个文件，%s", rp.Files, size(rp.Bytes))
	if rp.RestoreSecret {
		fmt.Fprint(w, "（含凭据）")
	}
	fmt.Fprintln(w)
	if p.Carry {
		fmt.Fprintf(w, "  备份没装的类别（%s 不包含的）保留现在的内容\n", tierLabel(p.Tier))
	}
	for _, e := range rp.Excluded {
		fmt.Fprintf(w, "  不恢复独占凭据：%s\n", e)
	}
	switch p.Body.Action {
	case revive.BodyKeep:
		fmt.Fprintln(w, "  身体：目标上已有同一来源、同一版本，保持不变")
	case revive.BodyMoveDirs:
		fmt.Fprintf(w, "  身体：保持不变（交换时搬动 %v）\n", p.Body.Dirs)
	case revive.BodyNPMInstall:
		fmt.Fprintf(w, "  身体：按来源记录安装 %s@%s（registry %s，integrity 已与接入时的记录核对）\n", p.Body.Package, p.Body.Version, p.Body.Registry)
	}
	if p.Unit != "" {
		fmt.Fprintf(w, "  服务：%s", p.Unit)
		if p.InstallService {
			fmt.Fprintf(w, "（目标上没有，将执行 harness 自带的安装命令：%s）", strings.Join(rp.InstallArgv, " "))
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "  预计停机：%s\n", rp.Downtime)
	fmt.Fprintf(w, "  空间：需要约 %s，目标可用 %s；暂存目录与活动目录在同一文件系统\n", size(rp.NeedBytes), size(int64(rp.FreeBytes)))
	for _, x := range rp.Warnings {
		fmt.Fprintf(w, "  · %s\n", x)
	}
	if u := rp.UnverifiedSource; u != nil {
		fmt.Fprintf(w, "  %s：快照没有可核实的来源实例世代，以下只是快照自带的记录\n", u.Notice)
		name := u.InstanceID
		if u.Name != "" {
			name += "（命名实例 " + u.Name + "）"
		}
		fmt.Fprintf(w, "    实例：%s\n    profile：%s v%d\n    来源服务器：%s，周期 %s\n    创建时间：%s\n    范围：%s\n",
			name, u.ProfileID, u.ProfileVersion, u.SourceServer, u.Period, u.CreatedAt.Local().Format("2006-01-02 15:04"), tierLabel(u.Tier))
		if u.BackupFirst {
			fmt.Fprintf(w, "    执行前需要选择：我清楚，继续；或先对目标上的实例 %s 做一次 full 备份再恢复（--backup-first）\n", u.BackupInstance)
		} else {
			fmt.Fprintln(w, "    目标上没有同一实例，执行前需要确认：我清楚，继续")
		}
	}
}

func printSwap(c *cli, r *revive.SwapResult) {
	w := c.out
	switch r.State {
	case revive.StateHealthy:
		fmt.Fprintf(w, "结果：恢复完成，服务健康（op %s）\n", r.OpID)
	case revive.StateRestored:
		fmt.Fprintf(w, "结果：%s（op %s）\n", r.Message, r.OpID)
	case revive.StateRolledBack:
		fmt.Fprintf(w, "结果：rolled_back —— %s（op %s）\n", r.Message, r.OpID)
		if r.Logs != "" {
			fmt.Fprintf(w, "最近的服务日志：\n%s\n", r.Logs)
		}
	default:
		fmt.Fprintf(w, "结果：%s（op %s）\n", r.State, r.OpID)
	}
	if r.Carried > 0 {
		fmt.Fprintf(w, "  保留了 %d 个档位以外的文件\n", r.Carried)
	}
	for _, p := range r.Prev {
		fmt.Fprintf(w, "  保留：%s（用 valise prev drop 删除）\n", p)
	}
	for _, p := range r.Intruders {
		fmt.Fprintf(w, "  注意：交换期间有其他进程重新创建了活动目录，已原样挪到 %s 保留，请检查\n", p)
	}
}

func (c *cli) reviveResume(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("resume", flag.ContinueOnError)
	cont := fs.Bool("continue", false, "")
	back := fs.Bool("back", false, "")
	fault := fs.String("inject-fault", "", "")
	pos, err := parseArgs(fs, args, 2)
	if err != nil || *cont == *back {
		return errUsage
	}
	action := "back"
	if *cont {
		action = "continue"
	}
	res, err := c.svc.ReviveResume(ctx, pos[0], pos[1], action, *fault)
	if res != nil && res.State != "" {
		printSwap(c, res)
	}
	return err
}

func (c *cli) prev(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errUsage
	}
	fs := flag.NewFlagSet("prev", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "")
	switch args[0] {
	case "list":
		pos, err := parseArgs(fs, args[1:], 1)
		if err != nil {
			return err
		}
		res, err := c.svc.PrevList(ctx, pos[0])
		if err != nil {
			return err
		}
		if len(res.Entries) == 0 {
			fmt.Fprintln(c.out, "没有保留的交换前状态。")
		}
		for _, e := range res.Entries {
			fmt.Fprintf(c.out, "  %s  %s  %s  %s\n", e.OpID, e.Created.Local().Format("2006-01-02 15:04"), size(e.Bytes), e.Path)
		}
		return nil
	case "drop":
		pos, err := parseArgs(fs, args[1:], 2)
		if err != nil {
			return err
		}
		if !*yes && !c.confirm("永久删除操作 "+pos[1]+" 保留的交换前状态？删除后无法再退回到交换前。") {
			return nil
		}
		removed, err := c.svc.PrevDrop(ctx, pos[0], pos[1])
		for _, r := range removed {
			fmt.Fprintf(c.out, "已删除 %s\n", r)
		}
		return err
	}
	return errUsage
}

func (c *cli) keyUnregister(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("unregister", flag.ContinueOnError)
	recipient := fs.String("recipient", "", "")
	yes := fs.Bool("yes", false, "")
	pos, err := parseArgs(fs, args, 1)
	if err != nil {
		return err
	}
	if *recipient == "" {
		info, err := c.svc.KeyShow()
		if err != nil {
			return err
		}
		*recipient = info.Recipient
	}
	fmt.Fprintln(c.out, "移除这把钥匙后，以后的备份不再给它（会开始新的 epoch）。")
	fmt.Fprintln(c.out, "注意：以前的备份仍然可以被这把钥匙打开，加密无法追溯撤销；如果钥匙已泄露，应把旧快照视为已暴露。")
	if !*yes && !c.confirm("从 "+pos[0]+" 移除 "+*recipient+"？") {
		return nil
	}
	fps, removed, err := c.svc.KeyUnregister(ctx, pos[0], *recipient)
	if err != nil {
		return err
	}
	if !removed {
		fmt.Fprintln(c.out, "该公钥不在服务器的收件人中。")
	} else {
		fmt.Fprintf(c.out, "已移除。服务器上剩余的收件人指纹：%v\n", fps)
	}
	return nil
}
