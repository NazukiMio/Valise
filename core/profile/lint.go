package profile

import (
	"fmt"
	"net"
	"regexp"
	"slices"
	"strings"
)

// Issue is one profilelint finding. Every issue is an error.
type Issue struct {
	File    string `json:"file"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

func (i Issue) String() string {
	if i.Field == "" {
		return fmt.Sprintf("%s: %s", i.File, i.Message)
	}
	return fmt.Sprintf("%s: %s: %s", i.File, i.Field, i.Message)
}

// Result is the lint outcome of one document.
type Result struct {
	Doc *Document
	// Effective is the profile after applying extends; nil if it could not
	// be resolved.
	Effective *Profile
	Issues    []Issue
}

var (
	idRe    = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
	tableRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// childAllowedKeys are the top-level keys a child profile may declare.
var childAllowedKeys = []string{
	"schema", "id", "display_name", "profile_version", "status",
	"harness_versions_tested", "docs", "extends", "when",
	"paths", "consistency", "checkup",
}

// Lint checks a set of documents against docs/PROFILES.md section 3. Child
// profiles are resolved against parents in the same set.
func Lint(docs []*Document) []Result {
	byID := map[string]*Document{}
	results := make([]Result, len(docs))
	for i, d := range docs {
		results[i].Doc = d
		id := d.Profile.ID
		if prev, dup := byID[id]; dup && id != "" {
			results[i].Issues = append(results[i].Issues, Issue{d.Path, "id", fmt.Sprintf("id %q 与 %s 重复", id, prev.Path)})
			continue
		}
		byID[id] = d
	}
	for i, d := range docs {
		c := &checker{file: d.Path}
		eff, err := resolve(d, byID, nil)
		if err != nil {
			c.add("extends", "%v", err)
		}
		c.checkDoc(d, eff)
		results[i].Effective = eff
		results[i].Issues = append(results[i].Issues, c.issues...)
	}
	return results
}

// resolve returns the effective profile of d, applying extends recursively.
func resolve(d *Document, byID map[string]*Document, seen []string) (*Profile, error) {
	p := d.Profile
	if p.Extends == "" {
		return p, nil
	}
	if slices.Contains(seen, p.ID) {
		return nil, fmt.Errorf("extends 出现循环：%v", append(seen, p.ID))
	}
	parentDoc, ok := byID[p.Extends]
	if !ok {
		return nil, fmt.Errorf("找不到父 profile %q，它必须出现在同一次 lint 的文件集合中", p.Extends)
	}
	parent, err := resolve(parentDoc, byID, append(seen, p.ID))
	if err != nil {
		return nil, err
	}
	return Merge(parent, p), nil
}

// Merge applies a child profile to its effective parent. A child can only
// append paths, consistency.sqlite and checkup lists; metadata comes from the
// child.
func Merge(parent, child *Profile) *Profile {
	m := *parent
	m.Schema, m.ID, m.DisplayName = child.Schema, child.ID, child.DisplayName
	m.ProfileVersion, m.Status = child.ProfileVersion, child.Status
	m.HarnessVersionsTested, m.Extends, m.When = child.HarnessVersionsTested, child.Extends, child.When
	if child.Docs != "" {
		m.Docs = child.Docs
	}
	m.Paths = append(slices.Clone(parent.Paths), child.Paths...)
	if child.Consistency != nil {
		cons := Consistency{}
		if parent.Consistency != nil {
			cons = *parent.Consistency
		}
		cons.SQLite = append(slices.Clone(cons.SQLite), child.Consistency.SQLite...)
		m.Consistency = &cons
	}
	if child.Checkup != nil {
		ck := Checkup{}
		if parent.Checkup != nil {
			ck = *parent.Checkup
		}
		ck.MemoryFiles = append(slices.Clone(ck.MemoryFiles), child.Checkup.MemoryFiles...)
		ck.MemoryMechanismMarkers = append(slices.Clone(ck.MemoryMechanismMarkers), child.Checkup.MemoryMechanismMarkers...)
		m.Checkup = &ck
	}
	return &m
}

type checker struct {
	file   string
	issues []Issue
	roots  []string
}

func (c *checker) add(field, format string, a ...any) {
	c.issues = append(c.issues, Issue{c.file, field, fmt.Sprintf(format, a...)})
}

func (c *checker) checkDoc(d *Document, eff *Profile) {
	p := d.Profile
	if p.Schema != Schema {
		c.add("schema", "必须是 %q", Schema)
	}
	if !idRe.MatchString(p.ID) {
		c.add("id", "只能包含小写字母、数字和连字符：%q", p.ID)
	}
	if p.DisplayName == "" {
		c.add("display_name", "不能为空")
	}
	if p.ProfileVersion < 1 {
		c.add("profile_version", "必须是正整数")
	}
	switch p.Status {
	case StatusDraft:
	case StatusVerified:
		for _, pd := range d.Pending {
			c.add("status", "verified 的 profile 不允许出现 verify: pending（第 %d 行）", pd.Line)
		}
		if len(p.HarnessVersionsTested) == 0 {
			c.add("harness_versions_tested", "verified 的 profile 必须填写核实时使用的版本")
		}
	default:
		c.add("status", "只能是 draft 或 verified")
	}

	if p.Extends != "" {
		for _, k := range d.TopKeys {
			if !slices.Contains(childAllowedKeys, k) {
				c.add(k, "子 profile 只能追加 paths、consistency、checkup，不能声明 %s", k)
			}
		}
		if p.When == nil || p.When.Exists == nil {
			c.add("when", "子 profile 必须声明 when.exists 生效条件")
		}
		if p.Consistency != nil && p.Consistency.Quiesce != "" {
			c.add("consistency.quiesce", "子 profile 只能追加规则，不能修改 quiesce")
		}
		if p.Checkup != nil && (p.Checkup.Sessions != nil || p.Checkup.NativeOptionsDoc != "" || p.Checkup.Thresholds != nil) {
			c.add("checkup", "子 profile 只能追加 memory_files 与 memory_mechanism_markers")
		}
	} else if p.When != nil {
		c.add("when", "只有子 profile（声明了 extends）才能设置 when")
	}

	if eff == nil {
		return
	}
	c.roots = []string{StateRoot}
	for _, r := range eff.Roots {
		c.roots = append(c.roots, r.ID)
	}
	if p.Extends == "" {
		c.checkTop(eff)
	}
	if p.When != nil && p.When.Exists != nil {
		c.ref("when.exists", *p.When.Exists)
	}
	for i, r := range p.Paths {
		f := fmt.Sprintf("paths[%d]", i)
		c.ref(f, PathRef{r.Root, r.Glob})
		if !IsCategory(r.Category) {
			c.add(f+".category", "未知类别 %q", r.Category)
		}
		if r.Verify != "" && r.Verify != "pending" {
			c.add(f+".verify", "只允许取值 pending")
		}
	}
	if p.Consistency != nil {
		for i, r := range p.Consistency.SQLite {
			f := fmt.Sprintf("consistency.sqlite[%d]", i)
			c.ref(f, r.Ref())
			for j, tbl := range r.SecretTables {
				if !tableRe.MatchString(tbl) {
					c.add(fmt.Sprintf("%s.secret_tables[%d]", f, j), "表名只能包含字母、数字和下划线")
				}
			}
		}
	}
	if p.Checkup != nil {
		c.checkCheckup(p.Checkup)
	}
	c.checkSecretConflicts(eff)
}

func (c *checker) checkTop(p *Profile) {
	if p.Detect == nil || len(p.Detect.StateDirCandidates) == 0 {
		c.add("detect.state_dir_candidates", "不能为空")
	} else {
		for i, s := range p.Detect.StateDirCandidates {
			if err := validateBaseTemplate(s, VarHome); err != nil {
				c.add(fmt.Sprintf("detect.state_dir_candidates[%d]", i), "%v", err)
			}
		}
		for i, m := range p.Detect.MarkersAny {
			if err := validateRelPath(m, false); err != nil {
				c.add(fmt.Sprintf("detect.markers_any[%d]", i), "%v", err)
			}
		}
		c.argv("detect.version_argv", p.Detect.VersionArgv, false)
	}
	seen := map[string]bool{StateRoot: true}
	for i, r := range p.Roots {
		f := fmt.Sprintf("roots[%d]", i)
		if !idRe.MatchString(r.ID) {
			c.add(f+".id", "只能包含小写字母、数字和连字符")
		} else if seen[r.ID] {
			c.add(f+".id", "root id %q 重复或与 state 冲突", r.ID)
		}
		seen[r.ID] = true
		if err := validateBaseTemplate(r.Default, VarStateDir, VarHome); err != nil {
			c.add(f+".default", "%v", err)
		}
		c.argv(f+".discover_argv", r.DiscoverArgv, false)
	}

	if cons := p.Consistency; cons != nil && cons.Quiesce != "" && cons.Quiesce != "none" && cons.Quiesce != "stop_service" {
		c.add("consistency.quiesce", "只能是 none 或 stop_service")
	}
	c.checkProvenance(p.Provenance)
	if b := p.Body; b != nil {
		for i, r := range b.Paths {
			c.ref(fmt.Sprintf("body.paths[%d]", i), r)
		}
		c.argv("body.bootstrap_argv", b.BootstrapArgv, false)
	}
	if p.Relocation != nil && !envNameRe.MatchString(p.Relocation.Env) {
		c.add("relocation.env", "环境变量名不合法")
	}
	if p.Service != nil {
		c.argv("service.install_argv", p.Service.InstallArgv, false)
		if len(p.Service.InstallArgv) > 0 && len(p.Service.Candidates) == 0 {
			c.add("service.install_argv", "没有服务单元候选时不能声明安装命令")
		}
		for i, u := range p.Service.Candidates {
			f := fmt.Sprintf("service.candidates[%d]", i)
			if u.Scope != "user" {
				c.add(f+".scope", "只能是 user")
			}
			if u.Unit == "" {
				c.add(f+".unit", "不能为空")
			}
		}
	}
	entries := map[string]bool{}
	for i, e := range p.Entries {
		f := fmt.Sprintf("entries[%d]", i)
		if e.ID == "" || entries[e.ID] {
			c.add(f+".id", "不能为空，且不能重复")
		}
		entries[e.ID] = true
		if e.Kind != "web" {
			c.add(f+".kind", "只能是 web")
		}
		if e.Host != "localhost" && net.ParseIP(e.Host) == nil {
			c.add(f+".host", "必须是 IP 地址或 localhost")
		}
		if e.Port < 1 || e.Port > 65535 {
			c.add(f+".port", "端口必须在 1–65535 之间")
		}
		if len(e.Path) == 0 || e.Path[0] != '/' {
			c.add(f+".path", "必须以 / 开头")
		}
	}
	if h := p.Health; h != nil {
		if h.GraceSeconds < 0 {
			c.add("health.grace_seconds", "不能为负数")
		}
		for i, ch := range h.Checks {
			c.checkHealth(fmt.Sprintf("health.checks[%d]", i), ch, entries)
		}
	}
	for i, l := range p.Login {
		f := fmt.Sprintf("login[%d]", i)
		switch l.Kind {
		case "loopback_oauth", "device_code", "paste_code", "remote_browser":
		default:
			c.add(f+".kind", "未知登录方式 %q", l.Kind)
		}
		if l.ID == "" {
			c.add(f+".id", "不能为空")
		}
		c.argv(f+".argv", l.Argv, true)
		if l.URLPattern != "" {
			if _, err := regexp.Compile(l.URLPattern); err != nil {
				c.add(f+".url_pattern", "正则表达式无效")
			}
		}
		if l.TimeoutSeconds < 0 {
			c.add(f+".timeout_seconds", "不能为负数")
		}
	}
	for i, r := range p.ExclusiveSecrets {
		c.ref(fmt.Sprintf("exclusive_secrets[%d]", i), r)
	}
	if p.Native != nil && p.Native.ExportArgv != nil {
		c.argvVars("native.export_argv", p.Native.ExportArgv, true, VarHome, VarStateDir, VarOut)
	}
	if h := p.Handoff; h != nil {
		for i, t := range h.Targets {
			f := fmt.Sprintf("handoff.targets[%d]", i)
			c.ref(f, PathRef{t.Root, t.Glob})
			if t.Mode != "append_section" {
				c.add(f+".mode", "只能是 append_section")
			}
			if t.MaxLines <= 0 {
				c.add(f+".max_lines", "必须是正整数")
			}
		}
	}
	if a := p.AgentKit; a != nil && a.SkillsDir != nil {
		c.rootID("agent_kit.skills_dir.root", a.SkillsDir.Root)
		if err := validateRelPath(a.SkillsDir.Path, true); err != nil {
			c.add("agent_kit.skills_dir.path", "%v", err)
		}
	}
	if a := p.AgentKit; a != nil && a.MCPAddArgv != nil {
		c.argvVars("agent_kit.mcp_add_argv", a.MCPAddArgv, true, VarPorter, VarInstance)
	}
}

func (c *checker) checkProvenance(pv *Provenance) {
	if pv == nil {
		c.add("provenance", "不能为空")
		return
	}
	switch pv.Kind {
	case ProvenanceNPMGlobal:
		if pv.NPM == nil || pv.NPM.Package == "" {
			c.add("provenance.npm.package", "npm_global 必须声明包名")
		}
	case ProvenanceGit:
		if pv.Git == nil {
			c.add("provenance.git", "git 必须声明 root 与 path")
		} else {
			c.rootID("provenance.git.root", pv.Git.Root)
			if err := validateRelPath(pv.Git.Path, true); err != nil {
				c.add("provenance.git.path", "%v", err)
			}
		}
	case ProvenanceUVTool:
		if pv.UV == nil || pv.UV.Package == "" {
			c.add("provenance.uv.package", "uv_tool 必须声明包名")
		}
	case ProvenancePipx:
		if pv.Pipx == nil || pv.Pipx.Package == "" {
			c.add("provenance.pipx.package", "pipx 必须声明包名")
		}
	case ProvenanceBinary:
		if pv.Binary == nil || pv.Binary.Name == "" || strings.Contains(pv.Binary.Name, "/") {
			c.add("provenance.binary.name", "binary 必须声明命令名（不含路径）")
		}
	default:
		c.add("provenance.kind", "未知来源类型 %q", pv.Kind)
	}
}

func (c *checker) checkHealth(f string, ch HealthCheck, entries map[string]bool) {
	needEntry := func() {
		if !entries[ch.Entry] {
			c.add(f+".entry", "引用了不存在的入口 %q", ch.Entry)
		}
	}
	switch ch.Kind {
	case "systemd_active":
	case "tcp":
		needEntry()
	case "http":
		needEntry()
		if len(ch.ExpectStatus) == 0 {
			c.add(f+".expect_status", "不能为空")
		}
		for _, s := range ch.ExpectStatus {
			if s < 100 || s > 599 {
				c.add(f+".expect_status", "HTTP 状态码无效：%d", s)
			}
		}
	case "argv":
		c.argv(f+".argv", ch.Argv, true)
	default:
		c.add(f+".kind", "未知检查类型 %q", ch.Kind)
	}
}

var (
	identRe     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	fieldPathRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)*$`)
)

func (c *checker) checkCheckup(ck *Checkup) {
	if s := ck.Sessions; s != nil {
		c.ref("checkup.sessions", PathRef{s.Root, s.Glob})
		switch s.Format {
		case "jsonl":
			if s.SQLite != nil {
				c.add("checkup.sessions.sqlite", "只用于 format: sqlite")
			}
			for i, u := range s.UsageFields {
				if !fieldPathRe.MatchString(u) {
					c.add(fmt.Sprintf("checkup.sessions.usage_fields[%d]", i), "必须是点分字段路径")
				}
			}
			if s.TimeField != "" && !fieldPathRe.MatchString(s.TimeField) {
				c.add("checkup.sessions.time_field", "必须是点分字段路径")
			}
		case "sqlite":
			if len(s.UsageFields) > 0 || s.TimeField != "" {
				c.add("checkup.sessions", "format: sqlite 用 sqlite 子项声明列，不用 usage_fields、time_field")
			}
			if t := s.SQLite; t != nil {
				for f, v := range map[string]string{"table": t.Table, "session_column": t.SessionColumn, "time_column": t.TimeColumn} {
					if !identRe.MatchString(v) {
						c.add("checkup.sessions.sqlite."+f, "必须是标识符（字母、数字、下划线）")
					}
				}
				for f, v := range map[string]string{"tokens_column": t.TokensColumn, "size_column": t.SizeColumn} {
					if v != "" && !identRe.MatchString(v) {
						c.add("checkup.sessions.sqlite."+f, "必须是标识符（字母、数字、下划线）")
					}
				}
				for i, v := range t.BytesColumns {
					if !identRe.MatchString(v) {
						c.add(fmt.Sprintf("checkup.sessions.sqlite.bytes_columns[%d]", i), "必须是标识符（字母、数字、下划线）")
					}
				}
				if t.TimeUnit != "s" && t.TimeUnit != "ms" {
					c.add("checkup.sessions.sqlite.time_unit", "只能是 s 或 ms")
				}
				if j := t.TokensJSON; j != nil {
					if !identRe.MatchString(j.Column) || !fieldPathRe.MatchString(j.Path) {
						c.add("checkup.sessions.sqlite.tokens_json", "column 必须是标识符，path 必须是点分字段路径")
					}
					if t.TokensColumn != "" {
						c.add("checkup.sessions.sqlite.tokens_json", "与 tokens_column 只能二选一")
					}
				}
				if u := t.Usage; u != nil {
					idents := append([]string{u.Table, u.SessionColumn}, u.TokensColumns...)
					if u.TimeColumn != "" {
						idents = append(idents, u.TimeColumn)
						if u.TimeUnit != "s" && u.TimeUnit != "ms" {
							c.add("checkup.sessions.sqlite.usage.time_unit", "只能是 s 或 ms")
						}
					}
					for _, v := range idents {
						if !identRe.MatchString(v) {
							c.add("checkup.sessions.sqlite.usage", "必须是标识符（字母、数字、下划线）：%q", v)
						}
					}
					if len(u.TokensColumns) == 0 {
						c.add("checkup.sessions.sqlite.usage.tokens_columns", "不能为空")
					}
				}
			}
		default:
			c.add("checkup.sessions.format", "只能是 jsonl 或 sqlite")
		}
	}
	if t := ck.Thresholds; t != nil && (t.LongSessionDays < 0 || t.LongSessionTokens < 0 || t.CostGrowthMin < 0 || t.PileupBytes < 0 || t.ColdDays < 0) {
		c.add("checkup.thresholds", "不能为负数")
	}
	for i, m := range ck.MemoryFiles {
		f := fmt.Sprintf("checkup.memory_files[%d]", i)
		c.ref(f, PathRef{m.Root, m.Glob})
		if m.WarnLines <= 0 {
			c.add(f+".warn_lines", "必须是正整数")
		}
	}
	for i, r := range ck.MemoryMechanismMarkers {
		c.ref(fmt.Sprintf("checkup.memory_mechanism_markers[%d]", i), r)
	}
}

// checkSecretConflicts enforces that no secrets glob also appears in body,
// cache or ignore (see docs/PROFILES.md section 3 for the matching rule).
func (c *checker) checkSecretConflicts(p *Profile) {
	type other struct {
		ref   PathRef
		where string
	}
	var others []other
	for i, r := range p.Paths {
		switch r.Category {
		case CategoryBody, CategoryCache, CategoryIgnore:
			others = append(others, other{PathRef{r.Root, r.Glob}, fmt.Sprintf("paths[%d] (%s)", i, r.Category)})
		}
	}
	if p.Body != nil {
		for i, r := range p.Body.Paths {
			others = append(others, other{r, fmt.Sprintf("body.paths[%d]", i)})
		}
	}
	for i, s := range p.Paths {
		if s.Category != CategorySecrets || ValidateGlob(s.Glob) != nil {
			continue
		}
		for _, o := range others {
			if o.ref.Root != s.Root || ValidateGlob(o.ref.Glob) != nil {
				continue
			}
			if o.ref.Glob == s.Glob || (IsLiteral(s.Glob) && MatchGlob(o.ref.Glob, s.Glob)) {
				c.add(fmt.Sprintf("paths[%d]", i), "secrets 规则 %q 同时出现在 %s 中", s.Glob, o.where)
			}
		}
	}
}

func (c *checker) rootID(f, root string) {
	if !slices.Contains(c.roots, root) {
		c.add(f, "引用了未声明的 root %q", root)
	}
}

func (c *checker) ref(f string, r PathRef) {
	c.rootID(f+".root", r.Root)
	if err := ValidateGlob(r.Glob); err != nil {
		c.add(f+".glob", "%v", err)
	}
}

func (c *checker) argv(f string, a Argv, required bool) {
	c.argvVars(f, a, required, VarHome, VarStateDir)
}

func (c *checker) argvVars(f string, a Argv, required bool, vars ...string) {
	if len(a) == 0 {
		if required {
			c.add(f, "不能为空")
		}
		return
	}
	if a[0] == "" {
		c.add(f, "argv[0] 不能为空")
	}
	for i, s := range a {
		if err := checkVars(s, vars...); err != nil {
			c.add(fmt.Sprintf("%s[%d]", f, i), "%v", err)
		}
	}
}
