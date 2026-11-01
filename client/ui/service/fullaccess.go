package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"valise/client/porter"
	"valise/core/install"
	"valise/core/valise"
)

// Full access mode (docs/INSTALL.md section 4.1): enabled by the user for
// one server after both passphrases and the consent sentence; Valise then
// runs the profile's requirement installs (sudo included) in terminals of
// fresh admin connections. It lives only in this process.

// FullAccessConsent is the sentence the user types to enable the mode.
const FullAccessConsent = "我知道完全访问模式可能损坏服务器，后果由我自行承担"

// FullAccessRisks are shown before the consent sentence.
var FullAccessRisks = []string{
	"Valise 将以你的身份、包括管理员权限修改服务器",
	"系统包与运行时的改动不在 agent 的逃生舱里，Valise 无法撤销",
	"疑点检查只能发现明显的问题，不能保证安全",
	"Valise 不为任何第三方软件背书",
}

type fullAccess struct {
	mu    sync.Mutex
	host  string
	since time.Time
}

// FullAccessState is what the frontend shows.
type FullAccessState struct {
	Host  string    `json:"host"`
	Since time.Time `json:"since"`
	// Paused is set when the admin window has expired: the admin
	// passphrase is asked again before the next step.
	Paused bool `json:"paused"`
}

// FullAccessReady checks the preconditions without asking for anything.
func (s *Service) FullAccessReady(host string) error {
	if !s.server(host).Valise() {
		return valise.New("full_access_unsupported", host+" 不是由 Valise 接入的服务器，不提供完全访问模式",
			"完全访问模式需要 Valise 的日常钥匙与管理钥匙：用 valise server add 接入一台正常的服务器")
	}
	if !s.HasAdminKey(host) {
		return valise.New("full_access_no_admin_key", host+" 还没有管理钥匙", "先创建：valise server admin-key "+host)
	}
	if !fileExists(s.keystorePath()) {
		return valise.New("full_access_no_key", "还没有设置 Valise 口令（本机没有 Valise 密钥）", "先运行 valise key init")
	}
	return nil
}

// EnableFullAccess challenges both passphrases and the consent sentence,
// then enables the mode for host. Enabling it for another server ends it
// for the previous one.
func (s *Service) EnableFullAccess(ctx context.Context, host, valisePass, adminPass, consent string) error {
	if err := s.FullAccessReady(host); err != nil {
		return err
	}
	if strings.TrimSpace(consent) != FullAccessConsent {
		return valise.New("full_access_no_consent", "确认句不一致，没有开启完全访问模式", "照原样输入："+FullAccessConsent)
	}
	k, err := s.Unlock(valisePass)
	if err != nil {
		return valise.New("full_access_valise_passphrase", "Valise 口令不对，没有开启完全访问模式", "重新输入")
	}
	k.Lock()
	if err := s.loadAdmin(ctx, host, adminPass); err != nil {
		return err
	}
	s.DisableFullAccess(ctx)
	s.full.mu.Lock()
	s.full.host, s.full.since = host, time.Now()
	s.full.mu.Unlock()
	s.auditFullAccess(ctx, host, "enabled", nil)
	return nil
}

// FullAccess reports the mode's state; nil when it is off.
func (s *Service) FullAccess(ctx context.Context) *FullAccessState {
	s.full.mu.Lock()
	host, since := s.full.host, s.full.since
	s.full.mu.Unlock()
	if host == "" {
		return nil
	}
	return &FullAccessState{Host: host, Since: since, Paused: !s.AdminUnlocked(ctx, host)}
}

// FullAccessHost is the server full access mode is on for, "" when off
// (cheap: no agent query, for the UI's top bar).
func (s *Service) FullAccessHost() string {
	s.full.mu.Lock()
	defer s.full.mu.Unlock()
	return s.full.host
}

// DisableFullAccess ends the mode.
func (s *Service) DisableFullAccess(ctx context.Context) {
	s.full.mu.Lock()
	host := s.full.host
	s.full.host = ""
	s.full.mu.Unlock()
	if host != "" {
		s.auditFullAccess(ctx, host, "disabled", nil)
	}
}

func (s *Service) auditFullAccess(ctx context.Context, host, result string, steps []install.StepAudit) {
	_ = s.withSession(context.WithoutCancel(ctx), host, func(c caller) error {
		return c.Call(ctx, install.MethodAudit, install.AuditParams{Op: install.AuditFullAccess, PlanID: "full-access", Steps: steps, Result: result}, nil)
	})
}

// CheckedCommand is one command of a batch with its check result.
type CheckedCommand struct {
	Command  []string          `json:"command"`
	Source   string            `json:"source"`
	Findings []install.Finding `json:"findings"`
}

// Command sources (docs/INSTALL.md section 7).
const SourceProfile = "profile"

// RequirementBatch is the plan's requirement installs, checked.
type RequirementBatch struct {
	Commands []CheckedCommand `json:"commands"`
	// Unfixable are missing requirements the profile has no command for on
	// this distribution.
	Unfixable []install.Requirement `json:"unfixable,omitempty"`
	Refused   bool                  `json:"refused"`
	Sentence  bool                  `json:"needs_sentence"`
}

// CheckRequirements builds the batch of requirement installs for a plan.
func CheckRequirements(plan *InstallPlan) *RequirementBatch {
	b := &RequirementBatch{}
	for _, argv := range plan.Fixes {
		fs := install.CheckCommand(argv, "")
		b.Commands = append(b.Commands, CheckedCommand{Command: argv, Source: SourceProfile, Findings: fs})
		b.Refused = b.Refused || install.Refused(fs)
		b.Sentence = b.Sentence || install.NeedsRiskSentence(fs)
	}
	b.Unfixable = plan.Unfixable
	return b
}

// RunRequirements runs a confirmed batch in full access mode, one terminal
// per command on a fresh admin connection, then checks the requirements
// again. Valise reads nothing typed in the terminal (sudo's password
// included).
func (s *Service) RunRequirements(ctx context.Context, plan *InstallPlan, b *RequirementBatch, tio InstallIO) (*InstallPlan, error) {
	st := s.FullAccess(ctx)
	if st == nil || st.Host != plan.Host {
		return nil, valise.New("full_access_off", "完全访问模式没有为 "+plan.Host+" 开启", "")
	}
	if b.Refused {
		return nil, valise.New("full_access_refused", "这批命令里有 Valise 一律不执行的命令，整批都没有执行",
			"Valise 不会代为执行它们，也没有绕过的开关；如果坚持，只能自己登录服务器执行")
	}
	if len(b.Unfixable) > 0 {
		return nil, valise.New("install_requirements_unfixable", "profile 没有为这台服务器的发行版给出 "+b.Unfixable[0].Binary+" 的安装命令",
			"自己在服务器上装好它，或交给服务器管理员")
	}
	var audit []install.StepAudit
	defer func() { s.auditFullAccess(ctx, plan.Host, "requirements", audit) }()
	for i, c := range b.Commands {
		if tio.Step != nil {
			tio.Step(i, PlanStep{Kind: "requirement", Summary: strings.Join(c.Command, " "), Command: c.Command})
		}
		// The admin window is checked before every step; when it has
		// expired the admin passphrase is asked again (the mode pauses).
		if _, err := s.unlockAdmin(ctx, plan.Host); err != nil {
			return nil, err
		}
		if s.FullAccess(ctx) == nil {
			return nil, valise.New("full_access_off", "完全访问模式已经关闭，后面的命令没有执行", "")
		}
		t, done, err := s.adminTransport(ctx, plan.Host)
		if err != nil {
			return nil, err
		}
		found, err := porter.Probe(ctx, t)
		if err != nil {
			done()
			return nil, err
		}
		code, err := runTTY(ctx, t, found.Path, nil, c.Command, tio)
		done()
		audit = append(audit, install.StepAudit{Kind: c.Source, Command: strings.Join(c.Command, " "), Exit: code})
		if err != nil {
			return nil, err
		}
		if code != 0 {
			return nil, valise.New("install_requirement_failed", fmt.Sprintf("依赖安装命令失败（退出码 %d）：%s", code, strings.Join(c.Command, " ")),
				"看上面的输出找原因；后面的命令没有执行")
		}
	}
	again, err := s.PlanInstall(ctx, plan.Host, plan.ProfileID)
	if err != nil {
		return nil, err
	}
	if len(again.Missing) > 0 {
		return again, valise.New("install_requirements", "命令都已执行，但服务器上仍缺少 "+again.Missing[0].Binary,
			again.Missing[0].Reason)
	}
	return again, nil
}
