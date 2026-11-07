package service

import "strings"

// oldWording maps the themed words earlier Valise versions wrote into
// records and porter messages to the plain ones (PRODUCT.md 8.1). Records
// already on servers are never rewritten; text is mapped only when shown.
// Longer phrases come first so a generic word does not split them.
var oldWording = strings.NewReplacer(
	"我给你装了一个逃生舱，你学习一下", "我给你装了 Valise 保护技能，你学习一下",
	"逃生舱技能", "Agent 保护技能",
	"逃生舱着陆", "恢复",
	"原地着陆", "恢复当前实例",
	"着陆到另一台服务器", "迁移到另一台服务器",
	"着陆成一个新的 agent", "创建副本（新的 agent）",
	"着陆", "恢复",
	"本机仓库或空间站", "备份位置",
	"仓库与空间站", "备份位置",
	"仓库和空间站", "备份位置",
	"仓库或空间站", "备份位置",
	"空间站", "远程备份位置",
	"上云", "同步",
	"定时建立逃生舱", "定时备份",
	"不建逃生舱", "不建立备份",
	"建逃生舱", "建立备份",
	"逃生舱", "备份",
)

// PlainWording rewrites known old themed words in text read from records
// or an older porter; anything else passes through unchanged.
func PlainWording(s string) string { return oldWording.Replace(s) }
