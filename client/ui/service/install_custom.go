package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"valise/client/porter"
	"valise/core/enroll"
	"valise/core/install"
	"valise/core/profile"
	"valise/core/valise"
)

// CustomInstallPlan is a user-pasted official HTTPS script URL, one-line
// installer pipeline, or npm install command inspected locally before
// anything is uploaded or run on the server (docs/INSTALL.md section 7).
type CustomInstallPlan struct {
	ID           string            `json:"id"`
	Host         string            `json:"host"`
	ProfileID    string            `json:"profile_id,omitempty"`
	Raw          string            `json:"raw"`
	Kind         string            `json:"kind"` // "script" or "argv"
	URL          string            `json:"url,omitempty"`
	SHA256       string            `json:"sha256,omitempty"`
	ScriptName   string            `json:"script_name,omitempty"`
	Script       []byte            `json:"-"`
	Command      []string          `json:"command"`
	Findings     []install.Finding `json:"findings,omitempty"`
	Refused      bool              `json:"refused,omitempty"`
	NeedsConfirm bool              `json:"needs_confirm,omitempty"`
	Sudo         bool              `json:"sudo,omitempty"`
	Summary      string            `json:"summary"`
}

func (s *Service) downloadHTTPS(ctx context.Context, h *http.Client, rawURL string, max int64) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Host == "" {
		if strings.HasPrefix(strings.ToLower(rawURL), "http://") {
			return nil, valise.New("install_custom_insecure_url", "只支持 HTTPS 官方地址，拒绝不安全的 HTTP 地址", "请改用 https:// 开头的官方地址").WithDetail(rawURL)
		}
		return nil, valise.New("install_custom_invalid_url", "下载地址无效："+rawURL, "请检查官方 HTTPS 脚本地址")
	}
	if h == nil {
		h = http.DefaultClient
	}
	clone := *h
	origCheck := h.CheckRedirect
	var redirectErr error
	clone.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if !strings.EqualFold(req.URL.Scheme, "https") {
			redirectErr = valise.New("install_custom_insecure_url", "下载链接跳转到了不安全的 HTTP 地址，已停止",
				"请直接填写最终的 https:// 官方地址").WithDetail(req.URL.String())
			return redirectErr
		}
		if origCheck != nil {
			return origCheck(req, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	body, err := s.download(ctx, &clone, rawURL, max)
	if redirectErr != nil {
		return nil, redirectErr
	}
	return body, err
}

func validatePlanScripts(plan *InstallPlan) error {
	for _, st := range plan.Steps {
		if st.Kind != profile.StepScript {
			continue
		}
		sum := sha256.Sum256(st.Script)
		got := hex.EncodeToString(sum[:])
		if st.SHA256 != "" && got != st.SHA256 {
			return valise.New("install_script_changed", "安装脚本内容与待确认的 SHA-256 不一致，已停止", "重新加载安装计划并核对脚本")
		}
		if st.ScriptRefused || install.Refused(install.CheckScript(st.Script, "")) {
			return valise.New("install_script_refused", "官方更新后的安装脚本命中了 Valise 一律不执行的规则，已拦截",
				"Valise 不会执行它，也没有绕过开关；可以改用原生方式按官方文档处理")
		}
		if st.ScriptChanged && (!st.ScriptAccepted || st.AcceptedSHA256 == "" || st.AcceptedSHA256 != got) {
			return valise.New("install_script_changed", "官方安装脚本已经更新，请先核对新旧 SHA-256、检查结果与脚本全文并勾选确认",
				"核对最新脚本后勾选确认继续，或改用原生方式安装").WithDetail(fmt.Sprintf("%s\n期望 %s\n实际 %s", st.URL, st.ExpectedSHA256, got))
		}
	}
	return nil
}

// PlanCustomInstall parses a user-pasted official HTTPS script URL or
// common install command, downloads any script in client memory (never via
// curl|bash on the server), computes SHA-256, and runs static checks.
func (s *Service) PlanCustomInstall(ctx context.Context, host, raw, profileID string) (*CustomInstallPlan, error) {
	spec, err := install.ParseCustomInstall(raw)
	if err != nil {
		return nil, err
	}
	var idBytes [6]byte
	_, _ = rand.Read(idBytes[:])
	planID := "inst_" + hex.EncodeToString(idBytes[:])

	plan := &CustomInstallPlan{
		ID:        planID,
		Host:      host,
		ProfileID: profileID,
		Raw:       spec.Raw,
		Kind:      spec.Kind,
		Sudo:      spec.Sudo,
	}
	switch spec.Kind {
	case "script":
		body, err := s.downloadHTTPS(ctx, s.Release.HTTP, spec.URL, maxScriptBytes)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(body)
		shaHex := hex.EncodeToString(sum[:])
		fs := install.CheckScript(body, "")
		if spec.Sudo {
			fs = appendFindingOnce(fs, install.Finding{Rule: install.RulePrivilege, Reason: "以管理员身份运行（sudo）"})
		}
		name := install.SafeScriptFilename(spec.URL)
		cmd := make([]string, 0, len(spec.Interpreter)+len(spec.Args)+2)
		if spec.Sudo {
			cmd = append(cmd, "sudo")
		}
		cmd = append(cmd, spec.Interpreter...)
		cmd = append(cmd, "${HOME}/"+installDir(planID)+"/"+name)
		cmd = append(cmd, spec.Args...)

		plan.URL, plan.SHA256, plan.ScriptName, plan.Script = spec.URL, shaHex, name, body
		plan.Command = cmd
		plan.Findings = fs
		plan.Refused = install.Refused(fs)
		plan.NeedsConfirm = install.NeedsCustomConfirm(fs)
		plan.Summary = fmt.Sprintf("上传脚本 %s（%d 字节）到服务器暂存目录并执行", name, len(body))
	case "argv":
		fs := install.CheckCommand(spec.Argv, "")
		plan.Command = append([]string{}, spec.Argv...)
		plan.Findings = fs
		plan.Refused = install.Refused(fs)
		plan.NeedsConfirm = install.NeedsCustomConfirm(fs)
		plan.Summary = "在服务器上执行：" + strings.Join(spec.Argv, " ")
	default:
		return nil, valise.New("install_custom_unsupported", "不支持这种安装指令形式", "")
	}
	return plan, nil
}

func appendFindingOnce(fs []install.Finding, f install.Finding) []install.Finding {
	for _, cur := range fs {
		if cur.Rule == f.Rule && cur.Reason == f.Reason {
			return fs
		}
	}
	return append(fs, f)
}

// RunCustomInstall re-checks static safety rules and risk confirmation,
// uploads any script into the server's staging directory, and runs the
// command in an interactive terminal on a fresh admin connection.
func (s *Service) RunCustomInstall(ctx context.Context, plan *CustomInstallPlan, riskSentence string, tio InstallIO) (*InstallResult, error) {
	if plan == nil || len(plan.Command) == 0 {
		return nil, valise.New("install_custom_invalid", "安装计划为空", "重新解析安装地址或指令")
	}
	var fs []install.Finding
	switch plan.Kind {
	case "script":
		sum := sha256.Sum256(plan.Script)
		got := hex.EncodeToString(sum[:])
		if len(plan.Script) == 0 || plan.SHA256 == "" || got != plan.SHA256 {
			return nil, valise.New("install_script_changed", "待上传的脚本内容与校验哈希不一致，已停止", "重新下载并预览脚本后重试")
		}
		fs = install.CheckScript(plan.Script, "")
		if plan.Sudo {
			fs = appendFindingOnce(fs, install.Finding{Rule: install.RulePrivilege, Reason: "以管理员身份运行（sudo）"})
		}
	case "argv":
		fs = install.CheckCommand(plan.Command, "")
	default:
		return nil, valise.New("install_custom_unsupported", "不支持这种安装指令形式", "")
	}
	if install.Refused(fs) {
		return nil, valise.New("install_custom_refused", "指令或脚本命中了 Valise 一律不执行的规则，已拦截",
			"Valise 不会执行它，也没有绕过开关；如果坚持要运行，请自己登录服务器处理")
	}
	if install.NeedsCustomConfirm(fs) && strings.TrimSpace(riskSentence) != install.RiskSentence {
		return nil, valise.New("install_risk_sentence", "安全风险确认句不一致，没有执行",
			"请照原样输入："+install.RiskSentence)
	}
	if err := s.RequireGate(ctx, plan.Host, ""); err != nil {
		return nil, err
	}
	t, done, err := s.adminTransport(ctx, plan.Host)
	if err != nil {
		return nil, err
	}
	defer done()
	found, err := porter.Probe(ctx, t)
	if err != nil {
		return nil, err
	}
	if plan.Kind == "script" {
		name := plan.ScriptName
		if name == "" {
			name = install.SafeScriptFilename(plan.URL)
		}
		if err := uploadScript(ctx, t, plan.ID, name, plan.Script); err != nil {
			return nil, err
		}
	}
	var dirs []string
	if plan.ProfileID != "" {
		if set, perr := s.Profiles(); perr == nil && set.ByID[plan.ProfileID] != nil && set.ByID[plan.ProfileID].Effective.Detect != nil {
			dirs = set.ByID[plan.ProfileID].Effective.Detect.BinDirs
		}
	}
	res := &InstallResult{}
	profAudit := plan.ProfileID
	if profAudit == "" {
		profAudit = "custom"
	}
	audit := install.AuditParams{PlanID: plan.ID, ProfileID: profAudit, Result: "failed"}
	defer func() {
		_ = s.withSession(context.WithoutCancel(ctx), plan.Host, func(c caller) error {
			return c.Call(ctx, install.MethodAudit, audit, nil)
		})
	}()

	if tio.Step != nil {
		tio.Step(0, PlanStep{Kind: plan.Kind, Summary: plan.Summary, Command: plan.Command})
	}
	code, err := runLineTTY(ctx, t, execLine(found.Path, dirs, plan.Command), tio)
	res.Exits = append(res.Exits, code)
	audit.Steps = append(audit.Steps, install.StepAudit{Kind: "custom:" + plan.Kind, Command: strings.Join(plan.Command, " "), Exit: code})
	if err != nil {
		return res, err
	}
	if code != 0 {
		return res, valise.New("install_step_failed", fmt.Sprintf("执行失败（退出码 %d）：%s", code, plan.Summary),
			"看上面的输出找原因；也可以改用原生方式按官方文档继续")
	}
	res.Completed = true
	audit.Result = "steps_done"

	if plan.ProfileID != "" {
		det, derr := s.detectProfile(ctx, plan.Host, plan.ProfileID)
		if derr == nil {
			res.Detect = det
			for i := range det.Candidates {
				if det.Candidates[i].ProfileID == plan.ProfileID {
					res.Found = &det.Candidates[i]
					audit.Result, audit.Instance = "installed", plan.ProfileID
					break
				}
			}
		}
		return res, nil
	}
	if set, perr := s.Profiles(); perr == nil && len(set.Effective()) > 0 {
		var det enroll.DetectResult
		if derr := s.withSession(ctx, plan.Host, func(c caller) error {
			return c.Call(ctx, enroll.MethodDetect, enroll.DetectParams{Profiles: set.Effective()}, &det)
		}); derr == nil {
			res.Detect = &det
			if len(det.Candidates) > 0 {
				res.Found = &det.Candidates[0]
				audit.Result, audit.Instance = "installed", det.Candidates[0].ProfileID
			}
		}
	}
	return res, nil
}
