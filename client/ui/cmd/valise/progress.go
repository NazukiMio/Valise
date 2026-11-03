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
	service.StagePull: "取回逃生舱所在的整个周期到这台电脑（逃生舱按周期打包存放，只要一个也要先取回这个周期；已有的块不再下载）",
	service.StageOpen: "在这台电脑上解密", service.StageInspect: "检查目标服务器",
	service.StageStage: "写入服务器的暂存目录", service.StageVerify: "核对暂存的文件",
	service.StageSwap: "停止服务 → 交换 → 启动 → 健康检查", service.TestLocal: "在这台电脑上解开并核对",
	service.StageSealScan: "清点文件", service.StageSealPack: "读取、加密并写入", service.StageSealCommit: "写入逃生舱目录",
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
