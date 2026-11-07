package porter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"valise/client/ssh"
	"valise/core/identity"
	"valise/core/valise"
)

// Install locations. Deploy always targets ~/.local/bin and never uses sudo.
const (
	InstallDir  = "$HOME/.local/bin"
	InstallPath = InstallDir + "/valise-porter"
	tmpPath     = InstallPath + ".tmp"
)

// Platform is a GOOS/GOARCH pair.
type Platform struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

func (p Platform) String() string { return p.OS + "/" + p.Arch }

// ReleaseSupported reports whether Releases ship a porter for p.
func (p Platform) ReleaseSupported() bool {
	return p.OS == "linux" && (p.Arch == "amd64" || p.Arch == "arm64")
}

// RemotePlatform maps `uname -s -m` to Go names.
func RemotePlatform(ctx context.Context, t *ssh.Transport) (Platform, error) {
	res, err := t.Run(ctx, "uname -s -m", nil)
	if err != nil {
		return Platform{}, err
	}
	f := strings.Fields(string(res.Stdout))
	if res.ExitCode != 0 || len(f) != 2 {
		return Platform{}, valise.New("uname_failed", "无法识别服务器的系统与架构", "确认服务器上可以执行 uname").
			WithDetail(strings.TrimSpace(string(res.Stdout) + string(res.Stderr)))
	}
	p := Platform{OS: strings.ToLower(f[0]), Arch: f[1]}
	switch p.Arch {
	case "x86_64", "amd64":
		p.Arch = "amd64"
	case "aarch64", "arm64":
		p.Arch = "arm64"
	}
	return p, nil
}

// Upload verifies a unique staged executable before publishing it. Publication
// holds the protection transaction lock and rejects incompatible downgrades.
// Failed verification never replaces the installed porter.
func Upload(ctx context.Context, t *ssh.Transport, binary []byte, want Platform) (*Found, error) {
	sum := sha256.Sum256(binary)
	local := hex.EncodeToString(sum[:])
	id, err := identity.NewID()
	if err != nil {
		return nil, err
	}
	stage := tmpPath + "." + id

	write := fmt.Sprintf(`umask 077; mkdir -p "%s" && cat > "%s" && chmod 0755 "%s"`, InstallDir, stage, stage)
	res, err := t.Run(ctx, write, bytes.NewReader(binary))
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		discard(ctx, t, stage)
		return nil, valise.New("porter_upload_failed",
			fmt.Sprintf("无法把 porter 写入服务器 %s 的 ~/.local/bin/", t.Host()),
			"检查服务器上的磁盘空间与 ~/.local/bin/ 的权限",
		).WithDetail(strings.TrimSpace(string(res.Stderr)))
	}

	f, err := VersionAt(ctx, t, "~/.local/bin/valise-porter.tmp."+id, `"`+stage+`"`)
	if err != nil {
		discard(ctx, t, stage)
		return nil, valise.New("porter_wrong_binary",
			fmt.Sprintf("上传的 porter 无法在服务器 %s（%s）上运行", t.Host(), want),
			fmt.Sprintf("选择为 %s 构建的 porter", want),
		).WithDetail(err.Error())
	}
	if got := (Platform{f.Info.OS, f.Info.Arch}); got != want {
		discard(ctx, t, stage)
		return nil, valise.New("porter_wrong_binary",
			fmt.Sprintf("上传的 porter 是为 %s 构建的，而服务器是 %s", got, want),
			fmt.Sprintf("选择为 %s 构建的 porter", want))
	}
	if f.Info.SelfSHA256 != local {
		discard(ctx, t, stage)
		return nil, valise.New("porter_upload_corrupt",
			"上传到服务器的 porter 与本地文件不一致",
			"重新部署；如果反复出现，检查服务器磁盘").
			WithDetail("local " + local + "\nremote " + f.Info.SelfSHA256)
	}

	res, err = t.Run(ctx, "sh -s", strings.NewReader(publishScript(stage, local, f.Supports(ProtectionCapability))))
	if err != nil {
		// The connection dropped: whether mv ran is unknown, so report it
		// rather than guessing; the next probe shows the installed binary.
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, publishError(res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	return Probe(ctx, t)
}

// ProtectionCapability must equal core/protection.Capability. It is repeated
// here so the client module does not pull core/vault's dependencies into
// client/porter; TestUploadCapablePorterWithPlan checks the built porter declares it.
const ProtectionCapability = "protection-plan/1"

// Exit codes of publishScript.
const (
	exitNoFlock     = 20
	exitLockOpen    = 21
	exitLockBusy    = 22
	exitDowngrade   = 23
	exitHashChanged = 24
	exitNoHashTool  = 25
)

// publishScript runs on the server. It takes the same flock(2) lock as the
// porter's protection store (porter/internal/protection, store.lock), so a
// plan cannot be adopted between the capability check and the mv. Holding the
// lock it refuses a porter that does not declare the protection capability
// once any plan or plan transaction exists, re-hashes the staged file (another
// writer may have touched it since version --json) and only then renames it.
// It never edits plan records; on refusal the staged file is removed and the
// installed porter is untouched.
func publishScript(stage, sum string, supportsProtection bool) string {
	supported := "0"
	if supportsProtection {
		supported = "1"
	}
	return fmt.Sprintf(`set -u
umask 077
stage="%[1]s"
want="%[2]s"
supported="%[3]s"
cleanup() { [ -e "$stage" ] && rm -- "$stage"; }
lockdir="$HOME/.local/state/valise/protection-changes"
command -v flock >/dev/null 2>&1 || { cleanup; exit %[4]d; }
mkdir -p "$lockdir" && chmod 0700 "$lockdir" || { cleanup; exit %[5]d; }
exec 9>>"$lockdir/store.lock" || { cleanup; exit %[5]d; }
flock -n 9 || { cleanup; exit %[6]d; }
adopted=0
for f in "$HOME/.config/valise/protection/"*.json "$lockdir/"*.json; do
  if [ -e "$f" ]; then adopted=1; break; fi
done
if [ "$adopted" = 1 ] && [ "$supported" != 1 ]; then cleanup; exit %[7]d; fi
if command -v sha256sum >/dev/null 2>&1; then got=$(sha256sum "$stage" | cut -d' ' -f1)
elif command -v shasum >/dev/null 2>&1; then got=$(shasum -a 256 "$stage" | cut -d' ' -f1)
else cleanup; exit %[9]d; fi
[ "$got" = "$want" ] || { cleanup; exit %[8]d; }
mv "$stage" "%[10]s"
`, stage, sum, supported, exitNoFlock, exitLockOpen, exitLockBusy, exitDowngrade, exitHashChanged, exitNoHashTool, InstallPath)
}

func publishError(code int, detail string) error {
	switch code {
	case exitNoFlock:
		return valise.New("porter_replace_failed", "服务器上没有 flock，无法在保护计划锁内安全替换 porter", "安装 util-linux 提供的 flock 后重新部署；原 porter 未改动")
	case exitLockBusy:
		return valise.New("resource_busy", "服务器上正在更新保护设置，暂不替换 porter", "等保护设置更新完成后重新部署；原 porter 未改动")
	case exitDowngrade:
		return valise.New("porter_downgrade_refused", "这台服务器已采用版本化保护计划，所选 porter 不支持它", "选择声明 "+ProtectionCapability+" 能力的 porter 版本；原 porter 未改动")
	case exitHashChanged:
		return valise.New("porter_upload_corrupt", "暂存的 porter 在发布前发生了变化", "重新部署；原 porter 未改动")
	case exitNoHashTool:
		return valise.New("porter_replace_failed", "服务器上没有 sha256sum 或 shasum，无法在发布前复核 porter", "安装其中之一后重新部署；原 porter 未改动")
	}
	return valise.New("porter_replace_failed", "无法替换服务器上的 porter", "检查 ~/.local/bin/ 与 ~/.local/state/valise/ 的权限；原 porter 未改动").WithDetail(detail)
}

// discard removes a staged upload that failed verification; best effort.
func discard(ctx context.Context, t *ssh.Transport, stage string) {
	t.Run(ctx, fmt.Sprintf(`[ -e "%[1]s" ] && rm -- "%[1]s"`, stage), nil)
}
