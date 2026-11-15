package service

import (
	"archive/tar"
	"context"
	"io"
	"slices"

	"valise/client/ssh"
	"valise/core/revive"
	"valise/core/valise"
)

func parallelManifest(p *RevivePlan) []revive.ManifestEntry {
	manifest := restoreManifest(p)
	slices.SortFunc(manifest, func(a, b revive.ManifestEntry) int {
		if a.Path < b.Path {
			return -1
		}
		if a.Path > b.Path {
			return 1
		}
		return 0
	})
	return manifest
}

// This transfer has one server-derived stage target and a bounded lifetime.
// Cancellation closes this ssh process; it never interrupts active directories.
func (s *Service) streamParallelStage(ctx context.Context, p *RevivePlan) error {
	if len(p.links) != 0 {
		return valise.New("environment_prepare_entries", "当前正式准备尚未适配这些链接，没有上传", "仍可校验文件；等待对应内容的准备适配")
	}
	cmd, err := p.target.t.Command(ssh.Quote(p.target.path) + " stage-write --op " + ssh.Quote(p.Plan.OpID))
	if err != nil {
		return err
	}
	input, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		input.Close()
		return err
	}
	stop := context.AfterFunc(ctx, func() { input.Close(); _ = cmd.Process.Kill() })
	defer stop()
	var done int64
	tw := tar.NewWriter(&countingWriter{ctx: ctx, stage: StageStage, done: &done, total: p.Bytes, w: input})
	writeErr := func() error {
		for _, file := range p.files {
			h := &tar.Header{Name: tarName(p.Plan, file.Root, file.Path), Mode: int64(file.Mode & 0777), Size: file.Size, ModTime: file.MTime, Typeflag: tar.TypeReg, Format: tar.FormatPAX}
			if err := tw.WriteHeader(h); err != nil {
				return err
			}
			if err := p.writeRestoreFile(file, tw); err != nil {
				return err
			}
		}
		return tw.Close()
	}()
	input.Close()
	waitErr := cmd.Wait()
	if writeErr != nil || waitErr != nil {
		return valise.New("environment_upload_incomplete", "新环境的数据暂存尚未完成，当前 Agent 未切换", "查询原准备操作；不要重复上传，需取消后另行处理保留材料")
	}
	return nil
}
