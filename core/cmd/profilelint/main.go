// Command profilelint checks Harness Profile files against
// docs/PROFILES.md section 3. All files given are linted as one set so child
// profiles can resolve their parents.
package main

import (
	"fmt"
	"os"

	"valise/core/profile"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "用法：profilelint <profile.yaml>...")
		os.Exit(2)
	}
	os.Exit(run(os.Args[1:]))
}

func run(paths []string) int {
	failed := 0
	var docs []*profile.Document
	for _, p := range paths {
		d, err := profile.LoadFile(p)
		if err != nil {
			fmt.Fprintf(os.Stderr, "FAIL %v\n", err)
			failed++
			continue
		}
		docs = append(docs, d)
	}
	for _, r := range profile.Lint(docs) {
		if len(r.Issues) > 0 {
			failed++
			fmt.Fprintf(os.Stderr, "FAIL %s\n", r.Doc.Path)
			for _, i := range r.Issues {
				fmt.Fprintf(os.Stderr, "  %s\n", i)
			}
			continue
		}
		fmt.Printf("OK   %s  id=%s status=%s pending=%d\n", r.Doc.Path, r.Doc.Profile.ID, r.Doc.Profile.Status, len(r.Doc.Pending))
	}
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "%d 个文件未通过\n", failed)
		return 1
	}
	return 0
}
