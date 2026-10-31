package kit

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"time"

	"valise/core/agentkit"
	"valise/core/valise"
	"valise/core/vaultrpc"
	porterenroll "valise/porter/internal/enroll"
	"valise/porter/internal/seal"
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

// skillsDir resolves the instance's skills directory ("" when the profile
// declares none).
func skillsDir(home, instance string) (string, error) {
	rec, err := porterenroll.ReadRecord(home, instance)
	if err != nil {
		return "", err
	}
	if rec == nil {
		return "", valise.New("instance_not_enrolled", "服务器上没有登记实例 "+instance, "先运行 valise enroll <服务器> --accept")
	}
	prof, err := porterenroll.ReadProfile(home, instance)
	if err != nil {
		return "", err
	}
	if prof.AgentKit == nil || prof.AgentKit.SkillsDir == nil || rec.Roots[prof.AgentKit.SkillsDir.Root] == "" {
		return "", nil
	}
	return filepath.Join(rec.Roots[prof.AgentKit.SkillsDir.Root], filepath.FromSlash(prof.AgentKit.SkillsDir.Path)), nil
}

func backups(dir string) []string {
	m, _ := filepath.Glob(filepath.Join(dir, "SKILL.md.valise-bak-*"))
	return m
}

// Status compares the installed skills with the shipped ones and reports
// the last agent call through MCP (from the audit log).
func Status(home string, p agentkit.KitStatusParams) (*agentkit.KitStatusResult, error) {
	dir, err := skillsDir(home, p.InstanceID)
	if err != nil {
		return nil, err
	}
	res := &agentkit.KitStatusResult{Dir: dir, Skills: []agentkit.SkillState{}}
	if dir != "" {
		for _, f := range p.Files {
			if !skillNameRe.MatchString(f.Skill) || f.Name != "SKILL.md" {
				return nil, valise.New("kit_bad_file", "技能文件名无效："+f.Skill+"/"+f.Name, "使用客户端自带的技能")
			}
			path := filepath.Join(dir, f.Skill, f.Name)
			st := agentkit.SkillState{Skill: f.Skill, Path: path, State: agentkit.SkillMissing, Backups: len(backups(filepath.Dir(path)))}
			if b, err := os.ReadFile(path); err == nil {
				st.State = agentkit.SkillModified
				if bytes.Equal(b, f.Content) {
					st.State = agentkit.SkillCurrent
				}
			}
			res.Skills = append(res.Skills, st)
		}
	}
	if f, err := os.Open(seal.Paths{Home: home}.Audit()); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			var e seal.AuditEntry
			if json.Unmarshal(sc.Bytes(), &e) == nil && e.Instance == p.InstanceID && e.Initiator == vaultrpc.InitiatorAgentMCP {
				res.MCPCalls++
				t := e.Time
				res.LastMCP = &t
			}
		}
	}
	return res, nil
}

// Uninstall removes installed skills: SKILL.md files equal to the shipped
// content, Valise's backups, and skill directories left empty. Modified
// files are kept unless Force. It runs only on the user's explicit
// `valise kit uninstall`.
func Uninstall(home string, p agentkit.KitUninstallParams) (*agentkit.KitUninstallResult, error) {
	dir, err := skillsDir(home, p.InstanceID)
	if err != nil {
		return nil, err
	}
	res := &agentkit.KitUninstallResult{Removed: []string{}}
	if dir == "" {
		return res, nil
	}
	remove := func(path string) error {
		if !p.DryRun {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		res.Removed = append(res.Removed, path)
		return nil
	}
	for _, f := range p.Files {
		if !skillNameRe.MatchString(f.Skill) || f.Name != "SKILL.md" {
			return nil, valise.New("kit_bad_file", "技能文件名无效："+f.Skill+"/"+f.Name, "使用客户端自带的技能")
		}
		sdir := filepath.Join(dir, f.Skill)
		path := filepath.Join(sdir, f.Name)
		if b, err := os.ReadFile(path); err == nil {
			if !bytes.Equal(b, f.Content) && !p.Force {
				res.Kept = append(res.Kept, path)
			} else if err := remove(path); err != nil {
				return res, err
			}
		}
		for _, bak := range backups(sdir) {
			if err := remove(bak); err != nil {
				return res, err
			}
		}
		if ents, err := os.ReadDir(sdir); err == nil && !p.DryRun && len(ents) == 0 {
			os.Remove(sdir)
		}
	}
	return res, nil
}
