package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"

	"valise/core/vaultrpc"
)

// The "第一次使用" checklist (docs/UI.md 9.1): ticks come from real state
// (escape pods exist, a copy reached a repository or station, a restore
// test passed); only the two read-only items are ticked by reading them.
// Progress lives on this computer only.

// Checklist item ids.
const (
	GuidePrep        = "prep"
	GuideFirstAgent  = "first_agent"
	GuideFirstPod    = "first_pod"
	GuideOffsite     = "offsite"
	GuideRestoreTest = "restore_test"
	GuideAdminKey    = "admin_key"
	GuideKit         = "kit"
)

// GuideItem is one line of the checklist.
type GuideItem struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Desc     string `json:"desc"`
	Optional bool   `json:"optional,omitempty"`
	Done     bool   `json:"done"`
	Skipped  bool   `json:"skipped,omitempty"`
}

// GuideState is what the user did with the checklist.
type GuideState struct {
	Skipped []string `json:"skipped,omitempty"`
	Read    []string `json:"read,omitempty"`
	// Hidden: no card on the home page (the checklist stays in settings).
	Hidden bool `json:"hidden,omitempty"`
}

const guideFile = "guide.json"

// LoadGuide reads the checklist state.
func (s *Service) LoadGuide() GuideState {
	var g GuideState
	if b, err := os.ReadFile(filepath.Join(s.Dirs.Data, guideFile)); err == nil {
		_ = json.Unmarshal(b, &g)
	}
	return g
}

// SaveGuide stores the checklist state.
func (s *Service) SaveGuide(g GuideState) error {
	b, _ := json.MarshalIndent(g, "", "  ")
	return os.WriteFile(filepath.Join(s.Dirs.Data, guideFile), append(b, '\n'), 0o600)
}

// GuideMark records an item read or skipped (skip toggles).
func (s *Service) GuideMark(id string, read, skip bool) error {
	g := s.LoadGuide()
	if read && !slices.Contains(g.Read, id) {
		g.Read = append(g.Read, id)
	}
	if skip {
		if i := slices.Index(g.Skipped, id); i >= 0 {
			g.Skipped = slices.Delete(g.Skipped, i, i+1)
		} else {
			g.Skipped = append(g.Skipped, id)
		}
	}
	return s.SaveGuide(g)
}

// GuideFacts is what the checklist is ticked from, per server.
type GuideFacts struct {
	Servers int
	// Agents counts enrolled agents over all servers.
	Agents int
	// Status is each loaded server's vault status (no key needed).
	Status map[string]*vaultrpc.StatusResult
}

// Guide is the checklist with its ticks.
func (s *Service) Guide(f GuideFacts) []GuideItem {
	g := s.LoadGuide()
	_, keyErr := s.KeyShow()
	set, profErr := s.Profiles()
	pods, offsite, tested, admin := false, false, false, false
	for host, st := range f.Status {
		if st == nil {
			continue
		}
		pods = pods || len(st.Snapshots) > 0
		for _, t := range st.Targets {
			offsite = offsite || t.LastOK != nil
		}
		for _, r := range s.RepoStatuses(host, st.ServerName) {
			offsite = offsite || r.State.LastOK != nil
		}
		admin = admin || s.HasAdminKey(host)
	}
	for _, byInst := range s.loadRestoreTests() {
		for _, t := range byInst {
			tested = tested || t.OK
		}
	}
	read := func(id string) bool { return slices.Contains(g.Read, id) }
	items := []GuideItem{
		{ID: GuidePrep, Title: "准备工作", Desc: "口令、钥匙、资料包、添加服务器",
			Done: keyErr == nil && profErr == nil && set != nil && len(set.ByID) > 0 && f.Servers > 0},
		{ID: GuideFirstAgent, Title: "第一个 agent", Desc: "在服务器上安装一个 harness，或把已有的 agent 接入 Valise", Done: f.Agents > 0},
		{ID: GuideFirstPod, Title: "建立第一个逃生舱", Desc: "把 agent 现在的样子打包加密存起来，出事时可以退回来", Done: pods},
		{ID: GuideOffsite, Title: "存一份到别处", Desc: "添加本机仓库或空间站，服务器坏了也不丢", Done: offsite},
		{ID: GuideRestoreTest, Title: "做一次恢复测试", Desc: "确认逃生舱真的能打开，不碰正在运行的 agent", Done: tested},
		{ID: GuideAdminKey, Title: "认识管理钥匙", Desc: "敏感操作不用每次输服务器密码", Optional: true, Done: read(GuideAdminKey) || admin},
		{ID: GuideKit, Title: "认识 Kit 与高级模式", Desc: "看看有哪些，以及在哪里打开", Optional: true, Done: read(GuideKit)},
	}
	for i := range items {
		items[i].Skipped = !items[i].Done && slices.Contains(g.Skipped, items[i].ID)
	}
	return items
}

// GuideProgress counts the required items and those done, and finds the
// next required one (neither done nor skipped). Optional items never keep
// the checklist unfinished.
func GuideProgress(items []GuideItem) (done, required int, next *GuideItem) {
	for i := range items {
		if items[i].Optional {
			continue
		}
		required++
		if items[i].Done {
			done++
		} else if !items[i].Skipped && next == nil {
			next = &items[i]
		}
	}
	return done, required, next
}
