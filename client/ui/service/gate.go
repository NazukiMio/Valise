package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
)

// Advanced features need an escape pod kept elsewhere that restores on its
// own (docs/INSTALL.md 4.2): in a repository (checked by this client and
// trial-decrypted) or on a station (in the server's sync record and in the
// station's own listing). Copies only on the server do not count. There
// is no bypass.

// StaleAcceptedEnv is set by the terminal UI when it runs a CLI command
// after the user accepted a stale-backup warning there, so the CLI does
// not ask again. It never skips the precondition itself.
const StaleAcceptedEnv = "VALISE_STALE_BACKUP_ACCEPTED"

// RiskAcceptedEnv is set by the terminal UI when the user chose to go on
// without an escape pod elsewhere and confirmed it there (INSTALL.md 4.2).
const RiskAcceptedEnv = "VALISE_RISK_ACCEPTED"

// RiskWarning is said whenever the user goes on at their own risk.
const RiskWarning = "如果没有充足的备份，数据丢失将是永久性的。建议先把备份复制到备份位置，或在云厂商、虚拟化平台那里给服务器做一份快照（Valise 不做整机备份）。"

// GateCode is the error code when the precondition is not met.
const GateCode = "offsite_pod_required"

// staleAfter: an offsite pod older than this is warned about (the warning
// can be dismissed). A variable for tests.
var staleAfter = 7 * 24 * time.Hour

// targetGateTimeout bounds listing each remote copy location during gate
// checks so one unreachable target cannot stall the rest. A variable for tests.
var targetGateTimeout = 3 * time.Second

const repoTrialFile = "repo-trial.json"

// GateAgent is one agent's offsite pod.
type GateAgent struct {
	Instance string      `json:"instance"`
	Pod      *vault.Meta `json:"pod,omitempty"`
	// Where names the place, e.g. "仓库 home" or "远程备份位置 nas".
	Where string `json:"where,omitempty"`
	// Reason says why there is none, when Pod is nil.
	Reason string `json:"reason,omitempty"`
	// Stale: Pod is older than staleAfter; Age is how old.
	Stale bool          `json:"stale,omitempty"`
	Age   time.Duration `json:"age,omitempty"`
}

// GateResult is the precondition for one agent or a whole server.
type GateResult struct {
	Host   string      `json:"host"`
	Agents []GateAgent `json:"agents"`
}

// OK reports every agent has an offsite pod (true for none).
func (g *GateResult) OK() bool {
	for _, a := range g.Agents {
		if a.Pod == nil {
			return false
		}
	}
	return true
}

// Err is the refusal when not OK, nil otherwise.
func (g *GateResult) Err() error {
	if g.OK() {
		return nil
	}
	var lines []string
	for _, a := range g.Agents {
		if a.Pod == nil {
			lines = append(lines, a.Instance+"："+a.Reason)
		}
	}
	return valise.New(GateCode, "这一步要先有一份放在别处、能独立恢复的备份（只在服务器上的不算）",
		"建立备份（推荐\"记忆与人格 + 凭据\"），再放进备份位置；也可以自己承担风险继续这一次（valise --accept-risk …）").WithDetail(strings.Join(lines, "\n"))
}

// Warning describes stale offsite pods, "" when none.
func (g *GateResult) Warning() string {
	var lines []string
	for _, a := range g.Agents {
		if a.Pod != nil && a.Stale {
			// Said of the place that was checked: when a repository
			// settled it, the server's copy locations were not asked, so
			// nothing is claimed about a newer copy there.
			lines = append(lines, a.Instance+" 在"+a.Where+"里的备份是 "+agoText(a.Age)+"前的；出了问题从这里恢复时，会丢失这段时间的内容")
		}
	}
	return strings.Join(lines, "\n")
}

func agoText(d time.Duration) string {
	if d >= 48*time.Hour {
		return strconv.Itoa(int(d/(24*time.Hour))) + " 天"
	}
	return strconv.Itoa(int(d/time.Hour)) + " 小时"
}

// AdvancedGate checks the precondition for instance on host, or for every
// enrolled agent when instance is "". A repository copy not yet
// trial-decrypted asks for the Valise passphrase (once per pod).
func (s *Service) AdvancedGate(ctx context.Context, host, instance string) (*GateResult, error) {
	insts, err := s.Instances(ctx, host)
	if err != nil {
		return nil, err
	}
	res := &GateResult{Host: host, Agents: []GateAgent{}}
	var want []string
	for _, in := range insts {
		if in.Superseded == nil && (instance == "" || in.InstanceID == instance) {
			want = append(want, in.InstanceID)
		}
	}
	if instance != "" && len(want) == 0 {
		want = []string{instance}
	}
	if len(want) == 0 {
		return res, nil // no agent yet: nothing to lose
	}
	st, err := s.VaultStatus(ctx, host)
	if err != nil {
		return nil, err
	}
	// This computer's repositories first: they are read here, with no
	// connection. When they hold a pod of every agent that is the answer,
	// and the server's copy locations are not asked at all: a slow or dead
	// one must not hold up what the person is doing (INSTALL.md 4.2). Only
	// when a repository does not settle it are the copy locations listed.
	places := s.repoPlaces(host, st)
	check := func() bool {
		res.Agents = res.Agents[:0]
		settled := true
		for _, id := range want {
			a := s.gateAgent(host, id, st.ServerName, st.Snapshots, places)
			settled = settled && a.Pod != nil
			res.Agents = append(res.Agents, a)
		}
		return settled
	}
	if !check() && len(st.Targets) > 0 {
		places = append(places, s.targetPlaces(ctx, host, st)...)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		check()
	}
	return res, nil
}

// RequireGate refuses when the precondition is not met; a stale pod is
// not a refusal (callers show Warning first).
type riskAcceptanceKey struct{}

// WithRiskAccepted carries an explicit user confirmation through one operation.
// It does not alter the shared service or later requests.
func WithRiskAccepted(ctx context.Context) context.Context {
	return context.WithValue(ctx, riskAcceptanceKey{}, true)
}

func (s *Service) RequireGate(ctx context.Context, host, instance string) error {
	accepted, _ := ctx.Value(riskAcceptanceKey{}).(bool)
	if s.RiskAccepted || accepted {
		return nil // the user accepted the risk for this one operation
	}
	g, err := s.AdvancedGate(ctx, host, instance)
	if err != nil {
		return err
	}
	return g.Err()
}

// offsitePlace is where copies of snapshots are known to be.
type offsitePlace struct {
	label string
	repo  string // repository path for trial decryption, "" for a station
	leaf  map[string]string
	err   string
}

// repoPlaces lists the snapshots each repository on this computer holds;
// nothing is connected to.
func (s *Service) repoPlaces(host string, st *vaultrpc.StatusResult) []offsitePlace {
	var out []offsitePlace
	for _, rs := range s.RepoStatuses(host, st.ServerName) {
		p := offsitePlace{label: "仓库 " + rs.Name, repo: rs.Path, leaf: map[string]string{}}
		if !rs.Available {
			p.err = "仓库 " + rs.Name + " 的文件夹现在不可用"
		}
		for leaf, ids := range rs.Leaves {
			for _, id := range ids {
				p.leaf[id] = leaf
			}
		}
		out = append(out, p)
	}
	return out
}

// targetPlaces lists the snapshots each of the server's copy locations
// holds concurrently, skipping unsynced targets and bounding each query with
// targetGateTimeout so an offline target cannot block the gate.
func (s *Service) targetPlaces(ctx context.Context, host string, st *vaultrpc.StatusResult) []offsitePlace {
	out := make([]offsitePlace, len(st.Targets))
	var wg sync.WaitGroup
	for i, t := range st.Targets {
		if len(t.LastSynced) == 0 {
			out[i] = offsitePlace{
				label: "备份位置 " + t.Name,
				leaf:  map[string]string{},
				err:   "远程备份位置 " + t.Name + " 还没有同步记录",
			}
			continue
		}
		wg.Add(1)
		go func(idx int, t vaultrpc.TargetState) {
			defer wg.Done()
			p := offsitePlace{label: "备份位置 " + t.Name, leaf: map[string]string{}}
			tctx, cancel := context.WithTimeout(ctx, targetGateTimeout)
			defer cancel()

			list, err := s.TargetList(tctx, host, t.Name)
			if err != nil {
				if (errors.Is(err, context.DeadlineExceeded) || errors.Is(tctx.Err(), context.DeadlineExceeded)) && ctx.Err() == nil {
					p.err = "查询远程备份位置 " + t.Name + " 超时，已跳过"
				} else {
					p.err = "列不出远程备份位置 " + t.Name + " 上的内容：" + errText1(err)
				}
				out[idx] = p
				return
			}
			synced := map[string]bool{}
			for _, id := range t.LastSynced {
				synced[id] = true
			}
			if list != nil {
				for _, l := range list.Leaves {
					if l.Deleting {
						continue
					}
					for _, id := range l.Snapshots {
						if synced[id] {
							p.leaf[id] = l.Leaf
						}
					}
				}
			}
			out[idx] = p
		}(i, t)
	}
	wg.Wait()
	return out
}

// gateAgent finds instance's newest pod that is offsite and restorable.
func (s *Service) gateAgent(host, instance, server string, metas []vault.Meta, places []offsitePlace) GateAgent {
	a := GateAgent{Instance: instance}
	var pods []vault.Meta
	for _, m := range metas {
		if m.InstanceID == instance {
			pods = append(pods, m)
		}
	}
	if len(pods) == 0 {
		a.Reason = "还没有备份"
		return a
	}
	sort.Slice(pods, func(i, j int) bool { return pods[i].CreatedAt.After(pods[j].CreatedAt) })
	var why []string
	for _, p := range places {
		if p.err != "" {
			why = append(why, p.err)
		}
	}
	for _, m := range pods {
		for _, p := range places {
			leaf, ok := p.leaf[m.SnapshotID]
			if !ok || p.err != "" {
				continue
			}
			if p.repo != "" {
				if err := s.trialDecrypt(host, filepath.Join(p.repo, server, leafDirName(leaf)), m.SnapshotID); err != nil {
					why = append(why, p.label+" 里的 "+m.SnapshotID+" 没有通过试解："+errText1(err))
					continue
				}
			}
			m := m
			a.Pod, a.Where = &m, p.label
			a.Age = time.Since(m.CreatedAt)
			a.Stale = a.Age > staleAfter
			return a
		}
	}
	a.Reason = "备份都只在服务器上"
	if len(places) == 0 {
		a.Reason += "；还没有备份位置"
	}
	if len(why) > 0 {
		a.Reason += "（" + strings.Join(why, "；") + "）"
	}
	return a
}

// trialDecrypt decrypts one snapshot of a repository copy and checks every
// file's hash, once per snapshot (remembered in repo-trial.json).
func (s *Service) trialDecrypt(host, dir, snapshot string) error {
	path := filepath.Join(s.Dirs.Data, repoTrialFile)
	done := map[string]time.Time{}
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, &done)
	}
	key := dir + "#" + snapshot
	if _, ok := done[key]; ok && fileExists(filepath.Join(dir, vault.SnapshotsDir, snapshot+".meta.json")) {
		return nil
	}
	if probs, err := vault.VerifyNoKey(dir); err != nil || len(probs) > 0 {
		if err == nil {
			err = valise.New("repo_verify_failed", probs[0].String(), "")
		}
		return err
	}
	ident, err := s.ownerIdentity(host)
	if err != nil {
		return err
	}
	if _, probs, err := vault.VerifyWithKey(dir, []string{snapshot}, ident); err != nil || len(probs) > 0 {
		if err == nil {
			err = valise.New("repo_verify_failed", probs[0].String(), "")
		}
		return err
	}
	done[key] = time.Now().UTC()
	b, _ := json.MarshalIndent(done, "", "  ")
	return os.WriteFile(path, append(b, '\n'), 0o600)
}
