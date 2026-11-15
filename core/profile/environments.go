package profile

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Environments declares a foreground runtime in a private environment. The
// signed profile supplies fixed argv, not a user command or an interpreter.
// Verification is independent of legacy whole-profile restore verification.
type Environments struct {
	RuntimeExecutable *RuntimeExecutable `yaml:"runtime_executable" json:"runtime_executable,omitempty"`
	Engine            string             `yaml:"engine" json:"engine"`
	StartArgv         Argv               `yaml:"start_argv" json:"start_argv"`
	WorkingDir        string             `yaml:"working_dir" json:"working_dir"`
	Path              []string           `yaml:"path" json:"path"`
	ProgramPaths      []PathRef          `yaml:"program_paths" json:"program_paths"`
	GatewayEntry      string             `yaml:"gateway_entry" json:"gateway_entry,omitempty"`
	OfflineReadiness  *OfflineReadiness  `yaml:"offline_readiness" json:"offline_readiness,omitempty"`
	Verification      Capability         `yaml:"verification" json:"verification"`
}

// EnvironmentTested never inherits Profile.Status or capabilities.restore.
func (p *Profile) EnvironmentTested(version string) bool {
	if p.Environments == nil {
		return false
	}
	v := p.Environments.Verification
	_, err := time.Parse("2006-01-02", v.Date)
	return p.Environments.Engine == "linux-bwrap" && v.Status == "tested" && strings.TrimSpace(v.Env) != "" && err == nil && version != "" && slices.Contains(v.Versions, version)
}

func (c *checker) checkEnvironments(p *Profile) {
	e := p.Environments
	if e == nil {
		return
	}
	const f = "environments"
	if e.Engine != "linux-bwrap" {
		c.add(f+".engine", "只能是 linux-bwrap")
	}
	variables := []string{VarHome, VarStateDir}
	if e.RuntimeExecutable != nil {
		variables = append(variables, "RUNTIME_EXECUTABLE")
		r := e.RuntimeExecutable
		if r.Service.Scope != "user" || p.Service == nil || !slices.Contains(p.Service.Candidates, r.Service) || !binaryRe.MatchString(r.Name) || r.Name == "." || r.Name == ".." {
			c.add(f+".runtime_executable", "必须引用本 profile 的用户服务和明确程序文件名")
		}
		if len(e.StartArgv) == 0 || e.StartArgv[0] != "${RUNTIME_EXECUTABLE}" {
			c.add(f+".start_argv", "必须由已核实的 RUNTIME_EXECUTABLE 启动")
		}
	}
	c.argvVars(f+".start_argv", e.StartArgv, true, variables...)
	for _, arg := range e.StartArgv {
		if strings.Contains(arg, "${env:") || strings.ContainsAny(arg, "\x00\r\n") {
			c.add(f+".start_argv", "只允许 HOME / STATE_DIR 变量，不继承宿主环境或脚本")
		}
	}
	if err := validateBaseTemplate(e.WorkingDir, VarHome, VarStateDir); err != nil || strings.Contains(e.WorkingDir, "${env:") {
		c.add(f+".working_dir", "必须位于私有 HOME / STATE_DIR 内")
	}
	if len(e.Path) == 0 {
		c.add(f+".path", "必须声明私有程序目录")
	}
	for i, v := range e.Path {
		bases := []string{VarHome}
		if e.RuntimeExecutable != nil {
			bases = append(bases, "RUNTIME_DIR")
		}
		if err := validateBaseTemplate(v, bases...); err != nil || strings.Contains(v, "${env:") || strings.Contains(v, ":") {
			c.add(fmt.Sprintf("%s.path[%d]", f, i), "必须是私有 HOME 下的单个目录")
		}
	}
	if len(e.ProgramPaths) == 0 {
		c.add(f+".program_paths", "必须声明私有重建的程序目录")
	}
	seen := map[PathRef]bool{}
	for i, r := range e.ProgramPaths {
		field := fmt.Sprintf("%s.program_paths[%d]", f, i)
		c.ref(field, r)
		if !IsLiteral(r.Glob) || seen[r] {
			c.add(field, "必须是无通配符且不重复的明确目录")
		}
		seen[r] = true
	}
	if e.OfflineReadiness != nil {
		if e.GatewayEntry != "" {
			c.add(f+".gateway_entry", "网页与离线就绪只能选择一种")
		}
		c.checkOfflineReadiness(e.OfflineReadiness)
	} else if !slices.ContainsFunc(p.Entries, func(v Entry) bool {
		return v.ID == e.GatewayEntry && v.Kind == "web" && (v.Host == "127.0.0.1" || v.Host == "::1")
	}) {
		c.add(f+".gateway_entry", "必须引用已声明的本机回环 web 入口")
	}
	v := e.Verification
	if !slices.Contains(CapabilityStatuses, v.Status) {
		c.add(f+".verification.status", "必须独立声明能力核实状态")
	}
	if v.Status == "tested" {
		_, dateErr := time.Parse("2006-01-02", v.Date)
		if len(v.Versions) == 0 || strings.TrimSpace(v.Env) == "" || dateErr != nil || slices.ContainsFunc(v.Versions, func(s string) bool { return strings.TrimSpace(s) == "" }) {
			c.add(f+".verification", "tested 必须写 versions、env 与有效 date")
		}
	}
}

// RuntimeExecutable identifies a public executable used by an already declared
// service. No argv, environment, terminal or model configuration is inspected.
type RuntimeExecutable struct {
	Service ServiceUnit `yaml:"service" json:"service"`
	Name    string      `yaml:"name" json:"name"`
}
