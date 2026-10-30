package porter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"valise/client/ssh"
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

// Upload installs binary as ~/.local/bin/valise-porter: cat to a .tmp file,
// chmod 0755, run `.tmp version --json` and check it reports the expected
// platform and the same SHA-256, then mv over the old porter. On any failure
// the existing porter is left untouched.
func Upload(ctx context.Context, t *ssh.Transport, binary []byte, want Platform) (*Found, error) {
	sum := sha256.Sum256(binary)
	local := hex.EncodeToString(sum[:])

	write := fmt.Sprintf(`mkdir -p "%s" && cat > "%s" && chmod 0755 "%s"`, InstallDir, tmpPath, tmpPath)
	res, err := t.Run(ctx, write, bytes.NewReader(binary))
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, valise.New("porter_upload_failed",
			fmt.Sprintf("无法把 porter 写入服务器 %s 的 ~/.local/bin/", t.Host()),
			"检查服务器上的磁盘空间与 ~/.local/bin/ 的权限",
		).WithDetail(strings.TrimSpace(string(res.Stderr)))
	}

	f, err := versionAt(ctx, t, "~/.local/bin/valise-porter.tmp", `"`+tmpPath+`"`)
	if err != nil {
		return nil, valise.New("porter_wrong_binary",
			fmt.Sprintf("上传的 porter 无法在服务器 %s（%s）上运行", t.Host(), want),
			fmt.Sprintf("选择为 %s 构建的 porter", want),
		).WithDetail(err.Error())
	}
	if got := (Platform{f.Info.OS, f.Info.Arch}); got != want {
		return nil, valise.New("porter_wrong_binary",
			fmt.Sprintf("上传的 porter 是为 %s 构建的，而服务器是 %s", got, want),
			fmt.Sprintf("选择为 %s 构建的 porter", want))
	}
	if f.Info.SelfSHA256 != local {
		return nil, valise.New("porter_upload_corrupt",
			"上传到服务器的 porter 与本地文件不一致",
			"重新部署；如果反复出现，检查服务器磁盘").
			WithDetail("local " + local + "\nremote " + f.Info.SelfSHA256)
	}

	res, err = t.Run(ctx, fmt.Sprintf(`mv -f "%s" "%s"`, tmpPath, InstallPath), nil)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, valise.New("porter_replace_failed", "无法替换服务器上的 porter", "检查 ~/.local/bin/ 的权限").
			WithDetail(strings.TrimSpace(string(res.Stderr)))
	}
	return Probe(ctx, t)
}
