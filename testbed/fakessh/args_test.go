package main

import (
	"reflect"
	"testing"
)

func TestParseArgs(t *testing.T) {
	cases := []struct {
		args []string
		want invocation
	}{
		{[]string{"-T", "host", "echo", "hi"}, invocation{alias: "host", command: "echo hi", tty: -1}},
		{[]string{"-tt", "-p", "2222", "-l", "bob", "h", "x"}, invocation{alias: "h", command: "x", tty: 2, port: "2222", user: "bob"}},
		{[]string{"-o", "ControlMaster=auto", "-oControlPersist=60", "-F", "/cfg", "--", "h", "cmd"},
			invocation{alias: "h", command: "cmd", options: []string{"ControlMaster=auto", "ControlPersist=60"}, config: "/cfg"}},
		{[]string{"-v", "-i", "/k", "-J", "jump", "alice@h"}, invocation{alias: "h", user: "alice", ignored: []string{"-v", "-i /k", "-J jump"}}},
		{[]string{"-Tq", "h", "a b"}, invocation{alias: "h", command: "a b", tty: -1, ignored: []string{"-q"}}},
		{[]string{"ssh://u@h:2200", "c"}, invocation{alias: "h", user: "u", port: "2200", command: "c"}},
	}
	for _, c := range cases {
		got, err := parseArgs(c.args)
		if err != nil {
			t.Fatalf("%q: %v", c.args, err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q:\n got %+v\nwant %+v", c.args, got, c.want)
		}
	}
	for _, bad := range [][]string{{}, {"-o"}, {"-T"}, {"-Z", "h"}} {
		if _, err := parseArgs(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
