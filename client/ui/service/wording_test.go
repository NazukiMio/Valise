package service

import "testing"

func TestPlainWording(t *testing.T) {
	for in, want := range map[string]string{
		"定时建立逃生舱":                   "定时备份",
		"在空间站设置里重新装一次钥匙":            "在远程备份位置设置里重新装一次钥匙",
		"有未完成的着陆操作，先处理后再修改配置":       "有未完成的恢复操作，先处理后再修改配置",
		"先建立逃生舱再放进仓库或空间站":           "先建立备份再放进备份位置",
		"从执行前建立的逃生舱原地着陆":            "从执行前建立的备份恢复当前实例",
		"upgrade before the change": "upgrade before the change",
	} {
		if got := PlainWording(in); got != want {
			t.Errorf("PlainWording(%q) = %q, want %q", in, got, want)
		}
	}
}
