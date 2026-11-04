package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"valise/core/profile"
	"valise/ui/service"
)

func TestRepairResultSeparatesExitAndVerification(t *testing.T) {
	for _, tt := range []struct {
		state, text string
		exit        int
	}{
		{"resolved", "本次检查通过", 0},
		{"remaining", "问题仍在", 0},
		{"unknown", "无法复核", 0},
		{"remaining", "问题仍在", 7},
	} {
		t.Run(fmt.Sprintf("%s/exit%d", tt.state, tt.exit), func(t *testing.T) {
			var out bytes.Buffer
			c := &cli{out: &out}
			r := &service.ActionResult{Verification: &service.HealthVerification{State: tt.state, Text: tt.text}}
			r.Exit = tt.exit
			err := c.printActionResult(context.Background(), &profile.Action{Name: "修复服务"}, r)
			if (err != nil) != (tt.exit != 0) {
				t.Fatalf("exit %d: %v", tt.exit, err)
			}
			if !strings.Contains(out.String(), "命令：退出码") || !strings.Contains(out.String(), "该问题复核结果："+tt.text) {
				t.Fatal(out.String())
			}
		})
	}
}
