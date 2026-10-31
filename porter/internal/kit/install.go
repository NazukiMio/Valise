package kit

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"valise/core/agentkit"
	"valise/core/valise"
	porterenroll "valise/porter/internal/enroll"
)

var skillNameRe = regexp.MustCompile(`^valise-[a-z]+$`)

// Install writes agent-kit skill files into the instance's skills_dir. An
// existing file with different content is kept as a dated backup first.
// It runs only on the user's explicit `valise kit install`.
func Install(home string, p agentkit.KitInstallParams, now time.Time) (*agentkit.KitInstallResult, error) {
	rec, err := porterenroll.ReadRecord(home, p.InstanceID)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, valise.New("instance_not_enrolled", "服务器上没有登记实例 "+p.InstanceID, "先运行 valise enroll <服务器> --accept")
	}
	prof, err := porterenroll.ReadProfile(home, p.InstanceID)
	if err != nil {
		return nil, err
	}
	if prof.AgentKit == nil || prof.AgentKit.SkillsDir == nil {
		return nil, valise.New("kit_no_skills_dir", "profile "+prof.ID+" 没有声明技能目录", "这个 harness 暂不支持安装技能；仍可以只接入 MCP")
	}
	root := rec.Roots[prof.AgentKit.SkillsDir.Root]
	if root == "" {
		return nil, valise.New("kit_no_root", "实例没有 root "+prof.AgentKit.SkillsDir.Root, "重新登记实例")
	}
	dir := filepath.Join(root, filepath.FromSlash(prof.AgentKit.SkillsDir.Path))
	res := &agentkit.KitInstallResult{Dir: dir, Written: []string{}}
	if self, err := os.Executable(); err == nil {
		res.Porter = self
	}
	for _, f := range p.Files {
		if !skillNameRe.MatchString(f.Skill) || f.Name != "SKILL.md" {
			return nil, valise.New("kit_bad_file", "技能文件名无效："+f.Skill+"/"+f.Name, "使用客户端自带的技能")
		}
		path := filepath.Join(dir, f.Skill, f.Name)
		old, err := os.ReadFile(path)
		switch {
		case err == nil && bytes.Equal(old, f.Content):
			res.Same = append(res.Same, path)
			continue
		case err == nil:
			bak := path + ".valise-bak-" + now.UTC().Format("20060102T150405Z")
			if !p.DryRun {
				if err := os.WriteFile(bak, old, 0o644); err != nil {
					return nil, err
				}
			}
			res.Backups = append(res.Backups, bak)
		}
		if !p.DryRun {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return nil, err
			}
			tmp := path + ".valise-tmp"
			if err := os.WriteFile(tmp, f.Content, 0o644); err != nil {
				return nil, err
			}
			if err := os.Rename(tmp, path); err != nil {
				return nil, err
			}
		}
		res.Written = append(res.Written, path)
	}
	return res, nil
}
