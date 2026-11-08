package main

import (
	"context"
	"fmt"
	"io"

	"valise/core/valise"
	"valise/ui/service"
)

// copyAfterBackup fills the locations this computer copies to, in the
// foreground (docs/ARCHITECTURE.md 7.4): nothing continues after the
// command exits.
func (c *cli) copyAfterBackup(ctx context.Context, w io.Writer, host, instance string, skip bool) {
	if len(c.svc.Repos()) == 0 && len(c.svc.Config.Clouds) == 0 {
		return
	}
	if skip {
		fmt.Fprintln(w, "本机仓库与这台电脑登录的云盘未更新（--no-copy）；之后运行 valise repo sync 或打开 Valise 时补齐")
		return
	}
	fmt.Fprintln(w, "正在补到这台电脑负责的位置（只在这个命令运行期间进行）…")
	res, err := c.svc.AutoCopy(c.withProgress(ctx), host, service.TriggerBackup, service.AutoCopyOptions{Instance: instance,
		Progress: func(loc string) { fmt.Fprintf(c.errOut, "[Valise] 补到 %s\n", loc) }})
	if err != nil {
		reason := err.Error()
		if ue, ok := valise.As(err); ok {
			reason = ue.Reason + "；" + ue.Suggestion
		}
		fmt.Fprintln(w, "自动补齐失败："+reason)
		return
	}
	lines := service.AutoCopyLines(res, true)
	for _, l := range lines {
		fmt.Fprintln(w, "  "+l)
	}
}
