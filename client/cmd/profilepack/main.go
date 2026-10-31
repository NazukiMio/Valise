// Command profilepack builds the harness profile pack from the repository's
// profiles/ (docs/PROFILES.md 1.1). Sign the output with minisign:
//
//	go run ./cmd/profilepack -dir ../profiles -version 2026.10.31 -out valise-profiles.tar.gz
//	minisign -S -m valise-profiles.tar.gz
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"valise/client/profiles"
)

func main() {
	dir := flag.String("dir", "../profiles", "profile directory")
	version := flag.String("version", time.Now().UTC().Format("2006.01.02"), "pack version")
	out := flag.String("out", profiles.PackAsset, "output file")
	flag.Parse()
	b, err := profiles.Build(*dir, *version, time.Now())
	if err == nil {
		err = os.WriteFile(*out, b, 0o644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "profilepack:", err)
		os.Exit(1)
	}
	fmt.Printf("%s  %s  %d bytes\n", *out, *version, len(b))
}
