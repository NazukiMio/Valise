package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"valise/core/valise"
)

// Fake system tools on the fake host (fake-ssh only proves the protocol and
// Valise's own logic; real loginctl/timedatectl/polkit behaviour needs a
// target machine).
const (
	fakeLoginctl = `#!/bin/sh
case "$1" in
 show-user) cat "$HOME/linger" 2>/dev/null || echo no ;;
 enable-linger)
  echo "$*" >> "$HOME/ran"
  if test -e "$HOME/deny"; then echo 'Could not enable linger: Access denied' >&2; exit 1; fi
  if test -e "$HOME/lose"; then mv "$HOME/.local/bin/valise-porter" "$HOME/.local/bin/porter-gone"; fi
  printf yes > "$HOME/linger" ;;
esac
`
	fakeTimedatectl = `#!/bin/sh
case "$1" in
 show) printf 'CanNTP=yes\nNTP=%s\nNTPSynchronized=%s\n' "$(cat "$HOME/ntp" 2>/dev/null || echo no)" "$(cat "$HOME/synced" 2>/dev/null || echo no)" ;;
 set-ntp) echo "$*" >> "$HOME/ran"; printf yes > "$HOME/ntp" ;;
esac
`
	fakeSystemctl = `#!/bin/sh
case "$*" in
 *"show systemd-timesyncd.service"*) echo loaded ;;
 *"restart systemd-timesyncd"*) echo "$*" >> "$HOME/ran"; if test -e "$HOME/restart-helps"; then printf yes > "$HOME/synced"; fi ;;
 *) printf 'LoadState=not-found\n' ;;
esac
`
	fakeSudo = "#!/bin/sh\necho sudo >> \"$HOME/ran\"\nexec \"$@\"\n"
)

func systemWorld(t *testing.T) (*Service, string) {
	t.Helper()
	s, home := enrollWorld(t)
	bin := filepath.Join(home, ".local/bin")
	for name, body := range map[string]string{"loginctl": fakeLoginctl, "timedatectl": fakeTimedatectl, "systemctl": fakeSystemctl, "sudo": fakeSudo} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Enroll(context.Background(), "box", EnrollOptions{ProfileID: "demo", Accept: true}); err != nil {
		t.Fatal(err)
	}
	oldSettle, oldPoll := systemSettle, systemPoll
	systemSettle, systemPoll = 0, time.Millisecond
	t.Cleanup(func() { systemSettle, systemPoll = oldSettle, oldPoll })
	return s, home
}

func write(t *testing.T, path, body string) {
	t.Helper()
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func findItem(r *HealthReport, id string) *HealthItem {
	for i := range r.Items {
		if r.Items[i].ID == id {
			return &r.Items[i]
		}
	}
	return nil
}

func ran(t *testing.T, home string) string {
	b, _ := os.ReadFile(filepath.Join(home, "ran"))
	return string(b)
}

func audit(t *testing.T, home string) string {
	b, _ := os.ReadFile(filepath.Join(home, ".local/state/valise/audit.jsonl"))
	return string(b)
}

func TestSystemHealthDetection(t *testing.T) {
	s, home := systemWorld(t)
	ctx := context.Background()
	write(t, filepath.Join(home, "ntp"), "yes")
	write(t, filepath.Join(home, "synced"), "yes")
	// Nothing scheduled: linger off is not a problem.
	r, _ := s.Health(ctx, "box", "demo")
	if findItem(r, FixLinger) != nil || findItem(r, FixClock) != nil {
		t.Fatalf("unexpected: %+v", r.Items)
	}
	// A timer depends on linger.
	write(t, filepath.Join(home, ".config/systemd/user/valise-seal@demo.timer"), "[Timer]\nOnUnitActiveSec=21600s\n")
	r, _ = s.Health(ctx, "box", "demo")
	it := findItem(r, FixLinger)
	if it == nil || it.Level != HealthWarn || !strings.Contains(it.Text, "退出登录或服务器重启后，定时备份不会运行") ||
		it.SystemFix != FixLinger || it.Key != "f" || it.ActionName != "开启 linger" || !strings.Contains(it.Detail, "loginctl enable-linger") {
		t.Fatalf("%+v", r.Items)
	}
	if tm := findItem(r, "timer"); tm == nil || !strings.Contains(tm.Text, "见 linger 一项") || tm.Counts() {
		t.Fatalf("timer %+v", tm)
	}
	write(t, filepath.Join(home, "linger"), "yes")
	r, _ = s.Health(ctx, "box", "demo")
	if findItem(r, FixLinger) != nil {
		t.Fatal("linger on still reported")
	}
	// NTP not synchronised.
	write(t, filepath.Join(home, "synced"), "no")
	r, _ = s.Health(ctx, "box", "demo")
	it = findItem(r, FixClock)
	if it == nil || !strings.Contains(it.Text, "还没有对上时间") || !strings.Contains(it.Detail, "不会用这台电脑的时间改写服务器时钟") {
		t.Fatalf("%+v", r.Items)
	}
	// ssh-config servers get no advanced mode: the clock fix is manual only.
	if it.SystemFix != "" || it.Key == "f" || !strings.Contains(it.Detail, "sudo systemctl restart systemd-timesyncd") {
		t.Fatalf("%+v", it)
	}
	for _, c := range r.Checks {
		if c.ID == "system" && c.State != "ok" {
			t.Fatalf("%+v", c)
		}
	}
}

// Plans and skew are judged without the server, as in collectHealth.
func TestSystemItemsRules(t *testing.T) {
	s := newService(t, "ssh")
	now := time.Now()
	run := func(sr *systemRead, legacyTimer bool) *HealthReport {
		r := &HealthReport{}
		s.systemItems(r, "box", "demo", func() (*systemRead, error) { return sr, nil }, legacyTimer, false, time.Time{})
		return r
	}
	ok := &SystemStatus{Now: now, User: "alice", Linger: "no", NTP: "yes", NTPSynchronized: "yes"}
	if r := run(&systemRead{status: ok, scheduled: true}, false); findItem(r, FixLinger) == nil || findItem(r, FixClock) != nil {
		t.Fatalf("accepted scheduled plan: %+v", r.Items)
	}
	if r := run(&systemRead{status: ok}, false); findItem(r, FixLinger) != nil {
		t.Fatal("no schedule, no linger item")
	}
	unknown := *ok
	unknown.Linger = "unknown"
	if r := run(&systemRead{status: &unknown, scheduled: true}, false); findItem(r, FixLinger) != nil {
		t.Fatal("unknown linger reported as off")
	}
	skewed := *ok
	skewed.Now = now.Add(-7 * time.Minute)
	if r := run(&systemRead{status: &skewed}, false); findItem(r, FixClock) == nil || !strings.Contains(findItem(r, FixClock).Text, "差 7 分钟") {
		t.Fatalf("skew: %+v", r.Items)
	}
	small := *ok
	small.Now = now.Add(4 * time.Minute)
	if r := run(&systemRead{status: &small}, false); findItem(r, FixClock) != nil {
		t.Fatal("4 minutes reported")
	}
	// Old porter: unsupported, the legacy timer still drives a manual item.
	r := run(&systemRead{statusErr: errSystemUnsupported}, true)
	if it := findItem(r, FixLinger); it == nil || it.SystemFix != "" || !strings.Contains(it.Detail, "sudo loginctl enable-linger") {
		t.Fatalf("%+v", r.Items)
	}
	if r.Checks[0].State != "unsupported" || r.Incomplete() != 0 {
		t.Fatalf("%+v", r.Checks)
	}
}

func TestLingerFixDirect(t *testing.T) {
	s, home := systemWorld(t)
	ctx := context.Background()
	f, err := s.PlanSystemFix(ctx, "box", "demo", FixLinger, SystemFixOptions{})
	if err != nil {
		t.Fatal(err)
	}
	user := strings.Fields(strings.Join(f.Steps[0].Command, " "))[2]
	if f.Mode != FixDirect || strings.Join(f.Steps[0].Command, " ") != "loginctl enable-linger "+user ||
		!strings.Contains(f.Steps[0].Change, "让 systemd 在你登出后继续运行你的用户服务") || f.Steps[0].Undo != "loginctl disable-linger "+user ||
		!strings.Contains(f.Access, "新开一次管理登录") || f.Digest == "" {
		t.Fatalf("%+v", f)
	}
	if ran(t, home) != "" {
		t.Fatal("planning ran a command")
	}
	if _, err := s.RunSystemFix(ctx, "box", "demo", FixLinger, "other", SystemFixOptions{}, InstallIO{}); err == nil {
		t.Fatal("ran a plan that was not confirmed")
	}
	res, err := s.RunSystemFix(ctx, "box", "demo", FixLinger, f.Digest, SystemFixOptions{}, InstallIO{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Steps) != 1 || !res.Steps[0].Ran || res.Steps[0].Exit != 0 || res.Verification.State != "resolved" || !res.Audited || res.Next != "" {
		t.Fatalf("%+v %+v", res, res.Verification)
	}
	if !strings.Contains(ran(t, home), "enable-linger "+user) || strings.Contains(ran(t, home), "sudo") {
		t.Fatal(ran(t, home))
	}
	if a := audit(t, home); !strings.Contains(a, `"op":"system_fix"`) || !strings.Contains(a, "loginctl enable-linger "+user+" exit 0") || !strings.Contains(a, `"result":"resolved"`) {
		t.Fatal(a)
	}
	if _, err := s.PlanSystemFix(ctx, "box", "demo", FixLinger, SystemFixOptions{}); err == nil {
		t.Fatal("planned a fix for a solved problem")
	}
}

func TestLingerFixDeniedAndLost(t *testing.T) {
	s, home := systemWorld(t)
	ctx := context.Background()
	write(t, filepath.Join(home, "deny"), "")
	f, _ := s.PlanSystemFix(ctx, "box", "demo", FixLinger, SystemFixOptions{})
	res, err := s.RunSystemFix(ctx, "box", "demo", FixLinger, f.Digest, SystemFixOptions{}, InstallIO{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Steps[0].Exit != 1 || res.Verification.State != "remaining" || !strings.Contains(res.Next, "高级模式") || !strings.Contains(res.Next, "sudo loginctl enable-linger") {
		t.Fatalf("%+v %+v", res, res.Verification)
	}
	if !strings.Contains(audit(t, home), `"result":"remaining"`) {
		t.Fatal(audit(t, home))
	}
	// The command succeeds but the server can no longer be read: unknown,
	// never resolved, and the missing audit is reported.
	os.Remove(filepath.Join(home, "deny"))
	write(t, filepath.Join(home, "lose"), "")
	res, err = s.RunSystemFix(ctx, "box", "demo", FixLinger, f.Digest, SystemFixOptions{}, InstallIO{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Steps[0].Exit != 0 || res.Verification.State != "unknown" || !strings.Contains(res.Verification.Text, "无法复核") || res.Audited {
		t.Fatalf("%+v %+v", res, res.Verification)
	}
}

func TestClockFixManualWithoutAdvanced(t *testing.T) {
	s, home := systemWorld(t)
	ctx := context.Background()
	f, err := s.PlanSystemFix(ctx, "box", "demo", FixClock, SystemFixOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if f.Mode != FixManual || len(f.Steps) != 0 || joinCommands(f.Manual) != "sudo timedatectl set-ntp true；sudo systemctl restart systemd-timesyncd" ||
		!strings.Contains(f.Why, "高级模式") {
		t.Fatalf("%+v", f)
	}
	_, err = s.RunSystemFix(ctx, "box", "demo", FixClock, f.Digest, SystemFixOptions{}, InstallIO{})
	if ue, ok := valise.As(err); !ok || ue.Code != "system_fix_manual" || !strings.Contains(ue.Suggestion, "sudo timedatectl set-ntp true") {
		t.Fatalf("%v", err)
	}
	if ran(t, home) != "" || audit(t, home) != "" {
		t.Fatal("manual fix ran or audited")
	}
}

func TestClockFixAdvanced(t *testing.T) {
	for _, helps := range []bool{true, false} {
		s, home := systemWorld(t)
		ctx := context.Background()
		s.full.host = "box" // advanced mode the user enabled (the challenge is tested elsewhere)
		if helps {
			write(t, filepath.Join(home, "restart-helps"), "")
		}
		f, err := s.PlanSystemFix(ctx, "box", "demo", FixClock, SystemFixOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if f.Mode != FixAdvanced || !f.Ready || len(f.Steps) != 2 || strings.Join(f.Steps[0].Command, " ") != "sudo timedatectl set-ntp true" ||
			f.Steps[1].When == "" || !strings.Contains(f.Steps[0].Change, "NTP") || !strings.Contains(f.Access, "sudo") {
			t.Fatalf("%+v", f)
		}
		if b := SystemFixSteps(f); b.Refused || len(b.Commands) != 2 {
			t.Fatalf("%+v", b)
		}
		res, err := s.RunSystemFix(ctx, "box", "demo", FixClock, f.Digest, SystemFixOptions{}, InstallIO{})
		if err != nil {
			t.Fatal(err)
		}
		if !res.Steps[0].Ran || !res.Steps[1].Ran || !strings.Contains(ran(t, home), "restart systemd-timesyncd") || !strings.Contains(ran(t, home), "sudo") {
			t.Fatalf("%+v\n%s", res, ran(t, home))
		}
		want := map[bool]string{true: "resolved", false: "remaining"}[helps]
		if res.Verification.State != want {
			t.Fatalf("%v: %+v", helps, res.Verification)
		}
		if !helps && !strings.Contains(res.Next, "UDP 123") {
			t.Fatal(res.Next)
		}
		if !strings.Contains(audit(t, home), `"result":"`+want+`"`) || !strings.Contains(audit(t, home), "(advanced)") {
			t.Fatal(audit(t, home))
		}
	}
}

// Once NTP catches up after set-ntp, the conditional restart is skipped.
func TestClockFixSkipsRestart(t *testing.T) {
	s, home := systemWorld(t)
	ctx := context.Background()
	s.full.host = "box"
	td := strings.Replace(fakeTimedatectl, `printf yes > "$HOME/ntp" ;;`, `printf yes > "$HOME/ntp"; printf yes > "$HOME/synced" ;;`, 1)
	write(t, filepath.Join(home, ".local/bin/timedatectl"), td)
	os.Chmod(filepath.Join(home, ".local/bin/timedatectl"), 0o755)
	f, _ := s.PlanSystemFix(ctx, "box", "demo", FixClock, SystemFixOptions{})
	res, err := s.RunSystemFix(ctx, "box", "demo", FixClock, f.Digest, SystemFixOptions{}, InstallIO{})
	if err != nil || res.Steps[1].Ran || res.Steps[1].Note == "" || res.Verification.State != "resolved" {
		t.Fatalf("%+v %v", res, err)
	}
	if strings.Contains(ran(t, home), "restart") {
		t.Fatal(ran(t, home))
	}
}
