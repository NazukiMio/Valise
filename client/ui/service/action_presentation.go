package service

import (
	"fmt"
	"strings"

	"valise/client/ssh"
	"valise/core/action"
)

func ActionPlanLines(p *action.Plan) []string {
	if p == nil {
		return nil
	}
	lines := []string{"目标：" + p.Definition.Explain, "配置只在暂存副本中修改，复核通过后交换目录。", fmt.Sprintf("完整状态目录复制：%d 字节", p.Bytes), "受影响实例：" + strings.Join(p.Affected, "、")}
	for _, sv := range p.Services {
		state := "原本停止，保持停止"
		if sv.Original == "active" {
			state = "原本运行，会短暂停止并恢复运行"
		}
		lines = append(lines, "服务 "+sv.Unit+"："+state)
	}
	lines = append(lines, linkLines(p)...)
	for _, step := range p.Definition.Steps {
		argv := make([]string, len(step.Argv))
		for i, a := range step.Argv {
			argv[i] = ssh.Quote(a)
		}
		lines = append(lines, "步骤 "+step.ID+"："+strings.Join(argv, " "))
	}
	return lines
}
func ActionOperationLines(j *action.Operation) []string {
	if j == nil {
		return nil
	}
	config := "尚未发布，活动目录保留原配置"
	if j.Published {
		config = "新配置已发布"
	}
	approval := map[string]string{"resolved": "已达到声明目标", "remaining": "问题仍在", "unknown": "无法判断"}[j.Approval]
	if approval == "" {
		approval = "尚未复核"
	}
	services := "尚未恢复原服务状态"
	if j.Services == "restored" {
		services = "已恢复原运行状态"
	}
	lines := []string{"配置：" + config, "审批检查：" + approval, "服务：" + services}
	for _, sv := range j.Plan.Services {
		if sv.Original == "inactive" {
			lines = append(lines, "  "+sv.Unit+"：原本停止；恢复原态不表示服务健康")
		}
	}
	if j.Health == "healthy" {
		lines = append(lines, "服务健康检查：通过")
	}
	if j.Health == "remaining" {
		lines = append(lines, "服务健康检查：未通过")
	}
	for _, step := range j.Steps {
		state := "结果不明，不会重跑"
		if step.Completed && step.Exit != nil {
			state = fmt.Sprintf("退出码 %d", *step.Exit)
		}
		lines = append(lines, "步骤 "+step.ID+"："+state)
	}
	if j.Failure != "" {
		lines = append(lines, "原执行问题："+j.Failure)
	}
	if j.RecoveryFailure != "" {
		lines = append(lines, "服务恢复问题："+j.RecoveryFailure)
	}
	if j.BackupID != "" {
		lines = append(lines, "修改前备份："+j.BackupID)
	}
	return lines
}
func CanContinueAction(j *action.Operation) bool {
	if j == nil || j.Done || j.Published || !j.StageVerified || j.StageTreeDigest == "" || len(j.Steps) == 0 || len(j.Steps) != len(j.Plan.Definition.Steps) {
		return false
	}
	for _, step := range j.Steps {
		if !step.Completed || step.Exit == nil || *step.Exit != 0 {
			return false
		}
	}
	return true
}

// linkLines explains the staged link handling (docs/INSTALL.md 5.2.1).
func linkLines(p *action.Plan) []string {
	var inner, body int
	for _, l := range p.Links {
		if l.Class == action.LinkBody {
			body++
		} else {
			inner++
		}
	}
	var out []string
	if inner > 0 {
		out = append(out, fmt.Sprintf("链接：%d 个指向状态目录内部的绝对链接，执行期间临时指向暂存中的对应位置，交换前还原", inner))
	}
	if body > 0 {
		out = append(out, fmt.Sprintf("链接：%d 个指向程序代码的链接，执行期间改用私有副本（%d 字节）；副本或原代码被改动就不发布", body, p.LinkBytes))
	}
	for _, l := range p.Links {
		out = append(out, "  "+l.Rel+" → "+l.Target)
	}
	return out
}
