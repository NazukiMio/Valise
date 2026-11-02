package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"valise/client/profiles"
	"valise/core/enroll"
	"valise/core/profile"
	"valise/core/valise"
)

// EnrollOptions are the user's choices for `valise enroll`.
type EnrollOptions struct {
	// ProfileID picks a candidate (or a child profile) when several match.
	ProfileID string `json:"profile_id,omitempty"`
	// Name picks a named instance (detect.named); "" is the default one.
	Name string `json:"name,omitempty"`
	// InstanceID defaults to the harness profile id, plus "-<name>" for a
	// named instance.
	InstanceID string `json:"instance_id,omitempty"`
	// Assign are "<root>:<glob>=<category>" decisions for unclassified files.
	Assign []string `json:"assign,omitempty"`
	// Accept writes the instance record; without it nothing is written.
	Accept bool `json:"accept,omitempty"`
}

// EnrollResult is what `valise enroll` shows.
type EnrollResult struct {
	Host       string             `json:"host"`
	Candidates []enroll.Candidate `json:"candidates"`
	Report     *enroll.Report     `json:"report"`
	// ProfileLocal marks a profile from the user's directory (never reviewed).
	ProfileLocal bool `json:"profile_local"`
	// Written is the record path on the server when Accept was set.
	Written string `json:"written,omitempty"`
	// RejectedLocalProfiles failed lint and were ignored.
	RejectedLocalProfiles []string `json:"rejected_local_profiles,omitempty"`
}

// Enroll detects, scans and (with Accept) records one instance.
func (s *Service) Enroll(ctx context.Context, host string, opts EnrollOptions) (*EnrollResult, error) {
	set, err := s.Profiles()
	if err != nil {
		return nil, err
	}
	if err := needProfiles(set); err != nil {
		return nil, err
	}
	var overrides []profile.Override
	for _, a := range opts.Assign {
		o, err := profile.ParseOverride(a)
		if err != nil {
			return nil, valise.New("assign_invalid", err.Error(), "例如：--assign state:notes/**=memory")
		}
		overrides = append(overrides, o)
	}
	c, err := s.session(ctx, host)
	if err != nil {
		return nil, err
	}
	defer c.Close()

	var det enroll.DetectResult
	if err := c.Call(ctx, enroll.MethodDetect, enroll.DetectParams{Profiles: set.Effective()}, &det); err != nil {
		return nil, err
	}
	res := &EnrollResult{Host: host, Candidates: det.Candidates, RejectedLocalProfiles: set.Rejected}
	cand, eff, err := choose(set, det.Candidates, opts.ProfileID, opts.Name, host)
	if err != nil {
		return res, err
	}
	loaded := set.ByID[eff]
	res.ProfileLocal = loaded.Local
	id := opts.InstanceID
	if id == "" {
		id = CandidateID(cand)
	}
	if err := enroll.ValidInstanceID(id); err != nil {
		return res, valise.New("instance_id_invalid", err.Error(), "用 --id 指定一个由小写字母、数字和连字符组成的 id")
	}

	var rep enroll.Report
	// The general profile goes to porter unchanged; the agent's effective
	// profile (ForAgent) is worked out there and from the dossier.
	params := enroll.ScanParams{Profile: *loaded.Effective, StateDir: cand.StateDir, InstanceID: id, Overrides: overrides, Name: cand.Name, Env: cand.Env}
	if err := c.Call(ctx, enroll.MethodScan, params, &rep); err != nil {
		return res, err
	}
	rep.Pending = pendingOf(set, eff)
	rep.Warnings = append(cand.Warnings, rep.Warnings...)
	if cand.Name != "" {
		if loaded.Effective.ForAgent(cand.Name).Service != nil {
			rep.Warnings = append(rep.Warnings, "这是命名实例 "+cand.Name+"：它和默认实例共用同一个服务，恢复它时会短暂停止这个服务（同一安装里的所有 agent 一起停下）")
		} else {
			rep.Warnings = append(rep.Warnings, "这是命名实例 "+cand.Name+"：它的服务与管理页尚未在靶机上核实，Valise 不控制它的服务（恢复时也不会停止或启动任何服务）")
		}
		rep.Provenance.Warnings = []string{"命名实例与默认实例共用同一份安装，Valise 不单独记录它的身体来源"}
	}
	res.Report = &rep

	if opts.Accept {
		var w enroll.InstanceWriteResult
		rec := enroll.RecordFrom(&rep, time.Now())
		if err := c.Call(ctx, enroll.MethodInstanceWrite, enroll.InstanceWriteParams{Record: rec, Profile: *loaded.Effective}, &w); err != nil {
			return res, err
		}
		res.Written = w.Path
	}
	return res, nil
}

// choose picks the candidate and the effective profile id (a child profile
// when exactly one child's condition holds).
// CandidateID is the default instance id of a candidate.
func CandidateID(c enroll.Candidate) string {
	if c.Name != "" {
		return c.ProfileID + "-" + c.Name
	}
	return c.ProfileID
}

// CandidateLabel names a candidate for messages.
func CandidateLabel(c enroll.Candidate) string {
	if c.Name != "" {
		return c.ProfileID + "（" + c.Name + "）"
	}
	return c.ProfileID
}

func choose(set *profiles.Set, cands []enroll.Candidate, want, name, host string) (enroll.Candidate, string, error) {
	if len(cands) == 0 {
		return enroll.Candidate{}, "", valise.New("no_instance", fmt.Sprintf("在服务器 %s 上没有发现任何已知 harness 的实例", host),
			"确认 harness 已安装并至少运行过一次初始化；如果是还没有资料的 harness，可以在本地资料目录中添加一份")
	}
	var pick []enroll.Candidate
	for _, c := range cands {
		if (want == "" || want == c.ProfileID || contains(c.Children, want)) && c.Name == name {
			pick = append(pick, c)
		}
	}
	if len(pick) != 1 {
		var ids []string
		for _, c := range cands {
			ids = append(ids, CandidateLabel(c))
		}
		sort.Strings(ids)
		reason := fmt.Sprintf("服务器 %s 上发现多个实例：%s", host, strings.Join(ids, "、"))
		if len(pick) == 0 {
			reason = fmt.Sprintf("服务器 %s 上没有 harness 资料 %q 的实例（发现的有：%s）", host, want, strings.Join(ids, "、"))
		}
		return enroll.Candidate{}, "", valise.New("choose_instance", reason, "用 --profile <id> 指定要接入的那一个；命名实例再加 --name <名字>")
	}
	c := pick[0]
	switch {
	case want != "" && want != c.ProfileID:
		return c, want, nil
	case len(c.Children) == 1:
		return c, c.Children[0], nil
	case len(c.Children) > 1:
		return c, "", valise.New("choose_profile",
			fmt.Sprintf("实例同时符合多个子 harness 资料：%s", strings.Join(c.Children, "、")),
			"用 --profile <子 profile id> 指定与已安装插件相符的那一个")
	}
	if set.ByID[c.ProfileID] == nil {
		return c, "", valise.New("profile_unknown", "porter 返回了未知的 harness 资料："+c.ProfileID, "升级客户端")
	}
	return c, c.ProfileID, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// pendingOf collects unverified markers of a profile and its parents.
func pendingOf(set *profiles.Set, id string) []profile.Pending {
	var out []profile.Pending
	for l := set.ByID[id]; l != nil; l = set.ByID[l.Doc.Profile.Extends] {
		out = append(out, l.Doc.Pending...)
		if l.Doc.Profile.Extends == "" {
			break
		}
	}
	return out
}

// DetectInstances lists the agents found on host, each with the instance
// already enrolled for its state dir ("" when none).
func (s *Service) DetectInstances(ctx context.Context, host string) ([]DetectedInstance, error) {
	set, err := s.Profiles()
	if err != nil {
		return nil, err
	}
	var det enroll.DetectResult
	if err := s.call(ctx, host, enroll.MethodDetect, enroll.DetectParams{Profiles: set.Effective()}, &det); err != nil {
		return nil, err
	}
	insts, _ := s.Instances(ctx, host)
	var out []DetectedInstance
	for _, c := range det.Candidates {
		d := DetectedInstance{Candidate: c, ID: CandidateID(c)}
		for _, in := range insts {
			if in.Superseded == nil && in.Roots[profile.StateRoot] == c.StateDir {
				d.Enrolled = in.InstanceID
			}
		}
		out = append(out, d)
	}
	return out, nil
}

// DetectedInstance is one agent found on a server.
type DetectedInstance struct {
	enroll.Candidate
	// ID is the default instance id; Enrolled the instance already
	// enrolled for this state dir.
	ID       string `json:"id"`
	Enrolled string `json:"enrolled,omitempty"`
}
