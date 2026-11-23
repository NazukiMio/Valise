package service

import (
	"context"
	"maps"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"valise/core/enroll"
	"valise/core/profile"
	"valise/core/revive"
	"valise/core/valise"
)

// TargetPlacementRequest asks for read-only placement suggestions on Target
// before opening or confirming a restore, migrate, or clone plan.
type TargetPlacementRequest struct {
	From               string `json:"from,omitempty"`
	Target             string `json:"target"`
	Mode               string `json:"mode"`
	ProfileID          string `json:"profile_id"`
	RecordedInstanceID string `json:"recorded_instance_id,omitempty"`
	DesiredInstanceID  string `json:"desired_instance_id,omitempty"`
}

// TargetPlacement summarizes the target server's home directory, default and
// collision-free recommended state directory, extra profile roots, and active
// occupancy read strictly over daily sessions (no passphrase or PIN required).
type TargetPlacement struct {
	TargetHome          string            `json:"target_home"`
	DefaultStateDir     string            `json:"default_state_dir"`
	RecommendedStateDir string            `json:"recommended_state_dir"`
	SourceName          string            `json:"source_name,omitempty"`
	SourceRoots         map[string]string `json:"source_roots,omitempty"`
	ExtraRootIDs        []string          `json:"extra_root_ids,omitempty"`
	DefaultOccupiedBy   []string          `json:"default_occupied_by,omitempty"`
	OccupiedPaths       map[string]string `json:"occupied_paths,omitempty"`
	CanReplaceDefault   bool              `json:"can_replace_default"`
	Relocatable         bool              `json:"relocatable"`
}

// InspectTargetPlacement inspects target (and best-effort source) using only
// daily-tier read RPCs so UI screens can compute deterministic target paths
// without triggering owner passphrase or PIN prompts.
func (s *Service) InspectTargetPlacement(ctx context.Context, req TargetPlacementRequest) (*TargetPlacement, error) {
	req.Target = strings.TrimSpace(req.Target)
	req.From = strings.TrimSpace(req.From)
	req.ProfileID = strings.TrimSpace(req.ProfileID)
	req.RecordedInstanceID = strings.TrimSpace(req.RecordedInstanceID)
	req.DesiredInstanceID = strings.TrimSpace(req.DesiredInstanceID)
	if req.Target == "" || req.ProfileID == "" {
		return nil, valise.New("request_invalid", "缺少目标服务器或 harness 资料标识", "选择目标服务器和备份后重试")
	}

	set, err := s.Profiles()
	if err != nil {
		return nil, err
	}
	if err := needProfiles(set); err != nil {
		return nil, err
	}
	lp := set.ByID[req.ProfileID]
	if lp == nil || lp.Effective == nil {
		return nil, valise.New("profile_missing", "本机没有 harness 资料 "+req.ProfileID, "请先安装或更新 harness 资料包")
	}
	prof := lp.Effective

	c, err := s.session(ctx, req.Target)
	if err != nil {
		return nil, err
	}
	defer c.Close()

	targetHome := ""
	if c.hello != nil {
		targetHome = c.hello.Home
	}
	var targetList enroll.InstanceListResult
	if err := c.Call(ctx, enroll.MethodInstanceList, struct{}{}, &targetList); err != nil {
		return nil, err
	}

	defaultDir := resolveDefaultStateDir(prof, targetHome, req.ProfileID)
	occupiedPaths := map[string]string{}
	var defaultOccupiedBy []string
	for _, in := range targetList.Instances {
		if in.Superseded != nil || in.StateMissing() {
			continue
		}
		if st := strings.TrimSpace(in.Roots[profile.StateRoot]); st != "" {
			clean := filepath.Clean(st)
			occupiedPaths[clean] = in.InstanceID
			if clean == defaultDir {
				defaultOccupiedBy = append(defaultOccupiedBy, in.InstanceID)
			}
		}
		for id, p := range in.Roots {
			if id == profile.StateRoot || strings.TrimSpace(p) == "" {
				continue
			}
			clean := filepath.Clean(p)
			if _, exists := occupiedPaths[clean]; !exists {
				occupiedPaths[clean] = in.InstanceID
			}
		}
	}
	sort.Strings(defaultOccupiedBy)

	var extraRootIDs []string
	for _, r := range prof.Roots {
		if r.ID != "" && r.ID != profile.StateRoot {
			extraRootIDs = append(extraRootIDs, r.ID)
		}
	}

	from := req.From
	if from == "" {
		from = req.Target
	}
	sourceName := ""
	var sourceRoots map[string]string
	if req.RecordedInstanceID != "" {
		var fromInsts []enroll.InstanceSummary
		if from == req.Target {
			fromInsts = targetList.Instances
		} else if list, ferr := s.Instances(ctx, from); ferr == nil {
			fromInsts = list
		}
		for _, in := range fromInsts {
			if in.InstanceID == req.RecordedInstanceID {
				sourceName = in.Name
				if len(in.Roots) > 0 {
					sourceRoots = maps.Clone(in.Roots)
				}
				break
			}
		}
	}

	canReplaceDefault := !(req.Mode == revive.ModeClone && from == req.Target)
	recommended := computeRecommendedStateDir(prof, targetHome, defaultDir, req, sourceName, occupiedPaths, canReplaceDefault)

	return &TargetPlacement{
		TargetHome:          targetHome,
		DefaultStateDir:     defaultDir,
		RecommendedStateDir: recommended,
		SourceName:          sourceName,
		SourceRoots:         sourceRoots,
		ExtraRootIDs:        extraRootIDs,
		DefaultOccupiedBy:   defaultOccupiedBy,
		OccupiedPaths:       occupiedPaths,
		CanReplaceDefault:   canReplaceDefault,
		Relocatable:         prof.Relocation != nil,
	}, nil
}

func resolveDefaultStateDir(prof *profile.Profile, targetHome, profileID string) string {
	if prof != nil && prof.Detect != nil {
		for _, cand := range prof.Detect.StateDirCandidates {
			expanded := strings.ReplaceAll(cand, "${HOME}", targetHome)
			if strings.Contains(expanded, "${") || strings.TrimSpace(expanded) == "" {
				continue
			}
			return filepath.Clean(expanded)
		}
	}
	if targetHome != "" {
		return filepath.Clean(filepath.Join(targetHome, "."+profileID))
	}
	return "." + profileID
}

func computeRecommendedStateDir(
	prof *profile.Profile,
	targetHome, defaultDir string,
	req TargetPlacementRequest,
	sourceName string,
	occupied map[string]string,
	canReplaceDefault bool,
) string {
	suffix := placementSuffix(req, sourceName)
	pattern := namedPlacementPattern(prof, targetHome)
	expand := func(s string) string {
		if pattern != "" {
			return filepath.Clean(strings.Replace(pattern, "*", s, 1))
		}
		return filepath.Clean(defaultDir + "-" + s)
	}
	taken := func(p string) bool {
		if _, ok := occupied[p]; ok {
			return true
		}
		if !canReplaceDefault && p == defaultDir {
			return true
		}
		return false
	}

	cand := expand(suffix)
	if !taken(cand) {
		return cand
	}
	for n := 2; ; n++ {
		next := expand(suffix + "-" + strconv.Itoa(n))
		if !taken(next) {
			return next
		}
	}
}

func namedPlacementPattern(prof *profile.Profile, targetHome string) string {
	if prof == nil || prof.Detect == nil {
		return ""
	}
	for _, n := range prof.Detect.Named {
		if !strings.Contains(n.Glob, "${HOME}") || strings.Count(n.Glob, "*") != 1 {
			continue
		}
		expanded := strings.ReplaceAll(n.Glob, "${HOME}", targetHome)
		if strings.Contains(expanded, "${") {
			continue
		}
		return expanded
	}
	return ""
}

func placementSuffix(req TargetPlacementRequest, sourceName string) string {
	if s := sanitizePlacementSuffix(sourceName); s != "" {
		return s
	}
	trimProfile := func(id string) string {
		id = strings.TrimSpace(id)
		if id == "" || id == req.ProfileID {
			return ""
		}
		if rest, ok := strings.CutPrefix(id, req.ProfileID+"-"); ok && rest != "" {
			return rest
		}
		return id
	}
	if req.Mode == revive.ModeClone {
		if s := sanitizePlacementSuffix(trimProfile(req.DesiredInstanceID)); s != "" {
			return s
		}
	}
	if s := sanitizePlacementSuffix(trimProfile(req.RecordedInstanceID)); s != "" {
		return s
	}
	return "copy"
}

func sanitizePlacementSuffix(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	if raw == "" {
		return ""
	}
	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_':
			b.WriteRune(r)
			lastDash = false
		case r == '-' || r == '.' || r == ' ' || r == '/':
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
		if b.Len() >= 48 {
			break
		}
	}
	return strings.Trim(b.String(), "-_")
}
