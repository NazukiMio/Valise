package service

import (
	"context"
	"errors"

	"valise/core/protection"
	"valise/core/stationsync"
	"valise/core/valise"
	"valise/core/vaultrpc"
)

// ErrCheckUnsupported: the server's porter predates target_check, so a
// location can only be tested by copying to it.
var ErrCheckUnsupported = errors.New("check unsupported")

// CheckTarget has host log in to a copy location once, read-only, before it
// is saved or to explain a failure (N2 G.3). A location the server cannot
// reach is a result (Reachable false), not an error.
func (s *Service) CheckTarget(ctx context.Context, host string, p vaultrpc.TargetCheckParams) (*vaultrpc.TargetCheckResult, error) {
	var res vaultrpc.TargetCheckResult
	err := s.withSession(ctx, host, func(c caller) error {
		return c.Call(ctx, vaultrpc.MethodTargetCheck, p, &res)
	})
	if ue, ok := valise.As(err); ok && (ue.Code == "unsupported_capability" || ue.Code == "method_not_found") {
		return nil, ErrCheckUnsupported
	}
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// CopyCause says, for a person, what a copy failure cause means and what
// to do about it (N2 G.2). Locations are named as the person knows them.
func CopyCause(cause, name string) (what, remedy string) {
	switch cause {
	case stationsync.CauseConnectTimeout, stationsync.CauseConnectRefused:
		what = "服务器连不上 " + name
		if cause == stationsync.CauseConnectRefused {
			what += "（对方拒绝连接）"
		} else {
			what += "（连接超时）"
		}
		return what + "：服务器可能在 NAT 后或另一个网络里，例如云服务器连不到家里的 NAS",
			"可以用 Tailscale 等组网工具让两边互通，或换成服务器能访问的地址；也可以经这台电脑中转（只在 Valise 打开时）"
	case stationsync.CauseHostUnknown:
		return "服务器不认识 " + name + " 的主机名", "在服务器的 ~/.ssh/config 里写上这个 Host，或改用 IP 地址"
	case stationsync.CauseAuthDenied:
		return name + " 拒绝了服务器的登录", "确认服务器用来登录 " + name + " 的钥匙已经装在对方上"
	case stationsync.CauseHostKeyUnverified:
		return "服务器还没确认过 " + name + " 的身份，或者它的身份变了", "在服务器上用 ssh 登录一次 " + name + " 并核对指纹"
	case stationsync.CauseNotWritable:
		return "服务器能登录 " + name + "，但写不进目标文件夹", "检查对方文件夹的权限与剩余空间"
	case stationsync.CauseToolMissing:
		return "服务器或 " + name + " 上没有可用的 rsync 或 sftp", "在两边装上 rsync，或确认对方开启了 SFTP"
	}
	return "复制到 " + name + " 失败", "再试一次；仍然失败时检查服务器能否用 SSH 登录 " + name
}

// copyItems turns the plan's latest failed copies into health items.
func copyItems(r *HealthReport, st []protection.CopyStatus) {
	for _, c := range st {
		if c.State != "failed" {
			continue
		}
		what, remedy := CopyCause(c.FailureCause, c.TargetRef.ID)
		r.Items = append(r.Items, HealthItem{ID: "copy-" + c.TargetRef.ID, Group: HealthAgent, Level: HealthWarn,
			Title: "复制到 " + c.TargetRef.ID, Text: "最近一次（" + c.At.Local().Format("01-02 15:04") + "）没有复制成功：" + what,
			Detail: remedy + "。备份本身已保存在服务器上。"})
	}
}
