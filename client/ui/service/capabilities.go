package service

import (
	"slices"

	"valise/core/identity"
	"valise/core/protection"
	"valise/core/rpc"
	"valise/core/valise"
)

func n2Compatibility(hello *rpc.HelloResult, method string) error {
	var capability string
	switch method {
	case identity.MethodGet:
		capability = identity.Capability
	case protection.MethodGet, protection.MethodPrepare, protection.MethodApply, protection.MethodStatus, protection.MethodReconcile:
		capability = protection.Capability
	case protection.MethodBackup, protection.MethodRunStatus, protection.MethodSnapshotBinding:
		capability = protection.BackupCapability
	}
	if capability != "" && (hello == nil || !slices.Contains(hello.Capabilities, capability)) {
		return valise.New("unsupported_capability", "服务器 porter 尚不支持这项能力："+capability, "更新服务器 porter 后重新准备操作")
	}
	return nil
}
