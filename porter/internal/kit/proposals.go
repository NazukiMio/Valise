package kit

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"valise/porter/internal/runtimeguard"

	"valise/core/agentkit"
	"valise/core/enroll"
	"valise/core/profile"
	"valise/core/valise"
	porterenroll "valise/porter/internal/enroll"
)

// ProposalItem is one entry of valise_propose_enrollment.
type ProposalItem struct {
	Root     string `json:"root"`
	Path     string `json:"path"`
	Category string `json:"category"`
	Reason   string `json:"reason"`
}

func proposalsPath(home, instance string) string {
	return filepath.Join(porterenroll.InstancesDir(home), instance+".proposals.json")
}

// LoadProposals returns the pending proposals of an instance.
func LoadProposals(home, instance string) ([]agentkit.Proposal, error) {
	var ps []agentkit.Proposal
	b, err := os.ReadFile(proposalsPath(home, instance))
	if errors.Is(err, os.ErrNotExist) {
		return []agentkit.Proposal{}, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &ps); err != nil {
		return nil, err
	}
	return ps, nil
}

func saveProposals(home, instance string, ps []agentkit.Proposal) error {
	b, _ := json.MarshalIndent(ps, "", "  ")
	return writeAtomic(proposalsPath(home, instance), b)
}

// Propose validates each item by code and stores it as pending. It returns
// one result line per item (accepted for review, or why it was refused).
func Propose(home string, rec *enroll.Record, p *profile.Profile, items []ProposalItem, now time.Time) ([]string, error) {
	ps, err := LoadProposals(home, rec.InstanceID)
	if err != nil {
		return nil, err
	}
	cls := profile.NewClassifier(p, rec.LocalOverrides)
	var out []string
	for _, it := range items {
		rule, unclassified, why := checkItem(rec, p, cls, it)
		if why != "" {
			out = append(out, it.Root+":"+it.Path+" — 拒绝："+why)
			continue
		}
		reason := strings.TrimSpace(it.Reason)
		if len(reason) > 500 {
			reason = reason[:500]
		}
		np := agentkit.Proposal{ID: newID("p_"), Rule: rule, Reason: reason, Demotion: agentkit.IsDemotion(rule.Category), Created: now.UTC()}
		if rule.Category != "secrets" && !np.Demotion {
			np.Unclassified = unclassified
		}
		i := slices.IndexFunc(ps, func(x agentkit.Proposal) bool { return x.Rule.Root == rule.Root && x.Rule.Glob == rule.Glob })
		if i >= 0 {
			np.ID = ps[i].ID
			ps[i] = np
		} else {
			ps = append(ps, np)
		}
		note := "已保存，等待用户在客户端确认"
		if np.Demotion {
			note += "（降级：这些文件将不再进入 soul 档，需要用户单独确认）"
		}
		out = append(out, rule.Root+":"+rule.Glob+" → "+rule.Category+" — "+note+"（"+np.ID+"）")
	}
	return out, saveProposals(home, rec.InstanceID, ps)
}

// checkItem turns an item into a rule, or says why it is refused. It also
// counts the files the rule covers that are unclassified today.
func checkItem(rec *enroll.Record, p *profile.Profile, cls *profile.Classifier, it ProposalItem) (profile.Override, int, string) {
	root, ok := rec.Roots[it.Root]
	if !ok {
		return profile.Override{}, 0, "实例没有 root " + it.Root
	}
	rel := filepath.ToSlash(filepath.Clean(it.Path))
	if it.Path == "" || filepath.IsAbs(it.Path) || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || strings.Contains(rel, "/../") {
		return profile.Override{}, 0, "路径必须是 root 内的相对路径"
	}
	full := filepath.Join(root, filepath.FromSlash(rel))
	st, err := os.Lstat(full)
	if err != nil {
		return profile.Override{}, 0, "路径不存在"
	}
	if real, err := filepath.EvalSymlinks(full); err != nil {
		return profile.Override{}, 0, "路径无法解析"
	} else if realRoot, _ := filepath.EvalSymlinks(root); real != realRoot && !strings.HasPrefix(real, realRoot+string(filepath.Separator)) {
		return profile.Override{}, 0, "路径经符号链接指向 root 之外"
	}
	glob := rel
	if st.IsDir() {
		glob = rel + "/**"
	}
	rule := profile.Override{Root: it.Root, Glob: glob, Category: it.Category}
	if err := rule.Validate(p); err != nil {
		return profile.Override{}, 0, err.Error()
	}
	unclassified := 0
	if rule.Category != "secrets" {
		secret := ""
		check := func(r string) {
			switch cls.Classify(it.Root, r).Category {
			case "secrets":
				if secret == "" {
					secret = r
				}
			case "":
				unclassified++
			}
		}
		if st.IsDir() {
			filepath.WalkDir(full, func(path string, d fs.DirEntry, err error) error {
				if err == nil && !d.IsDir() {
					r, _ := filepath.Rel(root, path)
					check(filepath.ToSlash(r))
				}
				return nil
			})
		} else {
			check(rel)
		}
		if secret != "" {
			return profile.Override{}, 0, secret + " 已按凭据规则保护，不能改为其他类别"
		}
	}
	return rule, unclassified, ""
}

// Decide applies the user's decisions: accepted rules are appended to the
// instance record's local_overrides. A demotion is accepted only from
// acceptDemotions.
func Decide(home string, d agentkit.DecideParams) (*agentkit.DecideResult, error) {
	if err := runtimeguard.CheckInstance(context.Background(), home, d.InstanceID); err != nil {
		return nil, err
	}
	rec, err := porterenroll.ReadRecord(home, d.InstanceID)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, valise.New("instance_not_enrolled", "服务器上没有接入 Valise 的实例 "+d.InstanceID, "先运行 valise enroll <服务器> --accept")
	}
	prof, err := porterenroll.ReadProfile(home, d.InstanceID)
	if err != nil {
		return nil, err
	}
	ps, err := LoadProposals(home, d.InstanceID)
	if err != nil {
		return nil, err
	}
	all := func(list []string) bool { return slices.Contains(list, "all") }
	res := &agentkit.DecideResult{Accepted: []agentkit.Proposal{}, Rejected: []string{}}
	var keep []agentkit.Proposal
	for _, pr := range ps {
		switch {
		case pr.Demotion && slices.Contains(d.AcceptDemotions, pr.ID), !pr.Demotion && (all(d.Accept) || slices.Contains(d.Accept, pr.ID)):
			if err := pr.Rule.Validate(prof); err != nil {
				return nil, valise.New("proposal_invalid", "提议 "+pr.ID+" 已不再有效："+err.Error(), "拒绝它")
			}
			rec.LocalOverrides = slices.DeleteFunc(rec.LocalOverrides, func(o profile.Override) bool { return o.Root == pr.Rule.Root && o.Glob == pr.Rule.Glob })
			rec.LocalOverrides = append(rec.LocalOverrides, pr.Rule)
			res.Accepted = append(res.Accepted, pr)
		case all(d.Reject) || slices.Contains(d.Reject, pr.ID):
			res.Rejected = append(res.Rejected, pr.ID)
		default:
			keep = append(keep, pr)
		}
	}
	if len(res.Accepted) > 0 {
		if rec.LocalOverrides == nil {
			rec.LocalOverrides = []profile.Override{}
		}
		if _, err := porterenroll.WriteRecord(home, *rec, *prof); err != nil {
			return nil, err
		}
	}
	if keep == nil {
		keep = []agentkit.Proposal{}
	}
	res.Remaining = len(keep)
	return res, saveProposals(home, d.InstanceID, keep)
}
