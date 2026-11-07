package service

import (
	"fmt"
	"strings"

	"valise/core/vault"
)

// Plain words for strict copy results (docs/ARCHITECTURE.md 7.1.1).

// CopyPlace names a place as the person knows it.
func CopyPlace(c CopyCheck) string {
	switch c.Kind {
	case KindServer:
		return "服务器"
	case KindRepo:
		return "本机仓库 " + c.Where
	}
	return c.Where
}

func firstOf(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	return "，如 " + paths[0]
}

// CopyStateText says what a place holds, with the reason or problem.
func CopyStateText(c CopyCheck) string {
	switch c.State {
	case CopyComplete:
		return fmt.Sprintf("完整：%d 个备份，文件与内容都已核对", c.Snapshots)
	case CopyListed:
		t := fmt.Sprintf("文件清单一致（%d 个备份），数据包内容未核对", c.Snapshots)
		if c.Kind == KindServer {
			t = fmt.Sprintf("对照用的文件清单（%d 个备份），数据包内容未核对", c.Snapshots)
		}
		if c.PendingBytes > 0 {
			t += "，核对要下载约 " + sizeText(c.PendingBytes)
		}
		return t
	case CopySelf:
		return fmt.Sprintf("能恢复这里列出的 %d 个备份；服务器读不到，无法确认是否还有更多备份", c.Snapshots)
	case CopyIncomplete:
		var parts []string
		if c.Reason != "" {
			parts = append(parts, c.Reason)
		}
		if n := len(c.Diff.Missing); n > 0 {
			parts = append(parts, fmt.Sprintf("缺 %d 个文件%s", n, firstOf(c.Diff.Missing)))
		}
		if n := len(c.Diff.Different); n > 0 {
			parts = append(parts, fmt.Sprintf("%d 个文件与服务器不同%s", n, firstOf(c.Diff.Different)))
		}
		if n := len(c.Diff.Extra); n > 0 {
			parts = append(parts, fmt.Sprintf("多出 %d 个服务器上没有的文件%s", n, firstOf(c.Diff.Extra)))
		}
		if len(c.Problems) > 0 {
			parts = append(parts, problemText(c.Problems))
		}
		return "不完整：" + strings.Join(parts, "；")
	case CopyUnchecked:
		return "未核对（已跳过），不算完整副本"
	case CopyDeleting:
		return "删除未完成"
	case CopyAbsent:
		return "没有这个周期"
	}
	if c.Reason != "" {
		return "未知：" + c.Reason
	}
	return "未知"
}

func problemText(ps []vault.Problem) string {
	t := ps[0].String()
	if len(ps) > 1 {
		t += fmt.Sprintf(" 等 %d 个问题", len(ps))
	}
	return t
}

// Lines describe every place of the report except the absent ones, with
// the time each was read.
func (r *CopyReport) Lines() []string {
	var out []string
	if r.NoBasis != "" {
		out = append(out, "没有服务器的文件清单可以对照（"+r.NoBasis+"）")
	} else if r.Current {
		out = append(out, "这是正在写入的周期：核对结果只代表核对这一刻")
	}
	for _, c := range r.Checks {
		if c.State == CopyAbsent && c.Kind != KindServer {
			continue
		}
		line := CopyPlace(c) + "：" + CopyStateText(c)
		if !c.CheckedAt.IsZero() && c.State != CopyUnchecked {
			line += "（" + c.CheckedAt.Local().Format("01-02 15:04") + " 核对）"
		}
		out = append(out, line)
	}
	return out
}

// CheckSummary says in one line, after a copy, whether the copied periods
// match the server's lists; pack contents are not part of it.
func CheckSummary(checks []CopyCheck) string {
	if len(checks) == 0 {
		return ""
	}
	var bad []string
	for _, c := range checks {
		if c.State != CopyListed && c.State != CopyComplete {
			bad = append(bad, CopyStateText(c))
		}
	}
	if len(bad) == 0 {
		return fmt.Sprintf("核对：%d 个周期的文件清单与服务器一致（数据包内容在“核对副本”或删除前核对）", len(checks))
	}
	return "核对发现问题：" + strings.Join(bad, "；")
}
