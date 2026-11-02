package main

import (
	"context"
	"fmt"

	"valise/core/valise"
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
		return err
	}
	if w := g.Warning(); w != "" {
		fmt.Fprintln(c.out, "⚠ "+w)
		if !yes && !c.confirm("知道这个风险，仍然继续？") {
			return valise.New("cancelled", "已取消，服务器上没有任何改动", "先建立逃生舱再放进仓库或空间站")
		}
	}
	return nil
}
