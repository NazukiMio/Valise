package kit

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"valise/core/agentkit"
	"valise/core/enroll"
	"valise/core/profile"
	"valise/core/valise"
	porterenroll "valise/porter/internal/enroll"
	porteridentity "valise/porter/internal/identity"
)

// ProposalItem is one entry of legacy valise_propose_enrollment.
type ProposalItem struct {
	Root     string `json:"root"`
	Path     string `json:"path"`
	Category string `json:"category"`
	Reason   string `json:"reason"`
}

// DossierProposalItem is one entry of valise_propose_dossier.
type DossierProposalItem struct {
	Kind              string   `json:"kind"` // classify | sqlite | external_ref | tool_dep | web_entry
	ID                string   `json:"id,omitempty"`
	DisplayName       string   `json:"display_name,omitempty"`
	Port              int      `json:"port,omitempty"`
	Hint              string   `json:"hint,omitempty"`
	Root              string   `json:"root,omitempty"`
	Glob              string   `json:"glob,omitempty"`
	Path              string   `json:"path,omitempty"` // classify relative path alias, external_ref abs path, or web_entry URL path
	Category          string   `json:"category,omitempty"`
	SuggestedCategory string   `json:"suggested_category,omitempty"`
	SecretTables      []string `json:"secret_tables,omitempty"`
	RefKind           string   `json:"ref_kind,omitempty"` // external_dir | symlink_target
	Name              string   `json:"name,omitempty"`
	Purpose           string   `json:"purpose,omitempty"`
	Reason            string   `json:"reason"`
}

var allowedClassifyCategories = map[string]bool{
	profile.CategoryIdentity:  true,
	profile.CategoryMemory:    true,
	profile.CategoryWorkspace: true,
	profile.CategoryState:     true,
	profile.CategorySkills:    true,
	profile.CategoryConfig:    true,
	profile.CategorySecrets:   true,
	profile.CategoryCache:     true,
	profile.CategoryIgnore:    true,
}

func proposalsPath(home, instance string) string {
	return filepath.Join(porterenroll.InstancesDir(home), instance+".proposals.json")
}

// LoadProposals returns the pending proposals of an instance.
func LoadProposals(home, instance string) ([]agentkit.Proposal, error) {
	var ps []agentkit.Proposal
	b, err := os.ReadFile(proposalsPath(home, instance))
	if errors.Is(err, os.ErrNotExist) {
		return []agentkit.Proposal{}, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &ps); err != nil {
		return nil, err
	}
	return ps, nil
}

func saveProposals(home, instance string, ps []agentkit.Proposal) error {
	b, _ := json.MarshalIndent(ps, "", "  ")
	return writeAtomic(proposalsPath(home, instance), b)
}

// Propose validates legacy classify items and stores them as pending.
func Propose(home string, rec *enroll.Record, p *profile.Profile, items []ProposalItem, now time.Time) ([]string, error) {
	wrapped := make([]DossierProposalItem, len(items))
	for i, it := range items {
		wrapped[i] = DossierProposalItem{
			Kind:     agentkit.ProposalKindClassify,
			Root:     it.Root,
			Path:     it.Path,
			Category: it.Category,
			Reason:   it.Reason,
		}
	}
	return ProposeDossier(home, rec, p, wrapped, now)
}

// ProposeDossier validates classify, sqlite, external_ref, and tool_dep items
// and stores valid ones in the instance's pending proposal queue.
func ProposeDossier(home string, rec *enroll.Record, p *profile.Profile, items []DossierProposalItem, now time.Time) ([]string, error) {
	ps, err := LoadProposals(home, rec.InstanceID)
	if err != nil {
		return nil, err
	}
	cls := profile.NewClassifier(p, rec.LocalOverrides).
		WithSQLite(rec.LocalSQLite).
		WithDefaultRoots(localRootDefaults(rec.LocalRoots))
	var out []string
	for _, it := range items {
		reason := trimReason(it.Reason)
		it.Reason = reason
		var np *agentkit.Proposal
		var why string
		switch it.Kind {
		case "", agentkit.ProposalKindClassify:
			np, why = buildClassifyProposal(rec, p, cls, it, now)
		case agentkit.ProposalKindSQLite:
			np, why = buildSQLiteProposal(rec, p, it, now)
		case agentkit.ProposalKindExternalRef:
			np, why = buildExternalRefProposal(home, rec, it, now)
		case agentkit.ProposalKindToolDep:
			np, why = buildToolDepProposal(it, now)
		case agentkit.ProposalKindWebEntry:
			np, why = buildWebEntryProposal(p, it, now)
		default:
			why = "未知提议类型 " + it.Kind
		}
		if why != "" {
			out = append(out, itemLabel(it)+" — 拒绝："+why)
			continue
		}
		upsertProposal(&ps, *np)
		out = append(out, formatProposalLine(*np))
	}
	return out, saveProposals(home, rec.InstanceID, ps)
}

func localRootDefaults(lrs []enroll.LocalRoot) map[string]string {
	if len(lrs) == 0 {
		return nil
	}
	out := make(map[string]string, len(lrs))
	for _, lr := range lrs {
		if lr.ID != "" && lr.Category != "" {
			out[lr.ID] = lr.Category
		}
	}
	return out
}

func localRootIDs(lrs []enroll.LocalRoot) []string {
	if len(lrs) == 0 {
		return nil
	}
	out := make([]string, 0, len(lrs))
	for _, lr := range lrs {
		if lr.ID != "" {
			out = append(out, lr.ID)
		}
	}
	return out
}

func trimReason(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 500 {
		return s[:500]
	}
	return s
}

func itemLabel(it DossierProposalItem) string {
	switch it.Kind {
	case agentkit.ProposalKindExternalRef:
		return "external:" + it.Path
	case agentkit.ProposalKindToolDep:
		return "tool:" + strings.TrimSpace(it.Name)
	case agentkit.ProposalKindWebEntry:
		return "web_entry:" + strings.TrimSpace(it.ID)
	case agentkit.ProposalKindSQLite:
		pat := it.Glob
		if pat == "" {
			pat = it.Path
		}
		return "sqlite:" + it.Root + ":" + pat
	default:
		pat := it.Glob
		if pat == "" {
			pat = it.Path
		}
		return it.Root + ":" + pat
	}
}

func formatProposalLine(np agentkit.Proposal) string {
	const note = "已保存，等待用户在客户端确认"
	switch np.EffectiveKind() {
	case agentkit.ProposalKindSQLite:
		return "sqlite:" + np.SQLite.Root + ":" + np.SQLite.Glob + " — " + note + "（" + np.ID + "）"
	case agentkit.ProposalKindExternalRef:
		return "external:" + np.ExternalRef.Path + " (" + np.ExternalRef.RefKind + ") — " + note + "（" + np.ID + "）"
	case agentkit.ProposalKindToolDep:
		return "tool:" + np.ToolDep.Name + " — " + note + "（" + np.ID + "）"
	case agentkit.ProposalKindWebEntry:
		return "web_entry:" + np.WebEntry.ID + " (:" + strconv.Itoa(np.WebEntry.Port) + np.WebEntry.Path + ") — " + note + "（" + np.ID + "）"
	default:
		line := np.Rule.Root + ":" + np.Rule.Glob + " → " + np.Rule.Category + " — " + note
		if np.Demotion {
			line += "（降级：这些文件将不再进入常规备份，需要用户单独确认）"
		}
		return line + "（" + np.ID + "）"
	}
}

func upsertProposal(ps *[]agentkit.Proposal, np agentkit.Proposal) {
	idx := slices.IndexFunc(*ps, func(x agentkit.Proposal) bool {
		if x.EffectiveKind() != np.EffectiveKind() {
			return false
		}
		switch np.EffectiveKind() {
		case agentkit.ProposalKindSQLite:
			return x.SQLite != nil && np.SQLite != nil && x.SQLite.Root == np.SQLite.Root && x.SQLite.Glob == np.SQLite.Glob
		case agentkit.ProposalKindExternalRef:
			return x.ExternalRef != nil && np.ExternalRef != nil && x.ExternalRef.Path == np.ExternalRef.Path
		case agentkit.ProposalKindToolDep:
			return x.ToolDep != nil && np.ToolDep != nil && x.ToolDep.Name == np.ToolDep.Name
		case agentkit.ProposalKindWebEntry:
			return x.WebEntry != nil && np.WebEntry != nil && x.WebEntry.ID == np.WebEntry.ID
		default:
			return x.Rule.Root == np.Rule.Root && x.Rule.Glob == np.Rule.Glob
		}
	})
	if idx >= 0 {
		np.ID = (*ps)[idx].ID
		(*ps)[idx] = np
		return
	}
	*ps = append(*ps, np)
}

func resolveGlobInRoot(rec *enroll.Record, rootID, raw string, allowDir bool) (glob string, files []string, why string) {
	root, ok := rec.Roots[rootID]
	if !ok || root == "" {
		return "", nil, "实例没有 root " + rootID
	}
	raw = strings.TrimSpace(raw)
	if raw == "" || filepath.IsAbs(raw) || strings.HasPrefix(raw, "/") {
		return "", nil, "路径必须是 root 内的相对路径"
	}
	if strings.ContainsAny(raw, "*?[") {
		clean := path.Clean(filepath.ToSlash(raw))
		if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
			return "", nil, "路径必须是 root 内的相对路径"
		}
		if err := profile.ValidateGlob(clean); err != nil {
			return "", nil, err.Error()
		}
		realRoot, _ := filepath.EvalSymlinks(root)
		var walkErr string
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || walkErr != "" {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			rel = filepath.ToSlash(rel)
			if rel == "." || !profile.MatchGlob(clean, rel) {
				return nil
			}
			if !pathInsideRoot(p, realRoot) {
				walkErr = "路径经符号链接指向 root 之外"
				return fs.SkipAll
			}
			if !d.IsDir() {
				files = append(files, rel)
			}
			return nil
		})
		if walkErr != "" {
			return "", nil, walkErr
		}
		if len(files) == 0 {
			return "", nil, "路径不存在"
		}
		return clean, files, ""
	}
	rel := filepath.ToSlash(filepath.Clean(raw))
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || strings.Contains(rel, "/../") {
		return "", nil, "路径必须是 root 内的相对路径"
	}
	full := filepath.Join(root, filepath.FromSlash(rel))
	st, err := os.Lstat(full)
	if err != nil {
		return "", nil, "路径不存在"
	}
	realRoot, _ := filepath.EvalSymlinks(root)
	if !pathInsideRoot(full, realRoot) {
		return "", nil, "路径经符号链接指向 root 之外"
	}
	if st.IsDir() {
		if !allowDir {
			return "", nil, "sqlite 路径需指向数据库文件或文件模式"
		}
		glob = rel + "/**"
		_ = filepath.WalkDir(full, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				r, _ := filepath.Rel(root, p)
				files = append(files, filepath.ToSlash(r))
			}
			return nil
		})
		return glob, files, ""
	}
	return rel, []string{rel}, ""
}

func pathInsideRoot(full, realRoot string) bool {
	real, err := filepath.EvalSymlinks(full)
	if err != nil {
		return false
	}
	if realRoot == "" {
		return false
	}
	return real == realRoot || strings.HasPrefix(real, realRoot+string(filepath.Separator))
}

func buildClassifyProposal(rec *enroll.Record, p *profile.Profile, cls *profile.Classifier, it DossierProposalItem, now time.Time) (*agentkit.Proposal, string) {
	if !allowedClassifyCategories[it.Category] {
		return nil, "类别只允许 identity、memory、workspace、state、skills、config、secrets、cache 或 ignore"
	}
	demotion := it.Category == profile.CategoryCache || it.Category == profile.CategoryIgnore
	if demotion && it.Reason == "" {
		return nil, "降级为 cache 或 ignore 必须说明为何可重建"
	}
	raw := it.Glob
	if strings.TrimSpace(raw) == "" {
		raw = it.Path
	}
	glob, files, why := resolveGlobInRoot(rec, it.Root, raw, true)
	if why != "" {
		return nil, why
	}
	rule := profile.Override{Root: it.Root, Glob: glob, Category: it.Category}
	if err := rule.ValidateWithRoots(p, localRootIDs(rec.LocalRoots)); err != nil {
		return nil, err.Error()
	}
	unclassified := 0
	if rule.Category != profile.CategorySecrets {
		for _, r := range files {
			switch cls.Classify(it.Root, r).Category {
			case profile.CategorySecrets:
				return nil, r + " 已按凭据规则保护，不能改为其他类别"
			case "":
				if !demotion {
					unclassified++
				}
			}
		}
	}
	return &agentkit.Proposal{
		ID:           newID("p_"),
		Kind:         agentkit.ProposalKindClassify,
		Rule:         rule,
		Reason:       it.Reason,
		Demotion:     demotion,
		Unclassified: unclassified,
		Created:      now.UTC(),
	}, ""
}

func buildSQLiteProposal(rec *enroll.Record, p *profile.Profile, it DossierProposalItem, now time.Time) (*agentkit.Proposal, string) {
	raw := it.Glob
	if strings.TrimSpace(raw) == "" {
		raw = it.Path
	}
	glob, files, why := resolveGlobInRoot(rec, it.Root, raw, false)
	if why != "" {
		return nil, why
	}
	tables, err := cleanSecretTables(it.SecretTables)
	if err != nil {
		return nil, err.Error()
	}
	ref := profile.SQLiteRef{Root: it.Root, Glob: glob, SecretTables: tables}
	if err := checkSQLiteNotWeakened(p, ref, files); err != nil {
		return nil, err.Error()
	}
	return &agentkit.Proposal{
		ID:      newID("p_"),
		Kind:    agentkit.ProposalKindSQLite,
		SQLite:  &ref,
		Reason:  it.Reason,
		Created: now.UTC(),
	}, ""
}

func cleanSecretTables(in []string) ([]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	seen := map[string]bool{}
	var out []string
	for _, t := range in {
		t = strings.TrimSpace(t)
		if t == "" {
			return nil, errors.New("secret_tables 不能包含空表名")
		}
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out, nil
}

func checkSQLiteNotWeakened(p *profile.Profile, ref profile.SQLiteRef, matchedFiles []string) error {
	if p == nil || p.Consistency == nil {
		return nil
	}
	have := map[string]bool{}
	for _, tbl := range ref.SecretTables {
		have[tbl] = true
	}
	for _, ps := range p.Consistency.SQLite {
		if ps.Root != ref.Root || len(ps.SecretTables) == 0 {
			continue
		}
		overlaps := ps.Glob == ref.Glob
		if !overlaps {
			for _, f := range matchedFiles {
				if profile.MatchGlob(ps.Glob, f) {
					overlaps = true
					break
				}
			}
		}
		if !overlaps {
			continue
		}
		for _, req := range ps.SecretTables {
			if !have[req] {
				return errors.New("不能削弱 profile 已声明的 secret_tables：" + req)
			}
		}
	}
	return nil
}

func buildExternalRefProposal(home string, rec *enroll.Record, it DossierProposalItem, now time.Time) (*agentkit.Proposal, string) {
	if it.RefKind != agentkit.ExternalRefDir && it.RefKind != agentkit.ExternalRefSymlink {
		return nil, "ref_kind 必须是 external_dir 或 symlink_target"
	}
	clean, err := ValidateExternalRefPath(home, it.Path, rec.Roots)
	if err != nil {
		return nil, err.Error()
	}
	suggestedCat := strings.TrimSpace(it.SuggestedCategory)
	if suggestedCat != "" && !AllowedLocalRootCategory(suggestedCat) {
		return nil, "suggested_category 只允许 identity、memory、workspace、state、skills、config 或 secrets"
	}
	files, bytes := EstimateDir(clean)
	return &agentkit.Proposal{
		ID:   newID("p_"),
		Kind: agentkit.ProposalKindExternalRef,
		ExternalRef: &agentkit.ExternalRefSpec{
			Path:              clean,
			RefKind:           it.RefKind,
			SuggestedCategory: suggestedCat,
			EstimatedFiles:    files,
			EstimatedBytes:    bytes,
		},
		Reason:  it.Reason,
		Created: now.UTC(),
	}, ""
}

func buildToolDepProposal(it DossierProposalItem, now time.Time) (*agentkit.Proposal, string) {
	name := strings.TrimSpace(it.Name)
	if err := enroll.ValidToolName(name); err != nil {
		return nil, err.Error()
	}
	purpose := trimReason(it.Purpose)
	return &agentkit.Proposal{
		ID:      newID("p_"),
		Kind:    agentkit.ProposalKindToolDep,
		ToolDep: &agentkit.ToolDepSpec{Name: name, Purpose: purpose},
		Reason:  it.Reason,
		Created: now.UTC(),
	}, ""
}

var proposalSecretFragRe = regexp.MustCompile(`(?i)(?:[?#](?:token|key|secret|auth|code)=|\bbearer\s+[A-Za-z0-9._-]{8,}|\bsk-[A-Za-z0-9]{10,}|\bghp_[A-Za-z0-9]{10,}|\bglpat-[A-Za-z0-9_-]{8,}|\bAIza[A-Za-z0-9_-]{10,})`)

func hasSuspiciousSecretFragment(s string) bool {
	return s != "" && proposalSecretFragRe.MatchString(s)
}

func checkEntryProfileConflict(p *profile.Profile, id string, port int) string {
	if p == nil {
		return ""
	}
	for _, e := range p.Entries {
		if e.LocalSource != "" {
			continue
		}
		if e.ID == id {
			return "入口 id 与 profile 原生声明冲突：" + id
		}
		if e.Port == port {
			return "端口 " + strconv.Itoa(port) + " 已被 profile 原生入口 " + e.ID + " 占用"
		}
	}
	return ""
}

func buildWebEntryProposal(p *profile.Profile, it DossierProposalItem, now time.Time) (*agentkit.Proposal, string) {
	le := enroll.LocalEntry{
		ID:          it.ID,
		DisplayName: it.DisplayName,
		Port:        it.Port,
		Path:        it.Path,
		Hint:        it.Hint,
		Reason:      it.Reason,
	}
	if err := enroll.ValidLocalEntry(&le); err != nil {
		return nil, err.Error()
	}
	if hasSuspiciousSecretFragment(le.DisplayName) || hasSuspiciousSecretFragment(le.Hint) || hasSuspiciousSecretFragment(le.Reason) {
		return nil, "入口名称、说明或原因不能包含令牌、密钥或 URL 凭据参数"
	}
	if why := checkEntryProfileConflict(p, le.ID, le.Port); why != "" {
		return nil, why
	}
	return &agentkit.Proposal{
		ID:   newID("p_"),
		Kind: agentkit.ProposalKindWebEntry,
		WebEntry: &agentkit.WebEntrySpec{
			ID:          le.ID,
			DisplayName: le.DisplayName,
			Port:        le.Port,
			Path:        le.Path,
			Hint:        le.Hint,
		},
		Reason:  le.Reason,
		Created: now.UTC(),
	}, ""
}

// Decide applies the user's decisions: accepted proposals are persisted into
// local_overrides, local_sqlite, external_refs, local_roots, or tool_deps.
// Demotions require AcceptDemotions. External refs may be promoted to backed-up
// local_roots via PromoteExternalRoots.
func Decide(home string, d agentkit.DecideParams) (*agentkit.DecideResult, error) {
	rec, err := porterenroll.ReadRecord(home, d.InstanceID)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, valise.New("instance_not_enrolled", "服务器上没有接入 Valise 的实例 "+d.InstanceID, "先运行 valise enroll <服务器> --accept")
	}
	prof, err := porterenroll.ReadProfile(home, d.InstanceID)
	if err != nil {
		return nil, err
	}
	ps, err := LoadProposals(home, d.InstanceID)
	if err != nil {
		return nil, err
	}
	promoteByID := make(map[string]agentkit.ExternalRootPromotion, len(d.PromoteExternalRoots))
	for _, promo := range d.PromoteExternalRoots {
		if promo.ProposalID != "" {
			promoteByID[promo.ProposalID] = promo
		}
	}
	all := func(list []string) bool { return slices.Contains(list, "all") }
	res := &agentkit.DecideResult{Accepted: []agentkit.Proposal{}, Rejected: []string{}}
	var keep []agentkit.Proposal
	matchedPromotions := map[string]bool{}
	rootsChanged := false
	for _, pr := range ps {
		if promo, ok := promoteByID[pr.ID]; ok {
			matchedPromotions[pr.ID] = true
			if err := promoteExternalProposal(home, rec, pr, promo); err != nil {
				return nil, valise.New("proposal_invalid", "无法将提议 "+pr.ID+" 纳入备份："+err.Error(), "检查目录路径与类别后重试")
			}
			rootsChanged = true
			res.Accepted = append(res.Accepted, pr)
			continue
		}
		switch {
		case pr.Demotion && slices.Contains(d.AcceptDemotions, pr.ID),
			!pr.Demotion && (slices.Contains(d.Accept, pr.ID) || (pr.EffectiveKind() != agentkit.ProposalKindWebEntry && all(d.Accept))):
			if err := applyAcceptedProposal(home, rec, prof, pr); err != nil {
				return nil, valise.New("proposal_invalid", "提议 "+pr.ID+" 已不再有效："+err.Error(), "拒绝它")
			}
			res.Accepted = append(res.Accepted, pr)
		case all(d.Reject) || slices.Contains(d.Reject, pr.ID):
			res.Rejected = append(res.Rejected, pr.ID)
		default:
			keep = append(keep, pr)
		}
	}
	for _, promo := range d.PromoteExternalRoots {
		if promo.ProposalID == "" || !matchedPromotions[promo.ProposalID] {
			return nil, valise.New("proposal_invalid", "未找到待确认的外部目录提议 "+promo.ProposalID, "刷新归档建议后重试")
		}
	}
	if len(res.Accepted) > 0 {
		if rec.LocalOverrides == nil {
			rec.LocalOverrides = []profile.Override{}
		}
		writeRec := func() error {
			_, err := porterenroll.UpdateRecord(home, *rec)
			return err
		}
		if rootsChanged {
			if err := porteridentity.SyncBoundRoots(home, d.InstanceID, rec.Roots, writeRec); err != nil {
				return nil, err
			}
		} else if err := writeRec(); err != nil {
			return nil, err
		}
	}
	if keep == nil {
		keep = []agentkit.Proposal{}
	}
	res.Remaining = len(keep)
	return res, saveProposals(home, d.InstanceID, keep)
}

func promoteExternalProposal(home string, rec *enroll.Record, pr agentkit.Proposal, promo agentkit.ExternalRootPromotion) error {
	if pr.EffectiveKind() != agentkit.ProposalKindExternalRef || pr.ExternalRef == nil {
		return errors.New("只能将 external_ref 提议晋升为附加目录")
	}
	cleanPath, err := ValidateLocalRootDir(home, pr.ExternalRef.Path, rec.Roots)
	if err != nil {
		return err
	}
	cat := strings.TrimSpace(promo.Category)
	if cat == "" {
		cat = strings.TrimSpace(pr.ExternalRef.SuggestedCategory)
	}
	if cat == "" {
		cat = profile.CategoryWorkspace
	}
	if !AllowedLocalRootCategory(cat) {
		return errors.New("附加目录类别只允许 identity、memory、workspace、state、skills、config 或 secrets")
	}
	if rec.Roots == nil {
		rec.Roots = map[string]string{}
	}
	rootID := strings.TrimSpace(promo.RootID)
	if rootID != "" {
		if err := enroll.ValidRootID(rootID); err != nil {
			return err
		}
		if _, exists := rec.Roots[rootID]; exists {
			return errors.New("附加目录 id 已存在：" + rootID)
		}
	} else {
		rootID = DeriveRootID(cleanPath, rec.Roots)
	}
	rec.LocalRoots = append(rec.LocalRoots, enroll.LocalRoot{
		ID:       rootID,
		Path:     cleanPath,
		Category: cat,
		Reason:   pr.Reason,
	})
	rec.Roots[rootID] = cleanPath
	rec.ExternalRefs = slices.DeleteFunc(rec.ExternalRefs, func(e enroll.ExternalRef) bool {
		return filepath.Clean(e.Path) == cleanPath
	})
	if rec.ExternalRefs == nil {
		rec.ExternalRefs = []enroll.ExternalRef{}
	}
	return nil
}

func applyAcceptedProposal(home string, rec *enroll.Record, prof *profile.Profile, pr agentkit.Proposal) error {
	switch pr.EffectiveKind() {
	case agentkit.ProposalKindSQLite:
		if pr.SQLite == nil {
			return errors.New("缺少 sqlite 规则")
		}
		if _, ok := rec.Roots[pr.SQLite.Root]; !ok {
			return errors.New("实例没有 root " + pr.SQLite.Root)
		}
		if err := profile.ValidateGlob(pr.SQLite.Glob); err != nil {
			return err
		}
		if err := checkSQLiteNotWeakened(prof, *pr.SQLite, nil); err != nil {
			return err
		}
		rec.LocalSQLite = slices.DeleteFunc(rec.LocalSQLite, func(s profile.SQLiteRef) bool {
			return s.Root == pr.SQLite.Root && s.Glob == pr.SQLite.Glob
		})
		rec.LocalSQLite = append(rec.LocalSQLite, *pr.SQLite)
	case agentkit.ProposalKindExternalRef:
		if pr.ExternalRef == nil {
			return errors.New("缺少 external_ref 定义")
		}
		if pr.ExternalRef.RefKind != agentkit.ExternalRefDir && pr.ExternalRef.RefKind != agentkit.ExternalRefSymlink {
			return errors.New("未知 external_ref 类型")
		}
		clean, err := ValidateExternalRefPath(home, pr.ExternalRef.Path, rec.Roots)
		if err != nil {
			return err
		}
		rec.ExternalRefs = slices.DeleteFunc(rec.ExternalRefs, func(e enroll.ExternalRef) bool {
			return filepath.Clean(e.Path) == clean
		})
		rec.ExternalRefs = append(rec.ExternalRefs, enroll.ExternalRef{
			Path:   clean,
			Kind:   pr.ExternalRef.RefKind,
			Reason: pr.Reason,
		})
	case agentkit.ProposalKindToolDep:
		if pr.ToolDep == nil {
			return errors.New("缺少 tool_dep 定义")
		}
		name := strings.TrimSpace(pr.ToolDep.Name)
		if err := enroll.ValidToolName(name); err != nil {
			return err
		}
		rec.ToolDeps = slices.DeleteFunc(rec.ToolDeps, func(td enroll.ToolDep) bool {
			return td.Name == name
		})
		rec.ToolDeps = append(rec.ToolDeps, enroll.ToolDep{
			Name:    name,
			Purpose: strings.TrimSpace(pr.ToolDep.Purpose),
			Reason:  pr.Reason,
		})
	case agentkit.ProposalKindWebEntry:
		if pr.WebEntry == nil {
			return errors.New("缺少 web_entry 定义")
		}
		le := enroll.LocalEntry{
			ID:          pr.WebEntry.ID,
			DisplayName: pr.WebEntry.DisplayName,
			Port:        pr.WebEntry.Port,
			Path:        pr.WebEntry.Path,
			Hint:        pr.WebEntry.Hint,
			Reason:      pr.Reason,
		}
		if err := enroll.ValidLocalEntry(&le); err != nil {
			return err
		}
		if hasSuspiciousSecretFragment(le.DisplayName) || hasSuspiciousSecretFragment(le.Hint) || hasSuspiciousSecretFragment(le.Reason) {
			return errors.New("入口名称、说明或原因包含疑似凭据片段")
		}
		if why := checkEntryProfileConflict(prof, le.ID, le.Port); why != "" {
			return errors.New(why)
		}
		rec.LocalEntries = slices.DeleteFunc(rec.LocalEntries, func(x enroll.LocalEntry) bool {
			return x.ID == le.ID
		})
		rec.LocalEntries = append(rec.LocalEntries, le)
	default:
		if err := pr.Rule.ValidateWithRoots(prof, localRootIDs(rec.LocalRoots)); err != nil {
			return err
		}
		if !allowedClassifyCategories[pr.Rule.Category] {
			return errors.New("不支持提议类别 " + pr.Rule.Category)
		}
		if pr.Rule.Category != profile.CategorySecrets {
			if err := ensureNoSecretDowngrade(rec, prof, pr.Rule); err != nil {
				return err
			}
		}
		rec.LocalOverrides = slices.DeleteFunc(rec.LocalOverrides, func(o profile.Override) bool {
			return o.Root == pr.Rule.Root && o.Glob == pr.Rule.Glob
		})
		rec.LocalOverrides = append(rec.LocalOverrides, pr.Rule)
	}
	return nil
}

func ensureNoSecretDowngrade(rec *enroll.Record, prof *profile.Profile, rule profile.Override) error {
	cls := profile.NewClassifier(prof, rec.LocalOverrides).
		WithSQLite(rec.LocalSQLite).
		WithDefaultRoots(localRootDefaults(rec.LocalRoots))
	if prof != nil {
		for _, r := range prof.Paths {
			if r.Category == profile.CategorySecrets && r.Root == rule.Root {
				if r.Glob == rule.Glob || rule.Glob == "**" || (profile.IsLiteral(r.Glob) && profile.MatchGlob(rule.Glob, r.Glob)) {
					return errors.New(r.Glob + " 已按凭据规则保护，不能改为其他类别")
				}
			}
		}
	}
	for _, o := range rec.LocalOverrides {
		if o.Category == profile.CategorySecrets && o.Root == rule.Root {
			if o.Glob == rule.Glob || rule.Glob == "**" || (profile.IsLiteral(o.Glob) && profile.MatchGlob(rule.Glob, o.Glob)) {
				return errors.New(o.Glob + " 已按凭据规则保护，不能改为其他类别")
			}
		}
	}
	if !strings.ContainsAny(rule.Glob, "*?[") {
		if cls.Classify(rule.Root, rule.Glob).Category == profile.CategorySecrets {
			return errors.New(rule.Glob + " 已按凭据规则保护，不能改为其他类别")
		}
	}
	_, files, why := resolveGlobInRoot(rec, rule.Root, rule.Glob, true)
	if why != "" && why != "路径不存在" {
		return errors.New(why)
	}
	for _, r := range files {
		if cls.Classify(rule.Root, r).Category == profile.CategorySecrets {
			return errors.New(r + " 已按凭据规则保护，不能改为其他类别")
		}
	}
	return nil
}
