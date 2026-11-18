package service

import (
	"encoding/json"
	"slices"

	"valise/core/identity"
	"valise/core/install"
	"valise/core/protection"
	"valise/core/rpc"
	"valise/core/valise"
	"valise/core/vaultrpc"
)

// n2Compatibility refuses an N2 method, or an N2 request field, the server's
// porter has not declared. Older porters decode leniently and would drop a
// field they do not know (schedule.catch_up), so the client must not send it.
func n2Compatibility(hello *rpc.HelloResult, method string, params any) error {
	var capability string
	switch method {
	case install.MethodNativeInfo:
		capability = install.NativeCapability
	case identity.MethodGet:
		capability = identity.Capability
	case protection.MethodGet, protection.MethodApply, protection.MethodStatus, protection.MethodReconcile:
		capability = protection.Capability
	case protection.MethodPrepare:
		capability = protection.Capability
		if usesCatchUp(params) {
			capability = protection.CapabilityV2
		}
	case protection.MethodTargetChangePrepare, protection.MethodTargetChangeApply:
		capability = protection.CapabilityV2
	case vaultrpc.MethodJourney:
		capability = vaultrpc.JourneyCapability
	case vaultrpc.MethodTargetCheck:
		capability = vaultrpc.CopyCheckCapability
	case vaultrpc.MethodLeafMaterials, vaultrpc.MethodTargetVerify:
		capability = vaultrpc.CopyVerifyCapability
	case vaultrpc.MethodTargetRemove:
		capability = vaultrpc.TargetRemoveCapability
	case vaultrpc.MethodCloudPrepare, vaultrpc.MethodCloudForget:
		capability = vaultrpc.CloudCapability
	case protection.MethodRunCancel:
		capability = protection.BackupCapabilityV2
	case protection.MethodBackup, protection.MethodRunStatus, protection.MethodSnapshotBinding:
		capability = protection.BackupCapability
		if method == protection.MethodBackup && usesBackupSelection(params) {
			capability = protection.BackupCapabilityV3
		}
	}
	if (method == vaultrpc.MethodSnapshotPromote || usesSavepointFields(method, params)) &&
		(hello == nil || !slices.Contains(hello.Capabilities, vaultrpc.SavepointsCapability)) {
		return valise.New("unsupported_capability", "服务器搬运工还不支持手动存档点或自动快照退役", "更新服务器上的搬运工后重试")
	}
	if capability != "" && (hello == nil || !slices.Contains(hello.Capabilities, capability)) {
		return valise.New("unsupported_capability", "服务器 porter 尚不支持这项能力："+capability, "更新服务器 porter 后重新准备操作")
	}
	return nil
}

// usesCatchUp reports whether a prepare request sets schedule.catch_up.
func usesCatchUp(params any) bool {
	b, err := json.Marshal(params)
	if err != nil {
		return true // cannot tell: require the capability rather than risk a silent drop
	}
	var p struct {
		Desired struct {
			Schedule struct {
				CatchUp *bool `json:"catch_up"`
			} `json:"schedule"`
		} `json:"desired"`
	}
	if json.Unmarshal(b, &p) != nil {
		return true
	}
	return p.Desired.Schedule.CatchUp != nil
}

func usesBackupSelection(params any) bool {
	b, err := json.Marshal(params)
	if err != nil {
		return true
	}
	var p struct {
		Selection protection.BackupSelection `json:"selection"`
	}
	return json.Unmarshal(b, &p) != nil || p.Selection != (protection.BackupSelection{})
}

func usesSavepointFields(method string, params any) bool {
	switch p := params.(type) {
	case protection.PrepareParams:
		return method == protection.MethodPrepare && p.Desired.Period.KeepAutoPeriods != 0
	case protection.BackupParams:
		return method == protection.MethodBackup && p.Selection.Note != ""
	case vaultrpc.SealParams:
		return method == vaultrpc.MethodSeal && (p.Note != "" || p.Kind != "")
	}
	return false
}
