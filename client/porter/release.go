package porter

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"

	"valise/client/minisign"
	"valise/core/valise"
)

// Release asset names, see docs/ARCHITECTURE.md section 2.
const (
	SumsAsset = "SHA256SUMS"
	SigAsset  = "SHA256SUMS.minisig"
)

// AssetName is the porter binary asset for a platform.
func AssetName(p Platform) string { return "valise-porter_" + p.OS + "_" + p.Arch }

// DefaultReleaseBase is where release assets are downloaded from.
const DefaultReleaseBase = "https://github.com/NazukiMio/Valise/releases/download"

// ReleasePublicKey is the minisign key releases are signed with, set at
// client build time (-ldflags "-X valise/client/porter.ReleasePublicKey=…").
// Until a key exists, Release deploys are refused.
var ReleasePublicKey = ""

// Size limits for downloads.
const (
	maxBinaryBytes = 64 << 20
	maxSmallBytes  = 1 << 20
)

// Release fetches and verifies release assets.
type Release struct {
	HTTP      *http.Client
	BaseURL   string
	PublicKey string
}

// Fetch downloads the porter for p at tag. It verifies the minisign
// signature of SHA256SUMS, then the binary's hash against it.
func (r Release) Fetch(ctx context.Context, tag string, p Platform) ([]byte, error) {
	if r.PublicKey == "" {
		return nil, valise.New("release_key_missing",
			"这个客户端没有配置 Release 签名公钥，无法校验下载的 porter",
			"改用 --local <文件> 上传你自己构建的 porter")
	}
	pk, err := minisign.ParsePublicKey(r.PublicKey)
	if err != nil {
		return nil, valise.New("release_key_invalid", "客户端内置的 Release 签名公钥无效", "改用 --local 上传本地构建的 porter").WithDetail(err.Error())
	}
	if !p.ReleaseSupported() {
		return nil, valise.New("release_unsupported_platform",
			fmt.Sprintf("Release 不提供 %s 的 porter", p),
			"在本机为该平台构建 porter，再用 --local 上传")
	}
	if tag == "" || strings.ContainsAny(tag, "/?# ") {
		return nil, valise.New("release_bad_tag", "Release 版本号无效", "使用形如 v0.1.0 的版本号")
	}
	sums, err := r.get(ctx, tag, SumsAsset, maxSmallBytes)
	if err != nil {
		return nil, err
	}
	sig, err := r.get(ctx, tag, SigAsset, maxSmallBytes)
	if err != nil {
		return nil, err
	}
	if _, err := minisign.Verify(pk, sums, sig); err != nil {
		return nil, valise.New("release_signature_invalid",
			"Release 校验和文件的签名无效，已停止部署",
			"不要使用这个 Release；改用 --local 上传本地构建的 porter，并向项目报告").WithDetail(err.Error())
	}
	want, ok := lookupSum(sums, AssetName(p))
	if !ok {
		return nil, valise.New("release_asset_missing",
			fmt.Sprintf("Release %s 的校验和文件中没有 %s", tag, AssetName(p)),
			"选择其他版本，或改用 --local 上传")
	}
	bin, err := r.get(ctx, tag, AssetName(p), maxBinaryBytes)
	if err != nil {
		return nil, err
	}
	got := sha256.Sum256(bin)
	if hex.EncodeToString(got[:]) != want {
		return nil, valise.New("release_hash_mismatch",
			"下载的 porter 与签名过的校验和不一致，已停止部署",
			"稍后重试；如果反复出现，改用 --local 上传并向项目报告").
			WithDetail("want " + want + "\ngot  " + hex.EncodeToString(got[:]))
	}
	return bin, nil
}

func (r Release) get(ctx context.Context, tag, asset string, limit int64) ([]byte, error) {
	base := r.BaseURL
	if base == "" {
		base = DefaultReleaseBase
	}
	url := strings.TrimRight(base, "/") + "/" + tag + "/" + asset
	fail := func(detail string) error {
		return valise.New("release_download_failed",
			fmt.Sprintf("无法下载 Release 资产 %s", asset),
			"检查网络与版本号，或改用 --local 上传本地构建的 porter").WithDetail(url + "\n" + detail)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fail(err.Error())
	}
	hc := r.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fail(err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fail(resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fail(err.Error())
	}
	if int64(len(b)) > limit {
		return nil, fail("文件过大")
	}
	return b, nil
}

// lookupSum finds name in sha256sum output ("<hex>  <name>" or "<hex> *<name>").
func lookupSum(sums []byte, name string) (string, bool) {
	sc := bufio.NewScanner(bytes.NewReader(sums))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name && len(f[0]) == 64 {
			if _, err := hex.DecodeString(f[0]); err == nil {
				return strings.ToLower(f[0]), true
			}
		}
	}
	return "", false
}
