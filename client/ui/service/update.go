package service

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"valise/core/valise"
)

// PorterUpdate is the result of checking a server's porter against the
// newest release (docs/ARCHITECTURE.md section 2, 检查更新). Updating is a
// separate, confirmed deploy.
type PorterUpdate struct {
	Host      string `json:"host"`
	Current   string `json:"current"`
	Latest    string `json:"latest,omitempty"`
	Available bool   `json:"available"`
	// NoReleaseKey: a development build cannot verify releases, so it
	// offers no one-click deploy or update.
	NoReleaseKey bool `json:"no_release_key,omitempty"`
}

// PorterReleaseTagPrefix marks porter (and client) releases; harness packs
// use profiles-*.
const PorterReleaseTagPrefix = "v"

// CheckPorterUpdate compares host's porter with the newest release.
func (s *Service) CheckPorterUpdate(ctx context.Context, host string) (*PorterUpdate, error) {
	doc, err := s.Doctor(ctx, host)
	if err != nil {
		return nil, err
	}
	u := &PorterUpdate{Host: host, Current: doc.PorterVersion}
	if s.Release.PublicKey == "" {
		u.NoReleaseKey = true
		return u, nil
	}
	h := s.Release.HTTP
	if h == nil {
		h = http.DefaultClient
	}
	b, err := s.download(ctx, h, ReleasesAPI, 2<<20)
	if err != nil {
		return nil, err
	}
	var rels []struct {
		Tag        string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.Unmarshal(b, &rels); err != nil {
		return nil, valise.New("release_list", "无法读取 Release 列表", "稍后重试").WithDetail(err.Error())
	}
	for _, r := range rels {
		if r.Draft || r.Prerelease || !strings.HasPrefix(r.Tag, PorterReleaseTagPrefix) || parseVersion(r.Tag) == nil {
			continue
		}
		if u.Latest == "" || versionLess(u.Latest, r.Tag) {
			u.Latest = r.Tag
		}
	}
	u.Available = u.Latest != "" && (u.Current == "" || versionLess(u.Current, u.Latest))
	return u, nil
}

// parseVersion reads "v1.2.3" or "1.2.3[-suffix]" as three numbers.
func parseVersion(v string) []int {
	v = strings.TrimPrefix(v, "v")
	core, _, _ := strings.Cut(v, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return nil
	}
	out := make([]int, 3)
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil
		}
		out[i] = n
	}
	return out
}

// versionLess orders versions numerically; a pre-release suffix ("-dev")
// sorts before the same release.
func versionLess(a, b string) bool {
	pa, pb := parseVersion(a), parseVersion(b)
	if pa == nil || pb == nil {
		return pa == nil && pb != nil
	}
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] < pb[i]
		}
	}
	return strings.Contains(a, "-") && !strings.Contains(b, "-")
}
