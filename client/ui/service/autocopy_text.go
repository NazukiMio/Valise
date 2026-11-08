package service

import (
	"fmt"
	"strings"
)

// AutoCopyLines words a result the same way in every front end. With all
// false, locations with nothing to report (current, off) are left out.
func AutoCopyLines(r *AutoCopyResult, all bool) []string {
	var out []string
	for _, o := range r.Locations {
		name := "仓库 " + o.Location
		if o.Kind == "cloud" {
			name = "云盘 " + o.Location + "（这台电脑登录）"
		}
		var line string
		switch o.State {
		case CopyCopied:
			if o.Kind == "cloud" {
				line = name + "：已同步，" + cloudPathText(o.Path)
			} else if o.Copied > 0 {
				line = fmt.Sprintf("%s：补齐 %d 个新备份", name, o.Copied)
			} else {
				line = name + "：补上了缺少的文件"
			}
		case CopyCurrent:
			if all {
				line = name + "：已是最新"
			}
		case CopyOff:
			if all {
				line = name + "：按本客户端的设置这次不补"
			}
		case CopyUnmounted, CopyBusy, CopyFailed:
			line = name + "：" + map[string]string{CopyUnmounted: "没有补（文件夹不在）", CopyBusy: "没有补", CopyFailed: "失败"}[o.State] + "，" + o.Reason
			if o.Suggestion != "" {
				line += "；" + o.Suggestion
			}
		}
		if line != "" {
			out = append(out, line)
		}
	}
	if len(r.CertsRenewed) > 0 {
		out = append(out, "到远程备份位置 "+strings.Join(r.CertsRenewed, "、")+" 的证书已续期")
	}
	if len(r.CertsExpiring) > 0 {
		out = append(out, "到远程备份位置 "+strings.Join(r.CertsExpiring, "、")+" 的证书快到期了：同步一次即可续期（自动同步才不会中断）")
	}
	out = append(out, r.LostSource...)
	if r.CertError != "" {
		out = append(out, "证书续期失败："+r.CertError)
	}
	return out
}

func cloudPathText(path string) string {
	switch path {
	case PathDirect:
		return "由服务器直传"
	case PathRepo:
		return "经本机仓库中转"
	case PathTemp:
		return "经这台电脑的临时文件夹中转"
	}
	return "方式未知"
}
