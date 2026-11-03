package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"valise/core/valise"
	"valise/ui/service"
)

// gate checks that an advanced feature may run (docs/INSTALL.md 4.2): an
// escape pod kept elsewhere that restores on its own. A stale one is shown
// and can be accepted; instance "" checks every agent on the server.
func (c *cli) gate(ctx context.Context, host, instance string, yes bool) error {
	g, err := c.svc.AdvancedGate(ctx, host, instance)
	if err != nil {
		return err
	}
	if err := g.Err(); err != nil {
		if !c.acceptRisk {
			return err
		}
		fmt.Fprintln(c.out, "⚠ 你选择在没有充足备份的情况下继续：")
		if ue, ok := valise.As(err); ok && ue.Detail != "" {
			fmt.Fprintln(c.out, "  "+strings.ReplaceAll(ue.Detail, "\n", "\n  "))
		}
		fmt.Fprintln(c.out, "  "+service.RiskWarning)
		if os.Getenv(service.RiskAcceptedEnv) != "1" && !c.confirm("我自己承担数据丢失的风险，继续这一次？") {
			return valise.New("cancelled", "已取消，服务器上没有任何改动", "先建立逃生舱再放进仓库或空间站")
		}
		c.svc.RiskAccepted = true
		return nil
	}
	if w := g.Warning(); w != "" {
		fmt.Fprintln(c.out, "⚠ "+w)
		if !yes && os.Getenv(service.StaleAcceptedEnv) != "1" && !c.confirm("知道这个风险，仍然继续？") {
			return valise.New("cancelled", "已取消，服务器上没有任何改动", "先建立逃生舱再放进仓库或空间站")
		}
	}
	return nil
}
