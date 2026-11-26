package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"valise/client/profiles"
	"valise/core/enroll"
	"valise/core/identity"
	"valise/core/profile"
	"valise/core/protection"
	"valise/core/valise"
)

// EnrollBatchItem is one agent to enroll in a batch.
type EnrollBatchItem struct {
	ProfileID         string   `json:"profile_id,omitempty"`
	Name              string   `json:"name,omitempty"`
	InstanceID        string   `json:"instance_id,omitempty"`
	Assign            []string `json:"assign,omitempty"`
	EnableDefaultPlan bool     `json:"enable_default_plan,omitempty"`
	ReactivatePlan    bool     `json:"reactivate_plan,omitempty"`
}

// EnrollBatchFailure records one item that could not be enrolled.
type EnrollBatchFailure struct {
	InstanceID string `json:"instance_id"`
	Label      string `json:"label,omitempty"`
	Err        error  `json:"-"`
	Reason     string `json:"reason"`
}

// EnrollBatchResult reports a batch enrollment.
type EnrollBatchResult struct {
	Host        string               `json:"host"`
	Enrolled    []string             `json:"enrolled"`
	PlanApplied []string             `json:"plan_applied,omitempty"`
	PlanNotes   map[string]string    `json:"plan_notes,omitempty"`
	Failures    []EnrollBatchFailure `json:"failures,omitempty"`
}

// EnrollBatch enrolls several agents on one owner session scope: one
// BeginOperation, so an owner unlock prompts at most once for the batch.
func (s *Service) EnrollBatch(ctx context.Context, host string, items []EnrollBatchItem) (*EnrollBatchResult, error) {
	stopOp := s.BeginOperation()
	defer stopOp()
	res := &EnrollBatchResult{Host: host, Enrolled: []string{}, PlanNotes: map[string]string{}}
	set, err := s.Profiles()
	if err != nil {
		return nil, err
	}
	if err := needProfiles(set); err != nil {
		return nil, err
	}
	o, err := s.ownerPorter(ctx, host)
	if err != nil {
		return nil, err
	}
	defer s.invalidateAgents(host)
	var det enroll.DetectResult
	if err := s.callOwner(ctx, o, nil, enroll.MethodDetect, enroll.DetectParams{Profiles: set.Effective()}, &det); err != nil {
		return nil, err
	}
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		id, err := s.enrollBatchOne(ctx, o, host, set, det.Candidates, item, res)
		if err == nil {
			continue
		}
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		reason := err.Error()
		if ue, ok := valise.As(err); ok {
			reason = ue.Reason
		}
		label := item.ProfileID
		if item.Name != "" {
			label += "/" + item.Name
		}
		res.Failures = append(res.Failures, EnrollBatchFailure{InstanceID: id, Label: label, Err: err, Reason: reason})
		if isAuthAbort(err) {
			return res, err
		}
	}
	return res, nil
}

// isAuthAbort reports an error that makes further items pointless.
func isAuthAbort(err error) bool {
	if ue, ok := valise.As(err); ok {
		switch ue.Code {
		case "owner_auth_cancelled", "auth_cancelled", "auth_failed":
			return true
		}
	}
	return errors.Is(err, context.Canceled)
}

func (s *Service) enrollBatchOne(ctx context.Context, o *ownerPorter, host string, set *profiles.Set, cands []enroll.Candidate, item EnrollBatchItem, res *EnrollBatchResult) (string, error) {
	var overrides []profile.Override
	for _, a := range item.Assign {
		ov, err := profile.ParseOverride(a)
		if err != nil {
			return item.InstanceID, valise.New("assign_invalid", err.Error(), "例如：--assign state:notes/**=memory")
		}
		overrides = append(overrides, ov)
	}
	cand, eff, err := choose(set, cands, item.ProfileID, item.Name, host)
	if err != nil {
		return item.InstanceID, err
	}
	loaded := set.ByID[eff]
	if loaded == nil || loaded.Effective == nil {
		return item.InstanceID, valise.New("profile_unknown", "未找到 harness 资料："+eff, "升级客户端")
	}
	id := item.InstanceID
	if id == "" {
		id = CandidateID(cand)
	}
	if err := enroll.ValidInstanceID(id); err != nil {
		return id, valise.New("instance_id_invalid", err.Error(), "用 --id 指定一个由小写字母、数字和连字符组成的 id")
	}
	var rep enroll.Report
	params := enroll.ScanParams{Profile: *loaded.Effective, StateDir: cand.StateDir, InstanceID: id, Overrides: overrides, Name: cand.Name, Env: cand.Env}
	if err := s.callOwner(ctx, o, nil, enroll.MethodScan, params, &rep); err != nil {
		return id, err
	}
	var w enroll.InstanceWriteResult
	rec := enroll.RecordFrom(&rep, time.Now())
	if err := s.callOwner(ctx, o, nil, enroll.MethodInstanceWrite, enroll.InstanceWriteParams{Record: rec, Profile: *loaded.Effective}, &w); err != nil {
		return id, err
	}
	res.Enrolled = append(res.Enrolled, id)
	if item.ReactivatePlan || item.EnableDefaultPlan {
		s.enrollBatchPlan(ctx, host, id, item, res)
	}
	return id, nil
}

func (s *Service) enrollBatchPlan(ctx context.Context, host, id string, item EnrollBatchItem, res *EnrollBatchResult) {
	var binding identity.Binding
	if err := s.call(ctx, host, identity.MethodGet, identity.GetParams{InstanceID: id}, &binding); err != nil {
		return
	}
	if binding.IdentityState != identity.Bound || binding.Agent == nil {
		return
	}
	ref := binding.Agent.Ref
	view, err := s.GetProtection(ctx, host, protection.GetParams{AgentRef: ref})
	if err != nil {
		res.PlanNotes[id] = "未能读取保护计划：" + errText1(err)
		return
	}
	var desired protection.Desired
	switch {
	case (item.ReactivatePlan || item.EnableDefaultPlan) && view.SupersededPlan != nil && view.ServerPlan == nil:
		desired = cloneProtection(view.SupersededPlan.Desired)
	case item.EnableDefaultPlan && view.ServerPlan == nil && view.SupersededPlan == nil:
		off := false
		desired = protection.Desired{
			Scope:        protection.Scope{Kind: "preset", Tier: "full"},
			Schedule:     protection.Schedule{Enabled: &off, Cadence: "daily", Timezone: "UTC"},
			Period:       protection.Period{Unit: "month", Timezone: "UTC"},
			ServerCopies: []protection.ServerCopy{},
		}
	default:
		return
	}
	if desired.ServerCopies == nil {
		desired.ServerCopies = []protection.ServerCopy{}
	}
	ok, note := s.carryOverProtection(ctx, host, ref, "enroll-"+id, desired)
	if ok {
		res.PlanApplied = append(res.PlanApplied, id)
	}
	if note != "" {
		res.PlanNotes[id] = note
	}
}

// QuickReenroll re-enrolls one superseded/known agent and reactivates its
// earlier protection plan.
func (s *Service) QuickReenroll(ctx context.Context, host, instanceID string) (*EnrollBatchResult, error) {
	insts, err := s.Instances(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, in := range insts {
		if in.InstanceID == instanceID {
			return s.EnrollBatch(ctx, host, []EnrollBatchItem{{ProfileID: in.ProfileID, Name: in.Name, InstanceID: instanceID, ReactivatePlan: true}})
		}
	}
	return nil, valise.New("instance_unknown", fmt.Sprintf("服务器 %s 上没有记录过 %s", host, instanceID), "在添加 agent 里重新选择")
}
