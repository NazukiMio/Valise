package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"

	"valise/client/profiles"
	"valise/core/profile"
	"valise/core/valise"
)

// ProfileSummary is one usable profile's metadata for status views.
type ProfileSummary struct {
	ID                string `json:"id"`
	DisplayName       string `json:"display_name"`
	Version           int    `json:"profile_version"`
	Status            string `json:"status"`
	Local             bool   `json:"local,omitempty"`
	OverridesOfficial bool   `json:"overrides_official,omitempty"`
	OfficialVersion   int    `json:"official_version,omitempty"`
}

// ProfileDiff summarizes structural differences between an existing effective
// profile and a newly previewed local profile of the same id.
type ProfileDiff struct {
	OldVersion            int      `json:"old_version"`
	NewVersion            int      `json:"new_version"`
	AddedEntries          []string `json:"added_entries,omitempty"`
	RemovedEntries        []string `json:"removed_entries,omitempty"`
	AddedActions          []string `json:"added_actions,omitempty"`
	RemovedActions        []string `json:"removed_actions,omitempty"`
	NativeCLIChanged      bool     `json:"native_cli_changed,omitempty"`
	NativeCLIOld          []string `json:"native_cli_old,omitempty"`
	NativeCLINew          []string `json:"native_cli_new,omitempty"`
	NativeSessionChanged  bool     `json:"native_session_changed,omitempty"`
	NativeSessionOld      []string `json:"native_session_old,omitempty"`
	NativeSessionNew      []string `json:"native_session_new,omitempty"`
	ServiceInstallChanged bool     `json:"service_install_changed,omitempty"`
	ServiceInstallOld     []string `json:"service_install_old,omitempty"`
	ServiceInstallNew     []string `json:"service_install_new,omitempty"`
	AgentKitChanged       bool     `json:"agent_kit_changed,omitempty"`
	PathRulesDelta        int      `json:"path_rules_delta,omitempty"`
	SQLiteDelta           int      `json:"sqlite_delta,omitempty"`
	Highlights            []string `json:"highlights,omitempty"`
}

// ProfileSyncTarget names one already-enrolled instance to re-enroll with an
// updated profile.
type ProfileSyncTarget struct {
	Host       string `json:"host"`
	InstanceID string `json:"instance_id"`
	Name       string `json:"name,omitempty"`
}

// ProfileSyncOutcome is one instance's independent sync result.
type ProfileSyncOutcome struct {
	Host       string `json:"host"`
	InstanceID string `json:"instance_id"`
	OK         bool   `json:"ok"`
	Written    string `json:"written,omitempty"`
	Err        string `json:"err,omitempty"`
}

func diffProfiles(oldP, newP *profile.Profile) *ProfileDiff {
	if oldP == nil || newP == nil {
		return nil
	}
	d := &ProfileDiff{
		OldVersion: oldP.ProfileVersion,
		NewVersion: newP.ProfileVersion,
	}
	if oldP.ProfileVersion != newP.ProfileVersion {
		d.Highlights = append(d.Highlights, fmt.Sprintf("版本：v%d → v%d", oldP.ProfileVersion, newP.ProfileVersion))
	}

	d.AddedEntries, d.RemovedEntries = diffIDs(entryIDs(oldP.Entries), entryIDs(newP.Entries))
	if len(d.AddedEntries) > 0 {
		d.Highlights = append(d.Highlights, "新增入口："+strings.Join(d.AddedEntries, "、"))
	}
	if len(d.RemovedEntries) > 0 {
		d.Highlights = append(d.Highlights, "移除入口："+strings.Join(d.RemovedEntries, "、"))
	}

	d.AddedActions, d.RemovedActions = diffIDs(actionIDs(oldP.Actions), actionIDs(newP.Actions))
	if len(d.AddedActions) > 0 {
		d.Highlights = append(d.Highlights, "新增管理动作："+strings.Join(d.AddedActions, "、"))
	}
	if len(d.RemovedActions) > 0 {
		d.Highlights = append(d.Highlights, "移除管理动作："+strings.Join(d.RemovedActions, "、"))
	}

	if !slices.Equal(oldP.NativeCLI, newP.NativeCLI) {
		d.NativeCLIChanged = true
		d.NativeCLIOld = append([]string(nil), oldP.NativeCLI...)
		d.NativeCLINew = append([]string(nil), newP.NativeCLI...)
		d.Highlights = append(d.Highlights, "命令行与向导命令已变化")
	}
	if !slices.Equal(oldP.NativeSessionCLI, newP.NativeSessionCLI) {
		d.NativeSessionChanged = true
		d.NativeSessionOld = append([]string(nil), oldP.NativeSessionCLI...)
		d.NativeSessionNew = append([]string(nil), newP.NativeSessionCLI...)
		d.Highlights = append(d.Highlights, "日常对话命令已变化")
	}

	oldSvcInstall := serviceInstallArgv(oldP)
	newSvcInstall := serviceInstallArgv(newP)
	if !slices.Equal(oldSvcInstall, newSvcInstall) {
		d.ServiceInstallChanged = true
		d.ServiceInstallOld = append([]string(nil), oldSvcInstall...)
		d.ServiceInstallNew = append([]string(nil), newSvcInstall...)
		d.Highlights = append(d.Highlights, "服务安装命令已变化")
	}

	if !jsonEqual(oldP.AgentKit, newP.AgentKit) {
		d.AgentKitChanged = true
		d.Highlights = append(d.Highlights, "Agent 工具包配置已变化")
	}

	d.PathRulesDelta = len(newP.Paths) - len(oldP.Paths)
	if d.PathRulesDelta != 0 {
		d.Highlights = append(d.Highlights, fmt.Sprintf("路径规则数量变化：%+d", d.PathRulesDelta))
	}

	d.SQLiteDelta = sqliteRuleCount(newP) - sqliteRuleCount(oldP)
	if d.SQLiteDelta != 0 {
		d.Highlights = append(d.Highlights, fmt.Sprintf("SQLite 规则数量变化：%+d", d.SQLiteDelta))
	}

	return d
}

func entryIDs(entries []profile.Entry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.ID)
	}
	return out
}

func actionIDs(actions []profile.Action) []string {
	out := make([]string, 0, len(actions))
	for _, a := range actions {
		out = append(out, a.ID)
	}
	return out
}

func diffIDs(oldIDs, newIDs []string) (added, removed []string) {
	oldSet := map[string]bool{}
	for _, id := range oldIDs {
		oldSet[id] = true
	}
	newSet := map[string]bool{}
	for _, id := range newIDs {
		newSet[id] = true
		if !oldSet[id] {
			added = append(added, id)
		}
	}
	for _, id := range oldIDs {
		if !newSet[id] {
			removed = append(removed, id)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return added, removed
}

func serviceInstallArgv(p *profile.Profile) []string {
	if p == nil || p.Service == nil {
		return nil
	}
	return p.Service.InstallArgv
}

func sqliteRuleCount(p *profile.Profile) int {
	if p == nil || p.Consistency == nil {
		return 0
	}
	return len(p.Consistency.SQLite)
}

func jsonEqual(a, b any) bool {
	ab, errA := json.Marshal(a)
	bb, errB := json.Marshal(b)
	if errA != nil || errB != nil {
		return false
	}
	return string(ab) == string(bb)
}

// RemoveLocalProfile deletes a local profile YAML by id, restoring any
// overridden official profile from the installed pack.
func (s *Service) RemoveLocalProfile(id string) error {
	if err := profiles.RemoveLocalFile(s.Dirs.Data, id); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return valise.New("local_profile_not_found", "没有叫 "+id+" 的本地资料", "刷新资料列表后确认")
		}
		return valise.New("local_profile_remove", "无法移除本地资料", "检查数据目录权限与资料 id").WithDetail(err.Error())
	}
	return nil
}

// SyncProfileToInstances re-enrolls each target instance against the current
// effective profileID, returning an independent outcome for each instance.
func (s *Service) SyncProfileToInstances(ctx context.Context, profileID string, targets []ProfileSyncTarget) ([]ProfileSyncOutcome, error) {
	if profileID == "" {
		return nil, valise.New("request_invalid", "缺少要同步的 harness 资料 id", "选择一份资料后重试")
	}
	set, err := s.Profiles()
	if err != nil {
		return nil, err
	}
	if set.ByID[profileID] == nil {
		return nil, valise.New("profile_missing", "本机没有 harness 资料 "+profileID, "先导入资料或安装资料包")
	}
	outcomes := make([]ProfileSyncOutcome, 0, len(targets))
	for _, t := range targets {
		o := ProfileSyncOutcome{Host: t.Host, InstanceID: t.InstanceID}
		if err := ctx.Err(); err != nil {
			o.Err = errText1(err)
			outcomes = append(outcomes, o)
			continue
		}
		res, err := s.Enroll(ctx, t.Host, EnrollOptions{
			ProfileID:  profileID,
			Name:       t.Name,
			InstanceID: t.InstanceID,
			Accept:     true,
		})
		if err != nil {
			o.Err = errText1(err)
		} else {
			o.OK = true
			if res != nil {
				o.Written = res.Written
			}
		}
		outcomes = append(outcomes, o)
	}
	return outcomes, nil
}
