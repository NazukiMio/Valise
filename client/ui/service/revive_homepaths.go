package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"path"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"valise/core/profile"
	"valise/core/revive"
	"valise/core/vault"
)

const (
	homeScanMaxBytes = 512 << 10
	homeScanMaxHits  = 200
	homeBakSuffix    = ".valise-home-bak"
)

// homeScanNames is the harness-agnostic allowlist of generic config basenames
// scanned when deciding whether an unclassified file should be checked for the
// source account's $HOME. Harness-specific config filenames come from the
// instance's profile (Detect.MarkersAny and Paths config rules).
var homeScanNames = map[string]bool{
	"config.yaml": true, "config.yml": true,
	".mcp.json": true, "mcp.json": true, "settings.json": true,
}

func hasConfigExt(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".json", ".yaml", ".yml", ".toml":
		return true
	default:
		return false
	}
}

func isCredentialBasename(base string) bool {
	low := strings.ToLower(base)
	return strings.HasPrefix(low, "auth") ||
		strings.HasPrefix(low, ".env") ||
		strings.Contains(low, "secret") ||
		strings.Contains(low, "token") ||
		strings.Contains(low, "credential") ||
		strings.Contains(low, "oauth")
}

func hasExcludedSegment(rel string) bool {
	parts := strings.Split(strings.Trim(rel, "/"), "/")
	if len(parts) <= 1 {
		return false
	}
	for _, seg := range parts[:len(parts)-1] {
		switch seg {
		case "workspace", "sessions", "memory", "cache", "credentials", "auth", ".git", "node_modules":
			return true
		}
	}
	return false
}

func profileConfigNames(prof *profile.Profile) map[string]bool {
	names := map[string]bool{}
	if prof == nil {
		return names
	}
	if prof.Detect != nil {
		for _, m := range prof.Detect.MarkersAny {
			base := path.Base(m)
			if base != "" && base != "." && hasConfigExt(base) && !isCredentialBasename(base) {
				names[base] = true
			}
		}
	}
	for _, r := range prof.Paths {
		if r.Category != profile.CategoryConfig {
			continue
		}
		if strings.ContainsAny(r.Glob, "*?[") {
			continue
		}
		base := path.Base(r.Glob)
		if base != "" && base != "." && hasConfigExt(base) && !isCredentialBasename(base) {
			names[base] = true
		}
	}
	return names
}

func shouldScanHomeFile(f vault.File, profNames map[string]bool) bool {
	if f.Size <= 0 || f.Size > homeScanMaxBytes || !hasConfigExt(f.Path) || hasExcludedSegment(f.Path) {
		return false
	}
	base := path.Base(f.Path)
	if isCredentialBasename(base) {
		return false
	}
	if f.Category == profile.CategoryConfig {
		return true
	}
	if f.Root != profile.StateRoot {
		return false
	}
	named := homeScanNames[base] || profNames[base]
	if f.Category == profile.CategorySecrets {
		return named && !strings.Contains(strings.TrimPrefix(f.Path, "/"), "/")
	}
	if f.Category == "" || f.Category == "unclassified" {
		return named
	}
	return false
}

// ExternalSymlinkNote is a snapshot symlink pointing outside every snapshot
// root; it is carried as is (or remapped when it points under the old $HOME).
type ExternalSymlinkNote struct {
	Root            string `json:"root"`
	Path            string `json:"path"`
	Target          string `json:"target"`
	Category        string `json:"category,omitempty"`
	UnderSourceHome bool   `json:"under_source_home,omitempty"`
}

// ConfigHomeHit is one reference to the source $HOME inside a config file.
// Only the path reference is kept, never the surrounding line.
type ConfigHomeHit struct {
	Root           string `json:"root"`
	Path           string `json:"path"`
	Line           int    `json:"line"`
	OldPath        string `json:"old_path"`
	NewPath        string `json:"new_path"`
	IsValisePorter bool   `json:"is_valise_porter,omitempty"`
}

func isPathByte(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || strings.IndexByte("._/@+-", b) >= 0
}

// findHomeRefs returns the [start,end) offsets of every reference to home
// (home itself, or home followed by "/..." path characters).
func findHomeRefs(data []byte, home string) [][2]int {
	var out [][2]int
	if home == "" || home == "/" {
		return nil
	}
	h := []byte(home)
	for off := 0; ; {
		i := bytes.Index(data[off:], h)
		if i < 0 {
			return out
		}
		start := off + i
		end := start + len(h)
		off = end
		if end < len(data) && data[end] != '/' && isPathByte(data[end]) {
			continue // a longer name such as /home/alice2
		}
		if start > 0 && isPathByte(data[start-1]) {
			continue // not at a path start
		}
		for end < len(data) && isPathByte(data[end]) {
			end++
		}
		out = append(out, [2]int{start, end})
		off = end
	}
}

// scanConfigHomeHits records path references only (never file lines).
func scanConfigHomeHits(root, rel string, data []byte, oldHome, newHome string) []ConfigHomeHit {
	var hits []ConfigHomeHit
	for _, m := range findHomeRefs(data, oldHome) {
		old := string(data[m[0]:m[1]])
		hits = append(hits, ConfigHomeHit{Root: root, Path: rel, Line: 1 + bytes.Count(data[:m[0]], []byte{'\n'}),
			OldPath: old, NewPath: newHome + strings.TrimPrefix(old, oldHome), IsValisePorter: strings.Contains(old, "valise-porter")})
	}
	return hits
}

func rewriteHomeRefs(data []byte, oldHome, newHome string) ([]byte, int) {
	refs := findHomeRefs(data, oldHome)
	if len(refs) == 0 {
		return data, 0
	}
	var out bytes.Buffer
	last := 0
	for _, m := range refs {
		out.Write(data[last:m[0]])
		out.WriteString(newHome + strings.TrimPrefix(string(data[m[0]:m[1]]), oldHome))
		last = m[1]
	}
	out.Write(data[last:])
	return out.Bytes(), len(refs)
}

func underHome(target, home string) bool {
	if home == "" || !path.IsAbs(home) || !path.IsAbs(target) {
		return false
	}
	cleanTarget := path.Clean(target)
	cleanHome := path.Clean(home)
	if cleanHome == "/" {
		return false
	}
	return cleanTarget == cleanHome || strings.HasPrefix(cleanTarget, cleanHome+"/")
}

// remapHomeTarget normalizes target and rewrites it when it is oldHome or below it.
func remapHomeTarget(target, oldHome, newHome string) (string, bool) {
	if !underHome(target, oldHome) || newHome == "" || !path.IsAbs(newHome) {
		return target, false
	}
	cleanTarget := path.Clean(target)
	cleanOld := path.Clean(oldHome)
	cleanNew := path.Clean(newHome)
	rel := strings.TrimPrefix(cleanTarget, cleanOld)
	if rel == "" {
		return cleanNew, true
	}
	return path.Clean(cleanNew + rel), true
}

// planHomePaths fills the home-change facts of rp: external symlinks and
// allowlisted config references to the source $HOME. Plaintext is read in memory only.
func (s *Service) planHomePaths(rp *RevivePlan, head *vault.Snapshot, prof *profile.Profile) {
	rp.SourceHome, rp.TargetHome = rp.sourceHome, rp.targetFacts.Home
	rp.HomeChanged = rp.SourceHome != "" && rp.TargetHome != "" && rp.SourceHome != rp.TargetHome
	rp.ExternalSymlinks = externalSymlinks(rp, head)
	if !rp.HomeChanged {
		return
	}
	profNames := profileConfigNames(prof)
	for _, f := range rp.files {
		if len(rp.ConfigHomeHits) >= homeScanMaxHits {
			break
		}
		if !shouldScanHomeFile(f, profNames) {
			continue
		}
		var buf bytes.Buffer
		if rp.writeRestoreFile(f, &buf) != nil || !validUTF8(buf.Bytes()) {
			continue
		}
		rp.ConfigHomeHits = append(rp.ConfigHomeHits, scanConfigHomeHits(f.Root, f.Path, buf.Bytes(), rp.SourceHome, rp.TargetHome)...)
	}
	if len(rp.ConfigHomeHits) > homeScanMaxHits {
		rp.ConfigHomeHits = rp.ConfigHomeHits[:homeScanMaxHits]
	}
}

func validUTF8(b []byte) bool { return utf8.Valid(b) && bytes.IndexByte(b, 0) < 0 }

func externalSymlinks(rp *RevivePlan, head *vault.Snapshot) []ExternalSymlinkNote {
	roots := map[string]string{}
	for id, p := range head.Roots {
		roots[id] = p
	}
	cats := map[string]string{}
	for _, lr := range head.LocalRoots {
		if _, dup := roots[lr.ID]; !dup && lr.Path != "" {
			roots[lr.ID] = lr.Path
		}
		cats[lr.ID] = lr.Category
	}
	inside := func(p string) bool {
		for _, r := range roots {
			if r != "" && (p == r || strings.HasPrefix(p, strings.TrimSuffix(r, "/")+"/")) {
				return true
			}
		}
		return false
	}
	var out []ExternalSymlinkNote
	for _, l := range rp.links {
		abs := l.Target
		if !path.IsAbs(abs) {
			base, ok := roots[l.Root]
			if !ok {
				continue
			}
			abs = path.Join(base, path.Dir(l.Path), l.Target)
		}
		abs = path.Clean(abs)
		if inside(abs) {
			continue
		}
		out = append(out, ExternalSymlinkNote{Root: l.Root, Path: l.Path, Target: l.Target, Category: cats[l.Root],
			UnderSourceHome: underHome(abs, rp.SourceHome)})
	}
	return out
}

// applyStageChoices turns the human's home-path choices into what is staged:
// remapped symlinks and rewritten config files (with a .valise-home-bak of
// the original). The in-memory rewrite is kept only until the stage is
// verified and swapped.
func (s *Service) applyStageChoices(rp *RevivePlan, c ReviveConfirm) error {
	rp.rewritten, rp.RemappedSymlinks, rp.RewrittenConfigPaths = nil, 0, 0
	rp.carryMCP = c.CarryMCP
	rp.stageLinks = rp.links
	if !rp.HomeChanged {
		return nil
	}
	if c.RemapHomeSymlinks {
		rp.stageLinks = slices.Clone(rp.links)
		for i, l := range rp.stageLinks {
			if t, ok := remapHomeTarget(l.Target, rp.SourceHome, rp.TargetHome); ok {
				rp.stageLinks[i].Target = t
				rp.RemappedSymlinks++
			}
		}
	}
	if !c.RewriteConfigHomePaths || len(rp.ConfigHomeHits) == 0 {
		return nil
	}
	have := map[string]bool{}
	for _, f := range rp.files {
		have[f.Root+":"+f.Path] = true
	}
	done := map[string]bool{}
	rp.rewritten = map[string]*rewrittenFile{}
	for _, f := range rp.files {
		key := f.Root + ":" + f.Path
		if done[key] || have[key+homeBakSuffix] || !slices.ContainsFunc(rp.ConfigHomeHits, func(h ConfigHomeHit) bool { return h.Root == f.Root && h.Path == f.Path }) {
			continue
		}
		done[key] = true
		var buf bytes.Buffer
		if err := rp.writeRestoreFile(f, &buf); err != nil {
			return err
		}
		orig := buf.Bytes()
		data, n := rewriteHomeRefs(orig, rp.SourceHome, rp.TargetHome)
		if n == 0 {
			continue
		}
		o, w := sha256.Sum256(orig), sha256.Sum256(data)
		rp.rewritten[key] = &rewrittenFile{data: data, orig: orig, newSHA: hex.EncodeToString(w[:]), origSHA: hex.EncodeToString(o[:])}
		rp.RewrittenConfigPaths += n
	}
	return nil
}

type rewrittenFile struct {
	data, orig      []byte
	newSHA, origSHA string
}

// RemainingConfigHomeHits counts the references still pointing at the old home.
func (rp *RevivePlan) remainingConfigHomeHits() int {
	n := len(rp.ConfigHomeHits)
	for _, h := range rp.ConfigHomeHits {
		if rp.rewritten[h.Root+":"+h.Path] != nil {
			n--
		}
	}
	return n
}

// stageManifest is the manifest the target's stage must match, including
// rewritten config files and their .valise-home-bak originals.
func stageManifest(rp *RevivePlan) []revive.ManifestEntry {
	var man []revive.ManifestEntry
	for _, f := range rp.files {
		e := revive.ManifestEntry{Path: tarName(rp.Plan, f.Root, f.Path), Size: f.Size, SHA256: f.SHA256, Category: f.Category, SQLite: rp.sqlite[f.Root+":"+f.Path]}
		if rw := rp.rewritten[f.Root+":"+f.Path]; rw != nil {
			man = append(man, revive.ManifestEntry{Path: e.Path + homeBakSuffix, Size: int64(len(rw.orig)), SHA256: rw.origSHA, Category: f.Category})
			e.Size, e.SHA256 = int64(len(rw.data)), rw.newSHA
		}
		man = append(man, e)
	}
	return man
}

// stageDirs are the empty directories to stage.
func (rp *RevivePlan) stageDirList() []vault.Dir {
	d := slices.Clone(rp.dirs)
	sort.Slice(d, func(i, j int) bool { return d[i].Root+"/"+d[i].Path < d[j].Root+"/"+d[j].Path })
	return d
}
