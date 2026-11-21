// Command profilelint checks Harness Profile files against
// docs/PROFILES.md section 3. All files given are linted as one set so child
// profiles can resolve their parents.
//
// Flags: --strict (warnings fail), --commands (audit every command),
// --classify <path> (offline classification of sample paths), --json.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"valise/core/install"
	"valise/core/profile"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

type commandReport struct {
	profile.CommandRef
	Findings []install.Finding `json:"findings,omitempty"`
}

type fileReport struct {
	Path     string                      `json:"path"`
	ID       string                      `json:"id,omitempty"`
	Status   string                      `json:"status,omitempty"`
	Pending  int                         `json:"pending"`
	OK       bool                        `json:"ok"`
	Error    string                      `json:"error,omitempty"`
	Issues   []profile.Issue             `json:"issues,omitempty"`
	Warnings []profile.Issue             `json:"warnings,omitempty"`
	Commands []commandReport             `json:"commands,omitempty"`
	Classify *profile.ClassifySimulation `json:"classify,omitempty"`
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("profilelint", flag.ContinueOnError)
	fs.SetOutput(stderr)
	strict := fs.Bool("strict", false, "警告也算失败")
	cmds := fs.Bool("commands", false, "打印全部命令审计")
	classify := fs.String("classify", "", "对样本路径（文件或目录）做离线分类模拟")
	asJSON := fs.Bool("json", false, "输出 JSON 报告")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	paths := fs.Args()
	if len(paths) == 0 {
		fmt.Fprintln(stderr, "用法：profilelint [--strict] [--commands] [--classify <path>] [--json] <profile.yaml>...")
		return 2
	}
	human := stdout
	if *asJSON {
		human = io.Discard
	}
	failed := 0
	var reports []fileReport
	var docs []*profile.Document
	for _, p := range paths {
		d, err := profile.LoadFile(p)
		if err != nil {
			fmt.Fprintf(stderr, "FAIL %v\n", err)
			reports = append(reports, fileReport{Path: p, Error: err.Error()})
			failed++
			continue
		}
		docs = append(docs, d)
	}
	var effs []*profile.Profile
	for _, r := range profile.Lint(docs) {
		rep := fileReport{Path: r.Doc.Path, ID: r.Doc.Profile.ID, Status: r.Doc.Profile.Status,
			Pending: len(r.Doc.Pending), Issues: r.Issues, Warnings: r.Warnings}
		bad := len(r.Issues) > 0 || (*strict && len(r.Warnings) > 0)
		rep.OK = !bad
		if r.Effective != nil && len(r.Issues) == 0 {
			effs = append(effs, r.Effective)
		} else {
			effs = append(effs, nil)
		}
		if bad {
			failed++
			fmt.Fprintf(stderr, "FAIL %s\n", r.Doc.Path)
			for _, i := range r.Issues {
				fmt.Fprintf(stderr, "  %s\n", i)
			}
		} else {
			fmt.Fprintf(human, "OK   %s  id=%s status=%s pending=%d\n", r.Doc.Path, rep.ID, rep.Status, rep.Pending)
		}
		for _, w := range r.Warnings {
			w := w
			if bad {
				fmt.Fprintf(stderr, "  WARN [%s] %s\n", w.Code, w)
			} else {
				fmt.Fprintf(human, "  WARN [%s] %s\n", w.Code, w)
			}
		}
		if *cmds && r.Effective != nil {
			for _, c := range profile.Commands(r.Effective) {
				cr := commandReport{CommandRef: c}
				if len(c.Argv) > 0 {
					cr.Findings = install.CheckCommand(c.Argv, "")
				}
				rep.Commands = append(rep.Commands, cr)
				printCommand(human, cr)
			}
		}
		reports = append(reports, rep)
	}
	if *classify != "" {
		if code := runClassify(*classify, docs, effs, failed, human, stderr, reports); code != 0 {
			return code
		}
	}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(reports); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
	}
	if failed > 0 {
		fmt.Fprintf(stderr, "%d 个文件未通过\n", failed)
		return 1
	}
	return 0
}

func runClassify(path string, docs []*profile.Document, effs []*profile.Profile, failed int, out, stderr io.Writer, reports []fileReport) int {
	child := -1 // index of the profile to simulate
	switch {
	case len(docs) == 1:
		child = 0
	case len(docs) == 2 && docs[1].Profile.Extends == docs[0].Profile.ID:
		child = 1
	case len(docs) == 2 && docs[0].Profile.Extends == docs[1].Profile.ID:
		child = 0
	}
	var eff *profile.Profile
	if child >= 0 && child < len(effs) {
		eff = effs[child]
	}
	if failed > 0 || eff == nil {
		fmt.Fprintln(stderr, "--classify 需要一个通过检查的 profile（或「父 profile 加子 profile」）")
		return 2
	}
	samples, err := profile.LoadSampleEntries(path)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	sim := profile.SimulateClassify(eff, samples)
	for i := range reports {
		if reports[i].Path == docs[child].Path {
			reports[i].Classify = &sim
		}
	}
	fmt.Fprintf(out, "分类模拟：共 %d 个样本\n", sim.Total)
	for _, cat := range append(append([]string{}, profile.Categories...), profile.Unclassified) {
		if n := sim.Counts[cat]; n > 0 {
			fmt.Fprintf(out, "  %-12s %d\n", cat, n)
		}
	}
	for i, u := range sim.Unclassified {
		if i == 20 {
			fmt.Fprintf(out, "  …另有 %d 个未分类\n", len(sim.Unclassified)-20)
			break
		}
		fmt.Fprintf(out, "  未分类 %s:%s\n", u.Root, u.Path)
	}
	for _, u := range sim.UnusedRules {
		fmt.Fprintf(out, "  未命中规则 %s\n", u)
	}
	return 0
}

func printCommand(w io.Writer, c commandReport) {
	fmt.Fprintf(w, "  CMD  [%s] %s (%s): %s\n", c.Tier, c.Label, c.Field, strings.Join(c.Argv, " "))
	if c.URL != "" {
		fmt.Fprintf(w, "       url=%s sha256=%s\n", c.URL, c.SHA256)
	}
	if c.Note != "" {
		fmt.Fprintf(w, "       note=%s\n", c.Note)
	}
	if len(c.Argv) == 0 {
		return
	}
	if len(c.Findings) == 0 {
		fmt.Fprintf(w, "       %s\n", install.NoFindings)
		return
	}
	for _, f := range c.Findings {
		mark := ""
		if f.Refuse {
			mark = "[REFUSE] "
		}
		fmt.Fprintf(w, "       %s%s: %s\n", mark, f.Rule, f.Reason)
	}
}
