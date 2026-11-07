package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"valise/ui/service"
)

// Long operations report their stage and bytes on stderr (review F03), so
// a wait always says what it is waiting for.

var stageLabels = map[string]string{
	service.StagePull:      "复制整个周期到这台电脑（已有的文件不再下载）",
	service.StagePullMeta:  "取回周期的索引与清单（很小）",
	service.StagePullPacks: "只取回这个备份用到的数据包（已有的核对 SHA-256 后复用）",
	service.StageOpen:      "在这台电脑上解密", service.StageInspect: "检查目标服务器",
	service.StageStage: "写入服务器的暂存目录", service.StageVerify: "核对暂存的文件",
	service.StageSwap: "停止服务 → 交换 → 启动 → 健康检查", service.TestLocal: "在这台电脑上解开并核对",
	service.StageSealScan: "清点文件", service.StageSealPack: "读取、加密并写入", service.StageSealCommit: "写入备份目录",
}

// withProgress prints each stage once, then its amount at most every two
// seconds.
func (c *cli) withProgress(ctx context.Context) context.Context {
	var mu sync.Mutex
	stage, last := "", time.Time{}
	return service.WithProgress(ctx, func(p service.Progress) {
		mu.Lock()
		defer mu.Unlock()
		if p.Stage != stage {
			stage, last = p.Stage, time.Time{}
			label := stageLabels[p.Stage]
			if label == "" {
				label = p.Stage
			}
			fmt.Fprintf(c.errOut, "[Valise] %s", label)
			if p.Total > 0 {
				fmt.Fprintf(c.errOut, "：共 %s", size(p.Total))
			}
			fmt.Fprintln(c.errOut)
		}
		if p.Total > 0 && time.Since(last) > 2*time.Second {
			last = time.Now()
			fmt.Fprintf(c.errOut, "  %s / %s（%d%%）\n", size(min(p.Done, p.Total)), size(p.Total), min(p.Done, p.Total)*100/p.Total)
		}
	})
}

// transferLine says what pulling one pod took (review F03).
func transferLine(t *service.SnapshotPull) string { return t.Summary() }
