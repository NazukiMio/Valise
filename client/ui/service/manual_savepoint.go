package service

import (
	"context"
	"os"
	"slices"
	"strings"

	"valise/client/config"
	"valise/core/enroll"
	"valise/core/rpc"
	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
)

// ManualSavepointTarget names one destination to sync after creating a manual
// savepoint. Kind may be server_target, station, local_repo, local_cloud, or
// empty to auto-resolve.
type ManualSavepointTarget struct {
	Kind string `json:"kind,omitempty"`
	Name string `json:"name"`
}

// ManualSavepointRequest creates an independent manual savepoint (separate
// from the automatic protection plan).
type ManualSavepointRequest struct {
	Host          string                  `json:"host"`
	InstanceID    string                  `json:"instance_id"`
	Tier          string                  `json:"tier"` // soul-only | soul | soul-secrets | full | raw
	BundleMembers []string                `json:"bundle_members,omitempty"`
	Note          string                  `json:"note,omitempty"`
	Targets       []ManualSavepointTarget `json:"targets,omitempty"`
}

// ManualSavepointSyncResult is the outcome of syncing the manual savepoint to
// one selected target location.
type ManualSavepointSyncResult struct {
	Target    ManualSavepointTarget `json:"target"`
	OK        bool                  `json:"ok"`
	Error     string                `json:"error,omitempty"`
	ErrorCode string                `json:"error_code,omitempty"`
}

// ManualSavepointResult reports the sealed snapshot(s) and explicit target
// sync results.
type ManualSavepointResult struct {
	Host     string                      `json:"host"`
	Tier     string                      `json:"tier"`
	BundleID string                      `json:"bundle_id,omitempty"`
	Members  []string                    `json:"members,omitempty"`
	Reports  []vaultrpc.SealReport       `json:"reports"`
	Syncs    []ManualSavepointSyncResult `json:"syncs,omitempty"`
	Partial  bool                        `json:"partial,omitempty"`
}

// normalizeManualTier maps user-facing tier labels (including "soul-only") to
// canonical vault tier names.
func normalizeManualTier(tier string) (string, error) {
	switch strings.TrimSpace(tier) {
	case "soul-only", vault.TierSoul:
		return vault.TierSoul, nil
	case vault.TierSoulSecrets:
		return vault.TierSoulSecrets, nil
	case vault.TierFull:
		return vault.TierFull, nil
	case vault.TierRaw:
		return vault.TierRaw, nil
	default:
		return "", valise.New("tier_invalid", "未知的备份档位："+tier, "可选 soul-only、soul-secrets、full 或 raw")
	}
}

// SealBundle seals multiple same-harness instances on host under one bundle id.
func (s *Service) SealBundle(ctx context.Context, host string, p vaultrpc.SealBundleParams) (*vaultrpc.SealBundleResult, error) {
	var res vaultrpc.SealBundleResult
	rctx := rpc.WithProgress(ctx, func(stage string, done, total int64) { report(ctx, "seal_"+stage, done, total) })
	err := s.withSession(ctx, host, func(c caller) error {
		return c.Call(rctx, vaultrpc.MethodSealBundle, p, &res)
	})
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// EnsureKeyRegistered registers this computer's age public key on host if it
// is not yet in host's recipients list (same daily-session boundary as GUI's
// ensureRegistered).
func (s *Service) EnsureKeyRegistered(ctx context.Context, host string) error {
	key, err := s.KeyShow()
	if err != nil {
		return err
	}
	st, err := s.VaultStatus(ctx, host)
	if err != nil {
		return err
	}
	if slices.Contains(st.Recipients, key.Fingerprint) {
		return nil
	}
	name, _ := os.Hostname()
	_, _, err = s.KeyRegister(ctx, host, strings.TrimSuffix(name, ".local"))
	return err
}

// CreateManualSavepoint seals an independent manual savepoint (single instance
// or same-harness bundle) and explicitly syncs it to the selected locations.
func (s *Service) CreateManualSavepoint(ctx context.Context, req ManualSavepointRequest) (*ManualSavepointResult, error) {
	stopOp := s.BeginOperation()
	defer stopOp()
	if strings.TrimSpace(req.Host) == "" {
		return nil, valise.New("request_invalid", "缺少目标服务器", "选择要手动存档的服务器")
	}
	if enroll.ValidInstanceID(req.InstanceID) != nil {
		return nil, valise.New("request_invalid", "无效的实例标识："+req.InstanceID, "选择已接入的实例")
	}
	tier, err := normalizeManualTier(req.Tier)
	if err != nil {
		return nil, err
	}
	note, err := vault.NormalizeNote(req.Note)
	if err != nil {
		return nil, err
	}
	members := normalizeBundleMembers(req.InstanceID, req.BundleMembers)
	for _, m := range members {
		if enroll.ValidInstanceID(m) != nil {
			return nil, valise.New("bundle_invalid", "组合备份成员无效："+m, "选择同一 Harness 的已接入实例")
		}
	}
	if err := s.EnsureKeyRegistered(ctx, req.Host); err != nil {
		return nil, err
	}
	out := &ManualSavepointResult{
		Host: req.Host,
		Tier: tier,
	}
	if len(members) >= 2 {
		bres, err := s.SealBundle(ctx, req.Host, vaultrpc.SealBundleParams{
			Members: members,
			Tier:    tier,
			Reason:  "manual",
			Kind:    vault.KindSavepoint,
			Note:    note,
		})
		if err != nil {
			return nil, err
		}
		out.BundleID = bres.BundleID
		out.Members = append([]string(nil), bres.Members...)
		out.Reports = append([]vaultrpc.SealReport(nil), bres.Reports...)
	} else {
		rep, err := s.Seal(ctx, req.Host, vaultrpc.SealParams{
			InstanceID: req.InstanceID,
			Tier:       tier,
			Reason:     "manual",
			Kind:       vault.KindSavepoint,
			Note:       note,
		})
		if err != nil {
			return nil, err
		}
		out.Members = []string{req.InstanceID}
		out.Reports = []vaultrpc.SealReport{*rep}
	}
	for _, t := range req.Targets {
		if strings.TrimSpace(t.Name) == "" {
			continue
		}
		sr := ManualSavepointSyncResult{Target: t, OK: true}
		if serr := s.syncManualTarget(ctx, req.Host, t); serr != nil {
			sr.OK = false
			sr.Error = serr.Error()
			if ue, ok := valise.As(serr); ok {
				sr.ErrorCode = ue.Code
				sr.Error = ue.Reason
			}
			out.Partial = true
		}
		out.Syncs = append(out.Syncs, sr)
	}
	return out, nil
}

func normalizeBundleMembers(self string, raw []string) []string {
	if len(raw) == 0 {
		return nil
	}
	out := make([]string, 0, len(raw)+1)
	seen := map[string]bool{}
	for _, m := range raw {
		m = strings.TrimSpace(m)
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	if !seen[self] {
		out = append(out, self)
	}
	slices.Sort(out)
	return out
}

func (s *Service) syncManualTarget(ctx context.Context, host string, t ManualSavepointTarget) error {
	switch t.Kind {
	case SnapshotLocationLocalRepo:
		_, err := s.RepoSync(ctx, host, t.Name)
		return err
	case SnapshotLocationStation:
		_, err := s.SyncStation(ctx, host, t.Name)
		return err
	case SnapshotLocationLocalCloud:
		_, err := s.SyncCloud(ctx, host, t.Name)
		return err
	case SnapshotLocationServerTarget:
		if s.IsStation(t.Name) {
			_, err := s.SyncStation(ctx, host, t.Name)
			return err
		}
		_, err := s.Sync(ctx, host, t.Name)
		return err
	default:
		switch {
		case s.IsStation(t.Name):
			_, err := s.SyncStation(ctx, host, t.Name)
			return err
		case s.IsCloudHere(t.Name):
			_, err := s.SyncCloud(ctx, host, t.Name)
			return err
		case slices.ContainsFunc(s.Repos(), func(r config.Repo) bool { return r.Name == t.Name }):
			_, err := s.RepoSync(ctx, host, t.Name)
			return err
		default:
			_, err := s.Sync(ctx, host, t.Name)
			return err
		}
	}
}
