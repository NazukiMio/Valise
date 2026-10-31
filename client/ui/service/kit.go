package service

import (
	"context"
	"io/fs"
	"path"
	"strings"

	agentkitfiles "valise/agentkit"
	"valise/client/porter"
	"valise/client/ssh"
	"valise/core/agentkit"
	"valise/core/valise"
)

// Checkup runs the code-only checkup of an instance on host.
func (s *Service) Checkup(ctx context.Context, host, instance string) (*agentkit.CheckupResult, error) {
	var res agentkit.CheckupResult
	err := s.withSession(ctx, host, func(c *ssh.RPCClient) error {
		return c.Call(ctx, agentkit.MethodCheckup, agentkit.CheckupParams{InstanceID: instance}, &res)
	})
	return &res, err
}

// Proposals lists an instance's pending classification proposals.
func (s *Service) Proposals(ctx context.Context, host, instance string) ([]agentkit.Proposal, error) {
	var res agentkit.ProposalsResult
	err := s.withSession(ctx, host, func(c *ssh.RPCClient) error {
		return c.Call(ctx, agentkit.MethodProposalsList, agentkit.ProposalsParams{InstanceID: instance}, &res)
	})
	return res.Proposals, err
}

// DecideProposals applies the user's decisions.
func (s *Service) DecideProposals(ctx context.Context, host string, d agentkit.DecideParams) (*agentkit.DecideResult, error) {
	var res agentkit.DecideResult
	err := s.withSession(ctx, host, func(c *ssh.RPCClient) error {
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

// KitPlan is what installing the skills will do, and how to wire MCP.
type KitPlan struct {
	Result     *agentkit.KitInstallResult `json:"result,omitempty"`
	NoSkills   string                     `json:"no_skills,omitempty"`   // why skills cannot be installed
	MCPCommand []string                   `json:"mcp_command,omitempty"` // the harness's own command, never run by Valise
	MCPDoc     string                     `json:"mcp_doc,omitempty"`
	AgentLine  string                     `json:"agent_line"`
}

// KitInstall installs the skills into an instance's skills_dir (dryRun only
// reports) and prepares the MCP instructions.
func (s *Service) KitInstall(ctx context.Context, host, instance string, dryRun bool) (*KitPlan, error) {
	files, err := KitFiles()
	if err != nil {
		return nil, err
	}
	target, err := s.LoginMethods(ctx, host, instance)
	if err != nil {
		return nil, err
	}
	t, _, err := s.transport(host)
	if err != nil {
		return nil, err
	}
	found, err := porter.Probe(ctx, t)
	if err != nil {
		return nil, err
	}
	plan := &KitPlan{}
	var res agentkit.KitInstallResult
	err = s.withSession(ctx, host, func(c *ssh.RPCClient) error {
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
	serverCmd := found.Path + " mcp --instance " + instance
	plan.AgentLine = "Add an MCP server named \"valise\" (stdio) that runs: " + serverCmd +
		" — then use the valise-enroll skill: 我给你装了一个逃生舱，你学习一下。"
	if ak := target.Profile.AgentKit; ak != nil {
		plan.MCPDoc = ak.MCPConfigDoc
		for _, a := range ak.MCPAddArgv {
			a = strings.ReplaceAll(a, "${PORTER}", found.Path)
			a = strings.ReplaceAll(a, "${INSTANCE}", instance)
			plan.MCPCommand = append(plan.MCPCommand, a)
		}
	}
	return plan, nil
}
