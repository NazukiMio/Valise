package service

import (
	"context"
	"io/fs"
	"path"
	"strings"

	agentkitfiles "valise/agentkit"
	"valise/core/agentkit"
	"valise/core/profile"
	"valise/core/valise"
)

// Checkup runs the code-only checkup of an instance on host.
func (s *Service) Checkup(ctx context.Context, host, instance string) (*agentkit.CheckupResult, error) {
	var res agentkit.CheckupResult
	err := s.withSession(ctx, host, func(c caller) error {
		return c.Call(ctx, agentkit.MethodCheckup, agentkit.CheckupParams{InstanceID: instance}, &res)
	})
	return &res, err
}

// Proposals lists an instance's pending classification proposals.
func (s *Service) Proposals(ctx context.Context, host, instance string) ([]agentkit.Proposal, error) {
	var res agentkit.ProposalsResult
	err := s.withSession(ctx, host, func(c caller) error {
		return c.Call(ctx, agentkit.MethodProposalsList, agentkit.ProposalsParams{InstanceID: instance}, &res)
	})
	return res.Proposals, err
}

// DecideProposals applies the user's decisions.
func (s *Service) DecideProposals(ctx context.Context, host string, d agentkit.DecideParams) (*agentkit.DecideResult, error) {
	stopOp := s.BeginOperation()
	defer stopOp()
	var res agentkit.DecideResult
	err := s.withSession(ctx, host, func(c caller) error {
		return c.Call(ctx, agentkit.MethodProposalsDecide, d, &res)
	})
	return &res, err
}

// KitFiles returns the skills shipped with this client.
func KitFiles() ([]agentkit.SkillFile, error) {
	var out []agentkit.SkillFile
	err := fs.WalkDir(agentkitfiles.Skills, "skills", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := agentkitfiles.Skills.ReadFile(p)
		if err != nil {
			return err
		}
		out = append(out, agentkit.SkillFile{Skill: path.Base(path.Dir(p)), Name: path.Base(p), Content: b})
		return nil
	})
	return out, err
}

// KitGuide is the daily-tier MCP wiring and conversation prompt guide for an instance.
type KitGuide struct {
	HasSkillsDir  bool     `json:"has_skills_dir"`
	PorterPath    string   `json:"porter_path"`
	ServerCmd     string   `json:"server_cmd"`
	MCPCommand    []string `json:"mcp_command,omitempty"`
	MCPDoc        string   `json:"mcp_doc,omitempty"`
	MCPSnippet    string   `json:"mcp_snippet,omitempty"`
	MCPRemoveCmd  []string `json:"mcp_remove_command,omitempty"`
	MCPRemoveNote bool     `json:"mcp_remove_snippet"`
	AgentLine     string   `json:"agent_line"`
	OnboardSay    string   `json:"onboard_say"`
	StatusSay     string   `json:"status_say"`
	BackupSay     string   `json:"backup_say"`
	// HasService/ServiceUnit: the instance's effective profile declares a
	// service unit that a restart can reload MCP configuration through.
	HasService  bool   `json:"has_service"`
	ServiceUnit string `json:"service_unit,omitempty"`
}

func buildKitGuide(target *LoginTarget, porterPath, instance string) *KitGuide {
	serverCmd := porterPath + " mcp --instance " + instance
	onboardSay := "用 valise-onboard 验收连接并报备你的目录布局。"
	g := &KitGuide{
		PorterPath: porterPath,
		ServerCmd:  serverCmd,
		OnboardSay: onboardSay,
		StatusSay:  "用 valise-backup 看看你现在的 Valise 备份状态。",
		BackupSay:  "动手改之前，先用 valise-backup 帮我留个底。",
		AgentLine: "Add an MCP server named \"valise\" (stdio) that runs: " + serverCmd +
			" — then run valise-onboard: " + onboardSay,
	}
	var ak *profile.AgentKit
	if target != nil {
		ak = target.Profile.AgentKit
	}
	if target != nil {
		g.ServiceUnit = kitServiceUnit(target)
		g.HasService = g.ServiceUnit != ""
	}
	if ak != nil {
		g.HasSkillsDir = ak.SkillsDir != nil
		g.MCPDoc = ak.MCPConfigDoc
		g.MCPSnippet = strings.NewReplacer("${PORTER}", porterPath, "${INSTANCE}", instance).Replace(ak.MCPConfigSnippet)
		for _, a := range ak.MCPAddArgv {
			a = strings.ReplaceAll(a, "${PORTER}", porterPath)
			a = strings.ReplaceAll(a, "${INSTANCE}", instance)
			g.MCPCommand = append(g.MCPCommand, a)
		}
		for _, a := range ak.MCPRemoveArgv {
			g.MCPRemoveCmd = append(g.MCPRemoveCmd, strings.ReplaceAll(a, "${INSTANCE}", instance))
		}
		g.MCPRemoveNote = len(ak.MCPRemoveArgv) == 0 && ak.MCPConfigSnippet != ""
	}
	return g
}

// KitGuide builds the MCP wiring and prompt guide using only daily-tier RPCs.
func (s *Service) KitGuide(ctx context.Context, host, instance string) (*KitGuide, error) {
	target, err := s.LoginMethods(ctx, host, instance)
	if err != nil {
		return nil, err
	}
	porterPath, err := s.porterAbsPath(ctx, host)
	if err != nil {
		return nil, err
	}
	return buildKitGuide(target, porterPath, instance), nil
}

// KitPlan is what installing the skills will do, and how to wire MCP.
type KitPlan struct {
	Result     *agentkit.KitInstallResult `json:"result,omitempty"`
	NoSkills   string                     `json:"no_skills,omitempty"`   // why skills cannot be installed
	MCPCommand []string                   `json:"mcp_command,omitempty"` // the harness's own command, never run by Valise
	MCPDoc     string                     `json:"mcp_doc,omitempty"`
	MCPSnippet string                     `json:"mcp_snippet,omitempty"` // configuration to paste, never applied by Valise
	AgentLine  string                     `json:"agent_line"`
	// MCPWire and ServiceRestart are set only when the person asked Valise
	// to run the harness's own add command / restart the service (not dry run).
	MCPWire        *KitStepOutcome           `json:"mcp_wire,omitempty"`
	ServiceRestart *KitServiceRestartOutcome `json:"service_restart,omitempty"`
}

// RewireMCP runs only the harness's MCP add command (and, when restartService
// is true and the instance has a managed unit, restarts the service) without
// writing or modifying skill files.
func (s *Service) RewireMCP(ctx context.Context, host, instance string, restartService bool) (*KitPlan, error) {
	stopOp := s.BeginOperation()
	defer stopOp()
	target, err := s.LoginMethods(ctx, host, instance)
	if err != nil {
		return nil, err
	}
	porterPath, err := s.porterAbsPath(ctx, host)
	if err != nil {
		return nil, err
	}
	guide := buildKitGuide(target, porterPath, instance)
	plan := &KitPlan{
		AgentLine:  guide.AgentLine,
		MCPDoc:     guide.MCPDoc,
		MCPSnippet: guide.MCPSnippet,
		MCPCommand: guide.MCPCommand,
		MCPWire:    s.kitRunStep(ctx, host, target, guide.MCPCommand, instance, false, "kit-mcp-wire-"),
	}
	if restartService && kitServiceUnit(target) != "" {
		plan.ServiceRestart = s.kitRestartService(ctx, host, instance, target, false)
	}
	return plan, nil
}

// KitInstall installs the skills into an instance's skills_dir (dryRun only
// reports) and prepares the MCP instructions.
func (s *Service) KitInstall(ctx context.Context, host, instance string, dryRun bool) (*KitPlan, error) {
	return s.KitInstallWithOptions(ctx, host, instance, KitInstallOptions{}, dryRun)
}

// KitInstallWithOptions is KitInstall plus the optional one-click MCP wiring
// and service restart (kit_wire.go). Skills installation is never undone by a
// failed wire or restart; each step reports its own outcome.
func (s *Service) KitInstallWithOptions(ctx context.Context, host, instance string, opts KitInstallOptions, dryRun bool) (*KitPlan, error) {
	if !dryRun {
		stopOp := s.BeginOperation()
		defer stopOp()
	}
	files, err := KitFiles()
	if err != nil {
		return nil, err
	}
	target, err := s.LoginMethods(ctx, host, instance)
	if err != nil {
		return nil, err
	}
	porterPath, err := s.porterAbsPath(ctx, host)
	if err != nil {
		return nil, err
	}
	plan := &KitPlan{}
	var res agentkit.KitInstallResult
	err = s.withSession(ctx, host, func(c caller) error {
		return c.Call(ctx, agentkit.MethodKitInstall, agentkit.KitInstallParams{InstanceID: instance, Files: files, DryRun: dryRun}, &res)
	})
	switch ue, _ := valise.As(err); {
	case err == nil:
		plan.Result = &res
	case ue != nil && ue.Code == "kit_no_skills_dir":
		plan.NoSkills = ue.Reason // MCP can still be wired up
	default:
		return nil, err
	}
	guide := buildKitGuide(target, porterPath, instance)
	plan.AgentLine = guide.AgentLine
	plan.MCPDoc = guide.MCPDoc
	plan.MCPSnippet = guide.MCPSnippet
	plan.MCPCommand = guide.MCPCommand
	if opts.WireMCP {
		plan.MCPWire = s.kitRunStep(ctx, host, target, guide.MCPCommand, instance, dryRun, "kit-mcp-wire-")
	}
	if opts.RestartService {
		plan.ServiceRestart = s.kitRestartService(ctx, host, instance, target, dryRun)
	}
	return plan, nil
}

// KitStatus reports the agent-kit skills on an instance and its last agent
// call through MCP.
func (s *Service) KitStatus(ctx context.Context, host, instance string) (*agentkit.KitStatusResult, error) {
	files, err := KitFiles()
	if err != nil {
		return nil, err
	}
	var res agentkit.KitStatusResult
	err = s.withSession(ctx, host, func(c caller) error {
		return c.Call(ctx, agentkit.MethodKitStatus, agentkit.KitStatusParams{InstanceID: instance, Files: files}, &res)
	})
	if err == nil {
		fillKitSkillChange(&res, files)
	}
	return &res, err
}

// fillKitSkillChange adds the shipped text for modified skills and infers
// ChangeKind when an older porter did not report it.
func fillKitSkillChange(res *agentkit.KitStatusResult, files []agentkit.SkillFile) {
	shipped := map[string][]byte{}
	for _, f := range files {
		shipped[f.Skill] = f.Content
	}
	for i := range res.Skills {
		sk := &res.Skills[i]
		if sk.State == agentkit.SkillModified {
			sk.ShippedContent = string(shipped[sk.Skill])
		}
		if sk.ChangeKind != "" {
			continue
		}
		switch sk.State {
		case agentkit.SkillMissing:
			sk.ChangeKind = agentkit.SkillChangeMissing
		case agentkit.SkillCurrent:
			sk.ChangeKind = agentkit.SkillChangeCurrent
		default:
			sk.ChangeKind = agentkit.SkillChangeLocalModified
		}
	}
}

// KitRemovalPlan is what uninstalling does and how to unwire MCP.
type KitRemovalPlan struct {
	Result         *agentkit.KitUninstallResult `json:"result"`
	MCPCommand     []string                     `json:"mcp_command,omitempty"` // never run by Valise
	MCPSnippet     bool                         `json:"mcp_snippet"`           // MCP was added by pasting configuration
	MCPServerName  string                       `json:"mcp_server_name"`
	MCPWire        *KitStepOutcome              `json:"mcp_wire,omitempty"` // the unwire step
	ServiceRestart *KitServiceRestartOutcome    `json:"service_restart,omitempty"`
}

// KitUninstall removes the installed skills (dryRun only reports). Modified
// skills are kept unless force.
func (s *Service) KitUninstall(ctx context.Context, host, instance string, force, dryRun bool) (*KitRemovalPlan, error) {
	return s.KitUninstallWithOptions(ctx, host, instance, KitUninstallOptions{Force: force}, dryRun)
}

// KitUninstallWithOptions is KitUninstall plus the optional MCP unwiring and
// service restart.
func (s *Service) KitUninstallWithOptions(ctx context.Context, host, instance string, opts KitUninstallOptions, dryRun bool) (*KitRemovalPlan, error) {
	force := opts.Force
	if !dryRun {
		stopOp := s.BeginOperation()
		defer stopOp()
	}
	files, err := KitFiles()
	if err != nil {
		return nil, err
	}
	target, err := s.LoginMethods(ctx, host, instance)
	if err != nil {
		return nil, err
	}
	var res agentkit.KitUninstallResult
	if err := s.withSession(ctx, host, func(c caller) error {
		return c.Call(ctx, agentkit.MethodKitUninstall, agentkit.KitUninstallParams{InstanceID: instance, Files: files, Force: force, DryRun: dryRun}, &res)
	}); err != nil {
		return nil, err
	}
	guide := buildKitGuide(target, "", instance)
	plan := &KitRemovalPlan{
		Result:        &res,
		MCPCommand:    guide.MCPRemoveCmd,
		MCPSnippet:    guide.MCPRemoveNote,
		MCPServerName: "valise",
	}
	if opts.UnwireMCP {
		plan.MCPWire = s.kitRunStep(ctx, host, target, guide.MCPRemoveCmd, instance, dryRun, "kit-mcp-unwire-")
	}
	if opts.RestartService {
		plan.ServiceRestart = s.kitRestartService(ctx, host, instance, target, dryRun)
	}
	return plan, nil
}
