package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"valise/ui/service"
)

func TestReviveUnverifiedSourceDecision(t *testing.T) {
	withBackup := &service.UnverifiedSource{Notice: service.UnverifiedSourceNotice, InstanceID: "demo", BackupFirst: true, BackupInstance: "demo"}
	newCopy := &service.UnverifiedSource{Notice: service.UnverifiedSourceNotice, InstanceID: "demo"}
	for _, tc := range []struct {
		name        string
		u           *service.UnverifiedSource
		stdin       string
		yes, backup bool
		want        service.SourceDecision
	}{
		{"continue", withBackup, "1\n", false, false, service.SourceProceed},
		{"backup first", withBackup, "2\n", false, false, service.SourceBackupFirst},
		{"backup not offered", newCopy, "2\n", false, false, service.SourceCancel},
		{"empty answer cancels", withBackup, "\n", false, false, service.SourceCancel},
		{"no answer cancels", withBackup, "", false, false, service.SourceCancel},
		{"yes stands for continue", withBackup, "", true, false, service.SourceProceed},
		{"backup flag", withBackup, "", true, true, service.SourceBackupFirst},
	} {
		var errOut bytes.Buffer
		c := &cli{in: strings.NewReader(tc.stdin), out: &bytes.Buffer{}, errOut: &errOut}
		if got := c.sourceDecision(tc.u, tc.yes, tc.backup); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
		asked := strings.Contains(errOut.String(), "我清楚，继续")
		if asked == (tc.yes || tc.backup) {
			t.Errorf("%s: prompt shown=%v", tc.name, asked)
		}
		if strings.Contains(errOut.String(), "full 备份") != (asked && tc.u.BackupFirst) {
			t.Errorf("%s: backup option shown wrongly:\n%s", tc.name, errOut.String())
		}
	}
}

func TestRevivePlanShowsUnverifiedSource(t *testing.T) {
	var out bytes.Buffer
	c := &cli{out: &out}
	rp := &service.RevivePlan{Target: "box", From: "old", UnverifiedSource: &service.UnverifiedSource{Notice: service.UnverifiedSourceNotice,
		InstanceID: "demo", Name: "work", ProfileID: "demo", ProfileVersion: 3, SourceServer: "old", Period: "2026-10-01_2026-11-01",
		CreatedAt: time.Date(2026, 10, 3, 4, 5, 0, 0, time.UTC), Tier: "full"}}
	printPlan(c, rp)
	for _, want := range []string{"来源无法核实", "demo（命名实例 work）", "demo v3", "old，周期 2026-10-01_2026-11-01", "2026-10-0", "目标上没有同一实例"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("missing %q:\n%s", want, out.String())
		}
	}
}
