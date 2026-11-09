package service

import (
	"context"
	"fmt"
	"strings"

	"valise/client/porter"
	"valise/client/ssh"
	"valise/core/agentkit"
	"valise/core/valise"
	"valise/core/vaultrpc"
)

// LeaveStep is one cleanup item's result.
type LeaveStep struct {
	Item   string `json:"item"`
	Detail string `json:"detail,omitempty"`
	// Skipped: nothing to clean, or not chosen.
	Skipped bool `json:"skipped,omitempty"`
}

// LeaveOptions choose the cleanup items and report progress.
type LeaveOptions struct {
	// Items are the chosen cleanup items; LeaveMark needs no choice.
	Items map[string]bool
	// Left is what the dialog showed; it limits what is cleaned.
	Left *ServerLeftovers
	// Doing reports the running item and what it waits on.
	Doing func(item, what string)
	// Step reports each finished item.
	Step func(LeaveStep)
}

// LeaveServer removes host from Valise and cleans the chosen items on the
// server in LeaveOrder over one admin login. It stops at the first failure
// and keeps the server in the list; what is done so far was reported by
// Step. The backup library stays.
func (s *Service) LeaveServer(ctx context.Context, host string, o LeaveOptions) error {
	l := o.Left
	if l == nil {
		return valise.New("leave_unread", "还没有读到服务器上有哪些 Valise 的东西", "先读取一次再删除")
	}
	doing := func(item, what string) {
		if o.Doing != nil {
			o.Doing(item, what)
		}
	}
	step := func(st LeaveStep) {
		if o.Step != nil {
			o.Step(st)
		}
	}
	doing("", "管理登录到 "+host)
	t, done, err := s.adminTransport(ctx, host)
	if err != nil {
		return err
	}
	defer done()
	var c *ssh.RPCClient
	if l.Porter {
		found, err := porter.Probe(ctx, t)
		if err != nil {
			return err
		}
		if c, _, err = ssh.Dial(ctx, t, found.Path, s.Version); err != nil {
			return err
		}
		defer func() {
			if c != nil {
				c.Close()
			}
		}()
	}
	for _, item := range LeaveOrder {
		chosen := item == LeaveMark || o.Items[item]
		if !chosen || !l.Has(item) {
			step(LeaveStep{Item: item, Skipped: true})
			continue
		}
		detail, err := s.leaveItem(ctx, host, item, l, c, t, doing)
		if err != nil {
			return err
		}
		if item == LeavePorter && c != nil {
			c.Close()
			c = nil
		}
		step(LeaveStep{Item: item, Detail: detail})
	}
	doing("", "从这台电脑移除 "+host)
	if err := s.removeHere(host, l, true); err != nil {
		return err
	}
	return nil
}

func (s *Service) leaveItem(ctx context.Context, host, item string, l *ServerLeftovers, c *ssh.RPCClient, t *ssh.Transport, doing func(string, string)) (string, error) {
	leave := func(step string) (vaultrpc.ServerLeaveResult, error) {
		var res vaultrpc.ServerLeaveResult
		err := c.Call(ctx, vaultrpc.MethodServerLeave, vaultrpc.ServerLeaveParams{Step: step}, &res)
		return res, err
	}
	switch item {
	case LeaveTimers:
		doing(item, "停止定时备份并删除它们的 systemd 单元")
		res, err := leave(vaultrpc.LeaveTimers)
		return strings.Join(res.Timers, "、"), err
	case LeaveTools:
		var names []string
		for _, k := range l.Tools {
			doing(item, "删除 "+k.Name)
			if err := c.Call(ctx, agentkit.MethodKitToolRemove, agentkit.KitToolRemoveParams{Name: k.Binary}, nil); err != nil {
				return "", err
			}
			names = append(names, k.Name)
		}
		return strings.Join(names, "、"), nil
	case LeaveClouds:
		for _, name := range l.Clouds {
			doing(item, "删除云盘授权 "+name)
			if err := c.Call(ctx, vaultrpc.MethodCloudForget, vaultrpc.CloudForgetParams{Name: name, RemoveTarget: true}, nil); err != nil {
				return "", err
			}
		}
		return strings.Join(l.Clouds, "、"), nil
	case LeaveSkills:
		files, err := KitFiles()
		if err != nil {
			return "", err
		}
		var parts []string
		for _, sk := range l.Skills {
			doing(item, "删除 "+sk.Instance+" 的保护技能")
			var res agentkit.KitUninstallResult
			if err := c.Call(ctx, agentkit.MethodKitUninstall, agentkit.KitUninstallParams{InstanceID: sk.Instance, Files: files}, &res); err != nil {
				return "", err
			}
			part := fmt.Sprintf("%s：删了 %d 个", sk.Instance, len(res.Removed))
			if len(res.Kept) > 0 {
				part += fmt.Sprintf("，%d 个改过的留着", len(res.Kept))
			}
			parts = append(parts, part)
		}
		return strings.Join(parts, "；"), nil
	case LeaveMark:
		doing(item, "在 server.json 标记“已删除”，备份库留着")
		_, err := leave(vaultrpc.LeaveMark)
		return "", err
	case LeavePorter:
		doing(item, "删除 porter")
		res, err := leave(vaultrpc.LeavePorter)
		return res.Porter, err
	case LeavePasses:
		doing(item, "从 authorized_keys 撤下通行证")
		return "", s.uninstallKeys(ctx, t, host, l.Passes...)
	}
	return "", nil
}
