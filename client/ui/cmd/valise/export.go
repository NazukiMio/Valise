package main

import (
	"context"
	"flag"
	"fmt"

	"valise/ui/service"
)

// export decrypts one backup into a local folder (docs/ARCHITECTURE.md
// 8.2.3): valise export <服务器> <备份> --to <文件夹> [--source …]
// [--include-secrets].
func (c *cli) export(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	var o service.ExportOptions
	fs.StringVar(&o.To, "to", "", "")
	fs.StringVar(&o.Source, "source", "", "")
	fs.BoolVar(&o.IncludeSecrets, "include-secrets", false, "")
	asJSON := fs.Bool("json", false, "")
	pos, err := parseArgs(fs, args, 2)
	if err != nil || o.To == "" {
		return errUsage
	}
	o.Host, o.SnapshotID = pos[0], pos[1]
	res, err := c.svc.Export(c.withProgress(ctx), o)
	if err != nil {
		return err
	}
	if *asJSON {
		return c.printJSON(res)
	}
	fmt.Fprintf(c.out, "已把备份 %s 导出到 %s（读取自 %s）\n", o.SnapshotID, res.To, res.From)
	fmt.Fprintf(c.out, "  %d 个文件，%s，每个都已按 SHA-256 核对；没有安装或启动任何程序\n", res.Files, size(res.Bytes))
	if l := transferLine(res.Transfer); l != "" {
		fmt.Fprintln(c.out, "  "+l)
	}
	switch {
	case len(res.Secrets) > 0 && o.IncludeSecrets:
		fmt.Fprintf(c.out, "  包含 %d 个凭据文件，请妥善保管\n", len(res.Secrets))
	case len(res.Secrets) > 0:
		fmt.Fprintf(c.out, "  %d 个凭据文件没有导出（需要时加 --include-secrets 导出到新文件夹）\n", len(res.Secrets))
	}
	if len(res.LinksKept) > 0 {
		fmt.Fprintf(c.out, "  %d 个符号链接指向导出文件夹之外，没有重建，列在说明里\n", len(res.LinksKept))
	}
	fmt.Fprintf(c.out, "  说明：%s\n", res.NotesFile)
	return nil
}
