package main

import (
	"context"
	"flag"
	"fmt"

	"sort"
	"strings"

	"valise/client/profiles"
	"valise/core/install"
	"valise/core/profile"
	"valise/core/valise"
	"valise/ui/service"
)

// profilesCmd manages the harness profile pack (docs/PROFILES.md 1.1).
func (c *cli) profilesCmd(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errUsage
	}
	switch args[0] {
	case "status":
		st, err := c.svc.ProfilesStatus()
		if err != nil {
			return err
		}
		switch {
		case st.Pack != nil:
			fmt.Fprintf(c.out, "harness 资料包：%s（已验签）\n", st.Pack.Version)
			for _, p := range st.Pack.Profiles {
				fmt.Fprintf(c.out, "  %s v%d\n", p.ID, p.ProfileVersion)
			}
		case st.PackProblem != "":
			fmt.Fprintf(c.out, "harness 资料包：不可用（%s）\n建议：重新安装：valise profiles install\n", st.PackProblem)
		default:
			fmt.Fprintln(c.out, "harness 资料包：未安装。安装：valise profiles install")
		}
		for _, id := range st.Local {
			fmt.Fprintf(c.out, "本地 harness 资料（未经审核）：%s\n", id)
		}
		for _, r := range st.Rejected {
			fmt.Fprintf(c.out, "已忽略的本地 harness 资料：%s\n", r)
		}
		return nil
	case "install":
		fs := flag.NewFlagSet("profiles install", flag.ContinueOnError)
		file := fs.String("file", "", "")
		tag := fs.String("tag", "", "")
		yes := fs.Bool("yes", false, "")
		if _, err := parseArgs(fs, args[1:], 0); err != nil {
			return err
		}
		var f *service.PackFile
		var err error
		if *file != "" {
			f, err = c.svc.ReadPackFile(*file)
		} else {
			fmt.Fprintln(c.out, "正在从 GitHub Release 下载 harness 资料包…")
			f, err = c.svc.FetchPack(ctx, *tag)
		}
		if err != nil {
			return err
		}
		fmt.Fprintf(c.out, "资料包 %s（来自 %s）包含：\n", f.Pack.Manifest.Version, f.From)
		for _, p := range f.Pack.Manifest.Profiles {
			fmt.Fprintf(c.out, "  %s v%d\n", p.ID, p.ProfileVersion)
		}
		if f.SignatureErr == nil {
			if err := c.svc.InstallPack(f); err != nil {
				return err
			}
			fmt.Fprintln(c.out, "签名校验通过，已安装。")
			return nil
		}
		c.printProblem(f.SignatureErr)
		fmt.Fprintln(c.out, "\n这个文件不能作为官方资料包安装。可以把其中的 harness 资料作为\"本地 harness 资料\"使用，它们未经审核。")
		fmt.Fprintln(c.out, "它们可能让 Valise 在你的服务器上运行以下命令：")
		for _, d := range f.Pack.Docs {
			for _, cmd := range profiles.Commands(d.Profile) {
				fmt.Fprintf(c.out, "  [%s] %s\n", d.Profile.ID, cmd)
			}
		}
		if !*yes && !c.confirm("作为本地 harness 资料安装？") {
			return nil
		}
		paths, err := c.svc.InstallPackAsLocal(f)
		if err != nil {
			return err
		}
		for _, p := range paths {
			fmt.Fprintf(c.out, "已写入 %s\n", p)
		}
		return nil
	case "lint":
		return c.profilesLintCmd(args[1:])
	}
	return errUsage
}

type profileCommandReport struct {
	Field    string   `json:"field"`
	Label    string   `json:"label"`
	Tier     string   `json:"tier,omitempty"`
	Argv     []string `json:"argv,omitempty"`
	URL      string   `json:"url,omitempty"`
	SHA256   string   `json:"sha256,omitempty"`
	Findings []string `json:"findings,omitempty"`
	Refused  bool     `json:"refused,omitempty"`
}

type profileLintReport struct {
	File     string                      `json:"file"`
	ID       string                      `json:"id,omitempty"`
	Status   string                      `json:"status,omitempty"`
	Pending  int                         `json:"pending"`
	OK       bool                        `json:"ok"`
	Issues   []string                    `json:"issues,omitempty"`
	Warnings []profile.Issue             `json:"warnings,omitempty"`
	Commands []profileCommandReport      `json:"commands,omitempty"`
	Classify *profile.ClassifySimulation `json:"classify,omitempty"`
}

func commandReports(eff *profile.Profile) []profileCommandReport {
	var out []profileCommandReport
	for _, cmd := range profile.Commands(eff) {
		r := profileCommandReport{Field: cmd.Field, Label: cmd.Label, Tier: cmd.Tier, Argv: cmd.Argv, URL: cmd.URL, SHA256: cmd.SHA256}
		if len(cmd.Argv) > 0 {
			for _, f := range install.CheckCommand(cmd.Argv, "") {
				r.Findings = append(r.Findings, f.Reason)
				if f.Refuse {
					r.Refused = true
				}
			}
		}
		out = append(out, r)
	}
	return out
}

func (c *cli) profilesLintCmd(args []string) error {
	fs := flag.NewFlagSet("profiles lint", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "")
	strict := fs.Bool("strict", false, "")
	showCmds := fs.Bool("commands", false, "")
	classify := fs.String("classify", "", "")
	paths, err := parseArgsRange(fs, args, 1, 1024)
	if err != nil {
		return err
	}
	failed := 0
	var reports []profileLintReport
	var docs []*profile.Document
	for _, p := range paths {
		d, err := profile.LoadFile(p)
		if err != nil {
			failed++
			reports = append(reports, profileLintReport{File: p, OK: false, Issues: []string{err.Error()}})
			if !*asJSON {
				fmt.Fprintf(c.errOut, "FAIL %v\n", err)
			}
			continue
		}
		docs = append(docs, d)
	}
	results := profile.Lint(docs)
	var sim *profile.ClassifySimulation
	simDoc := ""
	if *classify != "" && len(results) > 0 {
		var err error
		if sim, simDoc, err = classifyLeaf(results, *classify); err != nil {
			return err
		}
	}
	for _, r := range results {
		rep := profileLintReport{
			File:     r.Doc.Path,
			ID:       r.Doc.Profile.ID,
			Status:   r.Doc.Profile.Status,
			Pending:  len(r.Doc.Pending),
			OK:       len(r.Issues) == 0 && !(*strict && len(r.Warnings) > 0),
			Warnings: r.Warnings,
		}
		if *showCmds && r.Effective != nil {
			rep.Commands = commandReports(r.Effective)
		}
		if sim != nil && r.Doc.Path == simDoc {
			rep.Classify = sim
		}
		switch {
		case len(r.Issues) > 0:
			failed++
			if !*asJSON {
				fmt.Fprintf(c.errOut, "FAIL %s\n", r.Doc.Path)
			}
			for _, iss := range r.Issues {
				s := iss.String()
				rep.Issues = append(rep.Issues, s)
				if !*asJSON {
					fmt.Fprintf(c.errOut, "  %s\n", s)
				}
			}
		case !rep.OK:
			failed++
			if !*asJSON {
				fmt.Fprintf(c.errOut, "FAIL %s（--strict：存在 %d 条提示）\n", r.Doc.Path, len(r.Warnings))
			}
		default:
			if !*asJSON {
				fmt.Fprintf(c.out, "OK   %s  id=%s status=%s pending=%d\n", r.Doc.Path, r.Doc.Profile.ID, r.Doc.Profile.Status, len(r.Doc.Pending))
			}
		}
		if !*asJSON {
			for _, w := range r.Warnings {
				fmt.Fprintf(c.errOut, "  WARN %s\n", w.String())
			}
			c.printLintCommands(rep.Commands)
			if rep.Classify != nil {
				c.printClassify(rep.Classify)
			}
		}
		reports = append(reports, rep)
	}
	if *asJSON {
		if err := c.printJSON(reports); err != nil {
			return err
		}
	}
	if failed > 0 {
		return valise.New("profile_lint_failed", fmt.Sprintf("%d 个 profile 文件未通过校验", failed),
			"按上面的字段提示修正后重新运行 valise profiles lint")
	}
	return nil
}

// classifyLeaf simulates classification on the one profile no other given
// profile extends, so parent and child may be passed in any order.
func classifyLeaf(results []profile.Result, samplesPath string) (*profile.ClassifySimulation, string, error) {
	extended := map[string]bool{}
	for _, r := range results {
		if e := r.Doc.Profile.Extends; e != "" {
			extended[e] = true
		}
	}
	var leaf *profile.Result
	for i, r := range results {
		if extended[r.Doc.Profile.ID] {
			continue
		}
		if leaf != nil {
			return nil, "", valise.New("profile_classify_ambiguous", "--classify 一次只能模拟一份 profile", "只传入一份文件，或一对父子文件")
		}
		leaf = &results[i]
	}
	if leaf == nil || leaf.Effective == nil || len(leaf.Issues) > 0 {
		return nil, "", nil
	}
	samples, err := profile.LoadSampleEntries(samplesPath)
	if err != nil {
		return nil, "", valise.New("profile_classify_samples", "无法读取分类样本："+samplesPath, "样本可以是目录，或每行 <root>:<相对路径> 的文本文件").WithDetail(err.Error())
	}
	sim := profile.SimulateClassify(leaf.Effective, samples)
	return &sim, leaf.Doc.Path, nil
}

func (c *cli) printLintCommands(cmds []profileCommandReport) {
	for _, cmd := range cmds {
		text := strings.Join(cmd.Argv, " ")
		if cmd.URL != "" {
			text = cmd.URL + " -> " + text
		}
		verdict := install.NoFindings
		if len(cmd.Findings) > 0 {
			verdict = strings.Join(cmd.Findings, "；")
		}
		if cmd.Refused {
			verdict = "[REFUSE] " + verdict
		}
		fmt.Fprintf(c.out, "  CMD  %s [%s]: %s  =>  %s\n", cmd.Label, cmd.Field, text, verdict)
	}
}

func (c *cli) printClassify(sim *profile.ClassifySimulation) {
	fmt.Fprintf(c.out, "  分类模拟：共 %d 项，未分类 %d 项\n", sim.Total, len(sim.Unclassified))
	cats := make([]string, 0, len(sim.Counts))
	for k := range sim.Counts {
		cats = append(cats, k)
	}
	sort.Strings(cats)
	for _, k := range cats {
		fmt.Fprintf(c.out, "    %s: %d\n", k, sim.Counts[k])
	}
	for _, u := range sim.UnusedRules {
		fmt.Fprintf(c.out, "    未命中的规则：%s\n", u)
	}
}
