// Command valise-porter is the server-side porter. It is started on demand
// by the system OpenSSH and never listens on any port.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"valise/core/rpc"
	"valise/porter/internal/seal"
	"valise/porter/internal/server"
)

// version is set at release build time with -ldflags "-X main.version=…".
var version = "0.0.0-dev"

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

const usage = `用法：
  valise-porter version [--json]
  valise-porter rpc
  valise-porter vault-cat <金库内的相对路径>`

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	switch args[0] {
	case "version":
		return runVersion(args[1:], stdout, stderr)
	case "rpc":
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(stderr, "无法确定 HOME：", err)
			return 1
		}
		cfg := server.Config{Version: version, Home: home}
		if err := server.Serve(context.Background(), stdin, stdout, cfg); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "vault-cat":
		if len(args) != 2 {
			fmt.Fprintln(stderr, usage)
			return 2
		}
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		f, err := seal.OpenVaultFile(seal.Paths{Home: home}, args[1])
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		defer f.Close()
		if _, err := io.Copy(stdout, f); err != nil {
			return 1
		}
		return 0
	default:
		fmt.Fprintln(stderr, usage)
		return 2
	}
}

func runVersion(args []string, stdout, stderr io.Writer) int {
	asJSON := len(args) == 1 && args[0] == "--json"
	if len(args) > 1 || (len(args) == 1 && !asJSON) {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	if !asJSON {
		fmt.Fprintf(stdout, "valise-porter %s (protocol %s, %s/%s)\n", version, rpc.ProtocolVersion, runtime.GOOS, runtime.GOARCH)
		return 0
	}
	sum, err := selfSHA256()
	if err != nil {
		fmt.Fprintln(stderr, "无法读取自身文件：", err)
		return 1
	}
	info := rpc.VersionInfo{
		Name: "valise-porter", Version: version, Protocol: rpc.ProtocolVersion,
		OS: runtime.GOOS, Arch: runtime.GOARCH, SelfSHA256: sum,
	}
	if err := json.NewEncoder(stdout).Encode(info); err != nil {
		return 1
	}
	return 0
}

// selfSHA256 hashes the running executable so deploy can confirm the
// uploaded file is intact before it replaces the old porter.
func selfSHA256() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return "", err
	}
	f, err := os.Open(exe)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
