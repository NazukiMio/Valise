package main

import (
	"errors"
	"strings"
)

// Option letters of OpenSSH's ssh(1) that take an argument, and those that
// do not. Only -T, -t, -o, -p, -l and -F are understood; the rest are parsed
// so the command line splits correctly, then logged as ignored.
const (
	argOpts  = "BbcDEeFIiJLlmOoPpQRSWw"
	flagOpts = "46AaCfGgKkMNnqsTtVvXxYy"
)

// invocation is a parsed ssh command line.
type invocation struct {
	alias   string
	command string
	tty     int
	options []string // -o values
	port    string
	user    string
	config  string
	// identity is the -i key; ctl is -O (master control, e.g. exit).
	identity string
	ctl      string
	ignored  []string
}

func parseArgs(args []string) (invocation, error) {
	var inv invocation
	i := 0
	for ; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			i++
			break
		}
		if len(a) < 2 || a[0] != '-' {
			break
		}
		for j := 1; j < len(a); j++ {
			c := a[j]
			switch {
			case strings.IndexByte(flagOpts, c) >= 0:
				switch c {
				case 'T':
					inv.tty = -1
				case 't':
					if inv.tty < 0 {
						inv.tty = 0
					}
					inv.tty++
				default:
					inv.ignored = append(inv.ignored, "-"+string(c))
				}
			case strings.IndexByte(argOpts, c) >= 0:
				val := a[j+1:]
				if val == "" {
					i++
					if i >= len(args) {
						return inv, errors.New("option requires an argument -- " + string(c))
					}
					val = args[i]
				}
				switch c {
				case 'o':
					inv.options = append(inv.options, val)
				case 'p':
					inv.port = val
				case 'l':
					inv.user = val
				case 'F':
					inv.config = val
				case 'i':
					inv.identity = val
				case 'O':
					inv.ctl = val
				default:
					inv.ignored = append(inv.ignored, "-"+string(c)+" "+val)
				}
				j = len(a)
			default:
				return inv, errors.New("unknown option -- " + string(c))
			}
		}
	}
	if i >= len(args) {
		return inv, errors.New("missing destination")
	}
	dest := strings.TrimPrefix(args[i], "ssh://")
	if at := strings.LastIndexByte(dest, '@'); at >= 0 {
		inv.user, dest = dest[:at], dest[at+1:]
	}
	if host, port, ok := strings.Cut(dest, ":"); ok && strings.HasPrefix(args[i], "ssh://") {
		dest, inv.port = host, port
	}
	inv.alias = dest
	inv.command = strings.Join(args[i+1:], " ")
	return inv, nil
}
