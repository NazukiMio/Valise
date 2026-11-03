package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
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

// GateCode is the error code when the precondition is not met.
const GateCode = "offsite_pod_required"

// staleAfter: an offsite pod older than this is warned about (the warning
// can be dismissed).
const staleAfter = 7 * 24 * time.Hour

const repoTrialFile = "repo-trial.json"

// GateAgent is one agent's offsite pod.
type GateAgent struct {
	Instance string      `json:"instance"`
	Pod      *vault.Meta `json:"pod,omitempty"`
	// Where names the place, e.g. "仓库 home" or "空间站 nas".
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
	return valise.New(GateCode, "这一步要先有一份放在别处、能独立恢复的逃生舱（只在服务器上的不算）",
		"建立逃生舱（推荐\"记忆与人格 + 凭据\"），再放进本机仓库或空间站").WithDetail(strings.Join(lines, "\n"))
}

// Warning describes stale offsite pods, "" when none.
func (g *GateResult) Warning() string {
	var lines []string
	for _, a := range g.Agents {
		if a.Pod != nil && a.Stale {
			lines = append(lines, a.Instance+" 放在别处的最新逃生舱是 "+agoText(a.Age)+"前的（"+a.Where+"）；出了问题恢复时，会丢失这段时间的内容")
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
	places := s.offsitePlaces(ctx, host, st)
	for _, id := range want {
		res.Agents = append(res.Agents, s.gateAgent(host, id, st.ServerName, st.Snapshots, places))
	}
	return res, nil
}

// RequireGate refuses when the precondition is not met; a stale pod is
// not a refusal (callers show Warning first).
func (s *Service) RequireGate(ctx context.Context, host, instance string) error {
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

// offsitePlaces lists the snapshots each repository and station holds.
func (s *Service) offsitePlaces(ctx context.Context, host string, st *vaultrpc.StatusResult) []offsitePlace {
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
	for _, t := range st.Targets {
		p := offsitePlace{label: "空间站 " + t.Name, leaf: map[string]string{}}
		list, err := s.TargetList(ctx, host, t.Name)
		if err != nil {
			p.err = "列不出空间站 " + t.Name + " 上的内容：" + errText1(err)
			out = append(out, p)
			continue
		}
		synced := map[string]bool{}
		for _, id := range t.LastSynced {
			synced[id] = true
		}
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
		out = append(out, p)
	}
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
		a.Reason = "还没有逃生舱"
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
	a.Reason = "逃生舱都只在服务器上"
	if len(places) == 0 {
		a.Reason += "；还没有本机仓库或空间站"
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
