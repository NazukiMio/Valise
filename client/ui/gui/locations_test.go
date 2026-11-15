package gui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"valise/client/config"
	"valise/core/identity"
	"valise/core/kittools"
	"valise/core/protection"
	"valise/core/vaultrpc"
	"valise/ui/service"
)

// locWorld has a folder, another machine and a cloud, with home's vault
// read: two periods of 小满, the current one not yet at the machine.
func locWorld(t *testing.T) (*App, *ui.Tester) {
	a, tt := world(t)
	dir := filepath.Join(t.TempDir(), "usb")
	os.MkdirAll(dir, 0o700)
	a.svc.Config.Repos = []config.Repo{{Name: "usb", Path: dir}}
	a.svc.Config.Clouds = []config.Cloud{{Name: "gdrive", Cloud: "google-drive"}}
	if err := config.SaveStations(a.svc.Dirs.Data, []config.Station{{Name: "nas", Address: "nas.home", User: "valise", Path: "/volume1/valise"}}); err != nil {
		t.Fatal(err)
	}
	ok := time.Now().Add(-time.Hour)
	ls := a.locs()
	ls.read = true
	ls.statuses["home"] = &vaultrpc.StatusResult{ServerName: "home", Leaves: []vaultrpc.LeafState{
		{Leaf: "xiaoman/2026-11-01_2026-11-30", Current: true, Snapshots: []string{"a", "b"}, Bytes: 2048},
		{Leaf: "xiaoman/2026-10-01_2026-10-31", Snapshots: []string{"c"}, Bytes: 1024}},
		Targets: []vaultrpc.TargetState{{Target: vaultrpc.Target{Name: "nas", Kind: "rsync", Auto: &vaultrpc.AutoSync{Every: "1d"}}, LastOK: &ok}}}
	ls.lists["nas/home"] = &listRead{list: &vaultrpc.TargetListResult{Leaves: []vaultrpc.RemoteLeaf{{Leaf: "xiaoman/2026-10-01_2026-10-31", Snapshots: []string{"c"}}}}}
	ls.lists["nas/cloud"] = &listRead{list: &vaultrpc.TargetListResult{}}
	return a, tt
}

// The page lists each location with its servers under it; what is
// logged in, synced and missing are said apart.
func TestLocationsPage(t *testing.T) {
	a, tt := locWorld(t)
	a.router.Push("/locations")
	tt.Advance(time.Second)
	hasAll(t, tt.Texts(), "添加备份位置…", "usb", "本机文件夹", "现在补齐", "nas", "另一台机器", "同步", "gdrive", "云盘",
		"服务器：未登录 · 这台电脑：已登录", "home", "cloud", "有 3 个新备份没补", when(time.Now().Add(-time.Hour), time.Now())+" 已同步 · 自动 · 至多每天一次", "还没同步过")
}

// A location's page lists each server's periods by agent: what the
// server and the location have, and the buttons only where they apply.
func TestLocationPage(t *testing.T) {
	a, tt := locWorld(t)
	a.router.Push("/locations/nas")
	tt.Advance(time.Second)
	hasAll(t, tt.Texts(), "nas", "移除…", "自动同步 home", "备份后顺带同步，至多每天一次", "小满",
		"2026-11-01 ~ 2026-11-30（进行中）", "这里：没有", "2026-10-01 ~ 2026-10-31", "这里：有 · 未核对", "核对…", "删除…")
	click(t, tt, "删除…")
	if v := a.locs().verify; v == nil || !v.del || v.where != "nas" || v.leaf != "xiaoman/2026-10-01_2026-10-31" {
		t.Fatalf("delete: %+v", v)
	}
}

// Adding starts from the kind; the server's own SSH host is under 更多选项.
func TestLocationAdd(t *testing.T) {
	a, tt := locWorld(t)
	a.openLocAdd()
	tt.Advance(time.Second)
	hasAll(t, tt.Texts(), "添加备份位置", "本机文件夹", "另一台机器", "云盘", "更多选项", "名字", "文件夹", "选择…")
	if tt.HasText("服务器的 SSH 主机") {
		t.Error("the SSH host kind is not folded")
	}
	click(t, tt, "另一台机器")
	hasAll(t, tt.Texts(), "地址", "端口", "用户", "目录", "服务器的地址会变（家宽、动态地址）", "下一步")
}

// 备份后复制到: the server half and this computer's half in one dialog.
func TestCopyToDialog(t *testing.T) {
	a, tt := locWorld(t)
	ref := identity.AgentRef{ServerUID: "u", InstallID: "openclaw", InstanceID: "xiaoman", Generation: "g"}
	a.agents.value.Items[0].IdentityState, a.agents.value.Items[0].AgentRef = identity.Bound, &ref
	on := true
	ps := &protState{ref: &ref, view: &protection.View{ServerPlan: &protection.ServerPlan{Revision: 1, Desired: protection.Desired{
		Schedule: protection.Schedule{Enabled: &on, DailyAt: "04:40", Timezone: "UTC"}, Period: protection.Period{Unit: "month"},
		ServerCopies: []protection.ServerCopy{{TargetRef: protection.LocationRef{Kind: "legacy_target", ID: "nas", AuthorityID: "u"}, Enabled: &on, MinimumInterval: "24h"}}}}}}
	ps.copyTo = &copyEdit{targets: a.locs().statuses["home"].Targets, server: map[string]*serverCopyEdit{"nas": {on: true, every: "24h"}},
		local: &service.LocalCopiesView{Planned: true}, repos: map[string]*[2]bool{"usb": {true, false}}}
	a.prots["home/xiaoman"] = ps
	a.agentTab = tabManage
	a.router.Push("/agents/home/xiaoman")
	tt.Advance(time.Second)
	hasAll(t, tt.Texts(), "小满 · 备份后复制到", "服务器直传", "nas", "至多每天一次", "这台电脑补齐（只在 Valise 打开时）", "usb", "备份后", "打开时")
}

// TestLocationShots renders the locations segment (docs/UI.md 7.9).
func TestLocationShots(t *testing.T) {
	if os.Getenv("VALISE_SHOTS") == "" {
		t.Skip("VALISE_SHOTS not set")
	}
	a, tt := locWorld(t)
	step := func(path, name string) {
		if path != "" {
			a.router.Push(path)
		}
		tt.Frame()
		tt.Advance(time.Second)
		shot(t, tt, name)
	}
	a.locs().syncs["nas"] = &locSync{lines: []syncLine{{host: "home", text: "3 个备份，2 个周期", path: "经这台电脑临时中转（临时文件已删除）：服务器直连 nas 被拒（证书过期）", checks: "复制后核对 11 月周期：齐至现在"}}}
	step("/locations", "loc-list")
	step("/locations/nas", "loc-detail")
	a.locs().verify = &verifyRun{host: "home", leaf: "xiaoman/2026-10-01_2026-10-31", where: "nas", del: true,
		rep: &service.CopyReport{Checks: []service.CopyCheck{{Where: "server", Kind: service.KindServer, State: service.CopyAbsent}, {Where: "usb", Kind: service.KindRepo, State: service.CopyComplete, Snapshots: 1}}}}
	step("", "loc-delete")
	a.locs().verify = nil
	a.openLocAdd()
	step("", "loc-add")
	a.locAdding.kind = locStation
	step("", "loc-add-station")
	no := false
	a.cloudAdding = &cloudAdd{read: true, localOK: true, name: "gdrive", id: "google-drive", clouds: []kittools.Cloud{
		{ID: "google-drive", Name: "Google Drive", Access: kittools.AccessFolder, AccessNote: "只能访问 Valise 自己建的文件", AskApp: true, AppHelp: "https://rclone.org/drive/"},
		{ID: "dropbox", Name: "Dropbox", Access: kittools.AccessAccount, AccessNote: "整个网盘", Loopback: &no},
		{ID: "jianguoyun", Name: "坚果云", Access: kittools.AccessAccount, AccessNote: "整个网盘", Verify: "pending"}}}
	step("/locations/add/cloud", "loc-cloud")
}

// A folder name that breaks the rule says so; picking a folder suggests one.
func TestRepoAddName(t *testing.T) {
	a, tt := locWorld(t)
	dir := filepath.Join(t.TempDir(), "My Disk")
	a.pickFolder = func(_, _ string) (string, error) { return dir, nil }
	a.openLocAdd()
	tt.Advance(time.Second)
	click(t, tt, "选择…")
	tt.Frame()
	if a.locAdding.name != "my-disk" {
		t.Fatalf("suggested %q", a.locAdding.name)
	}
	a.locAdding.name = "U盘"
	tt.Frame()
	hasAll(t, tt.Texts(), T("set.name_rule"))
	click(t, tt, "添加")
	if a.locAdding.done != "" {
		t.Fatal("added with a bad name")
	}
	a.locAdding.name = "my-disk"
	tt.Frame()
	click(t, tt, "添加")
	if a.locAdding.done != "my-disk" {
		t.Fatalf("not added: %v", a.locAdding.j.err)
	}
}

// The cloud form: the own app is optional and said to go to rclone only;
// rclone's question is shown with its choices and answered with 下一步.
func TestCloudForm(t *testing.T) {
	a, tt := locWorld(t)
	a.cloudAdding = &cloudAdd{read: true, localOK: true, name: "gdrive", id: "google-drive", clouds: []kittools.Cloud{
		{ID: "google-drive", Name: "Google Drive", Access: kittools.AccessFolder, AccessNote: "只能访问 Valise 自己建的文件", AskApp: true}}}
	a.router.Push("/locations/add/cloud")
	tt.Advance(time.Second)
	hasAll(t, tt.Texts(), "用你自己的应用（可选）", "client_id", "client_secret", T("loc.app_shared"))
	fq := &formQuestion{q: kittools.Question{State: "d", Name: "config_change_team_drive", Help: "Configure this as a Shared Drive?\nMore words.",
		Default: "false", Only: true, Choices: []kittools.Choice{{Value: "true", Help: "Yes"}, {Value: "false", Help: "No"}}}, answer: "false", reply: make(chan string, 1)}
	a.cloudAdding.login = &cloudLogin{where: "home", form: true, q: fq, j: job{busy: true}}
	tt.Frame()
	hasAll(t, tt.Texts(), "正在 home 上登录", "Configure this as a Shared Drive?", "More words.", "true · Yes", "false · No")
	shot(t, tt, "loc-cloud-form")
	click(t, tt, "下一步")
	if got := <-fq.reply; got != "false" {
		t.Fatalf("answer %q", got)
	}
}
