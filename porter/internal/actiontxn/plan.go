// Package actiontxn executes fixed profile configuration transactions.
package actiontxn

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"valise/core/action"
	"valise/core/profile"
	"valise/core/valise"
	"valise/porter/internal/actionjournal"
	porterenroll "valise/porter/internal/enroll"
)

func reject(reason string) error {
	return valise.New("action_preflight", reason, "检查配置事务条件与当前 profile；条件变化后重新确认")
}
func digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func Plan(ctx context.Context, base *porterenroll.Env, id, actionID string) (*action.Plan, error) {
	p, err := porterenroll.ReadProfile(base.Home, id)
	if err != nil {
		return nil, err
	}
	rec, err := porterenroll.ReadRecord(base.Home, id)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, reject("实例未接入")
	}
	a := p.FindAction(actionID)
	if a == nil {
		return nil, reject("服务器资料没有此动作")
	}
	if len(profile.ValidateTransaction(*a, p.ApprovalCheck)) != 0 {
		return nil, reject("配置事务未核实或声明不完整")
	}
	if p.Service == nil || len(p.Service.Candidates) == 0 {
		return nil, reject("此实例没有已核实的服务作用域")
	}
	e := base.ForInstance(p, rec)
	live := rec.Roots[profile.StateRoot]
	if !filepath.IsAbs(live) || filepath.Clean(live) != live || live == "/" {
		return nil, reject("活动目录无效")
	}
	li, err := actionjournal.DirectoryIdentity(live)
	if err != nil {
		return nil, err
	}
	live, err = filepath.EvalSymlinks(live)
	if err != nil {
		return nil, err
	}
	if len(porterenroll.NestedInstances(e, p, id, live)) != 0 {
		return nil, reject("活动目录包含其他实例，不能自动交换")
	}
	if p.Detect == nil || len(p.Detect.VersionArgv) == 0 {
		return nil, reject("没有已核实的版本读法")
	}
	argv, ok := e.ExpandArgv(p.Detect.VersionArgv, map[string]string{"STATE_DIR": live})
	if !ok || len(argv) == 0 {
		return nil, reject("版本命令环境不完整")
	}
	out, err := e.Run(ctx, 15*time.Second, live, argv...)
	if err != nil || out.Code != 0 {
		return nil, reject("无法核实当前安装版本")
	}
	version := ""
	for _, line := range strings.Split(string(out.Stdout), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			version = line
			break
		}
	}
	if !a.TransactionVerified(version) {
		return nil, reject("当前安装版本未通过此配置事务验收")
	}
	for _, step := range a.Steps {
		argv, ok = e.ExpandArgv(step.Argv, nil)
		if !ok || len(argv) == 0 {
			return nil, reject("配置命令环境不完整")
		}
		if !filepath.IsAbs(argv[0]) {
			if _, err = e.LookPath(argv[0]); err != nil {
				return nil, reject("找不到配置命令")
			}
		}
	}
	if err = actionjournal.CheckWrites(live, a.Transaction.Writes); err != nil {
		return nil, reject("配置写入目标包含链接或无效路径")
	}
	_, size, err := actionjournal.TreeFacts(live)
	if err != nil {
		return nil, err
	}
	services, err := observeServices(ctx, e, p)
	if err != nil {
		return nil, err
	}
	affected := []string{id}
	recs, err := porterenroll.ListRecords(base.Home)
	if err != nil {
		return nil, err
	}
	for _, other := range recs {
		if other.InstanceID == id {
			continue
		}
		for _, sv := range other.Service {
			for _, ours := range services {
				if sv.Unit == ours.Unit && sv.Scope == ours.Scope {
					affected = append(affected, other.InstanceID)
				}
			}
		}
	}
	slices.Sort(affected)
	affected = slices.Compact(affected)
	plan := &action.Plan{InstanceID: id, Affected: affected, ProfileDigest: digest(p), ActionDigest: digest(a), Version: version, BinDirs: p.Detect.BinDirs, SelectionDigest: digest(rec.Env), Definition: *a, Approval: *p.ApprovalCheck, Live: live, LiveIdentity: li, Bytes: size, Services: services, Health: p.Health, Entries: p.Entries}
	plan.Digest = digest(plan)
	return plan, nil
}

func checkPlan(p *action.Plan) error {
	if p == nil || p.Digest == "" {
		return reject("缺少已确认的执行计划")
	}
	clone := *p
	clone.Digest = ""
	if digest(&clone) != p.Digest {
		return errors.New("configuration plan digest mismatch")
	}
	return nil
}
