package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"valise/client/config"
)

// Removing and cleaning: porter, the server mark and the passes go in
// order over one admin login; the backup library and server name stay;
// this computer forgets the server but remembers it as removed.
func TestLeaveServer(t *testing.T) {
	s, _, home := addVPS(t, "")
	ctx := context.Background()
	wantCode(t, s.LeaveServer(ctx, "vps", LeaveOptions{}), "leave_unread")
	// The deploy while adding recorded the porter file's SHA-256.
	if d, ok := s.LastDeploy("vps"); !ok || len(d.SHA256) != 64 || d.Source.LocalPath == "" {
		t.Fatalf("deploy record %+v", d)
	}

	l, err := s.ServerLeftovers(ctx, "vps")
	if err != nil || !l.Porter || strings.Join(l.Passes, ",") != dailyKeyName || l.ServerName != "vps" {
		t.Fatalf("%+v %v", l, err)
	}
	if last := s.LastLeftovers("vps"); last == nil || !last.Porter {
		t.Fatal("not remembered")
	}
	var steps, doing []string
	err = s.LeaveServer(ctx, "vps", LeaveOptions{Left: l, Items: map[string]bool{LeavePorter: true, LeavePasses: true, LeaveSkills: false},
		Doing: func(item, what string) { doing = append(doing, item+":"+what) },
		Step: func(st LeaveStep) {
			if !st.Skipped {
				steps = append(steps, st.Item)
			}
		}})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(steps, ","); got != "mark,porter,passes" {
		t.Fatalf("steps %s (%s)", got, strings.Join(doing, "\n"))
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "bin", "valise-porter")); !os.IsNotExist(err) {
		t.Fatal("porter still there")
	}
	sj, _ := os.ReadFile(filepath.Join(home, ".config", "valise", "server.json"))
	if !strings.Contains(string(sj), `"removed_at"`) || !strings.Contains(string(sj), `"name": "vps"`) {
		t.Fatalf("server.json %s", sj)
	}
	ak, _ := os.ReadFile(filepath.Join(home, ".ssh", "authorized_keys"))
	if strings.Contains(string(ak), "valise-porter serve") {
		t.Fatalf("pass left:\n%s", ak)
	}
	if list, _ := config.LoadServerList(s.Dirs.Data); len(list) != 0 || s.HasDailyKey("vps") {
		t.Fatalf("still here: %+v", list)
	}
	if kh, _ := os.ReadFile(filepath.Join(s.Dirs.Data, config.KnownHostsFile)); strings.Contains(string(kh), "203.0.113.9") {
		t.Fatalf("known_hosts %s", kh)
	}
	rm, err := s.RemovedServers()
	if err != nil || len(rm) != 1 || rm[0].Name != "vps" || !rm[0].Cleaned || rm[0].ServerName != "vps" || rm[0].Server == nil || rm[0].Server.HostName != "203.0.113.9" {
		t.Fatalf("%+v %v", rm, err)
	}
}

// Only from this computer: the server keeps everything; the record lists
// what stays there, and adding it again clears the record.
func TestRemoveServerHere(t *testing.T) {
	s := newService(t, "ssh")
	if _, err := s.AddServers("box"); err != nil {
		t.Fatal(err)
	}
	left := &ServerLeftovers{Porter: true, Timers: []string{"demo"}}
	if err := s.RemoveServerHere("box", left); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.Servers(); len(list) != 0 {
		t.Fatal(list)
	}
	rm, _ := s.RemovedServers()
	if len(rm) != 1 || rm[0].Cleaned || rm[0].Left == nil || rm[0].Left.Timers[0] != "demo" {
		t.Fatalf("%+v", rm)
	}
	s.AddServers("box")
	if rm, _ := s.RemovedServers(); len(rm) != 0 {
		t.Fatalf("still marked: %+v", rm)
	}
}
