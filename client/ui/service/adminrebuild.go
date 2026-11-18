package service

import (
	"context"
	"errors"
	"os"

	"valise/core/valise"
)

// Forgetting the admin passphrase (docs/ARCHITECTURE.md 3.3): admin
// passes guard no data, so they are rebuilt, not recovered. The old
// public key stays here (admin-old.pub) until the server's old line is
// removed.

const adminOldName = "admin-old"

// AdminRebuild is one server's result.
type AdminRebuild struct {
	Server  string `json:"server"`
	Done    bool   `json:"done"`
	Pending bool   `json:"pending"` // could not log in: rebuilt next time
	Err     error  `json:"-"`
}

// AdminRebuildPending reports whether host's admin pass still waits to be
// rebuilt.
func (s *Service) AdminRebuildPending(host string) bool {
	return fileExists(s.keyPath(host, adminOldName) + ".pub")
}

// AdminPassServers lists the servers with an admin pass or one waiting to
// be rebuilt.
func (s *Service) AdminPassServers() []string {
	list, _ := s.ServerList()
	var out []string
	for _, srv := range list {
		if srv.Valise() && (s.HasAdminKey(srv.Name) || s.AdminRebuildPending(srv.Name)) {
			out = append(out, srv.Name)
		}
	}
	return out
}

// RebuildAdminPass replaces host's admin pass with one under the new
// passphrase, over one temporary login with the server password (or the
// user's own key file).
func (s *Service) RebuildAdminPass(ctx context.Context, host, passphrase string) error {
	stopOp := s.BeginOperation()
	defer stopOp()
	if err := s.checkAdminPassphrase(passphrase); err != nil {
		return err
	}
	srv := s.server(host)
	if !srv.Valise() {
		return valise.New("admin_key_untiered", host+" 用的是你自己的 SSH 设置，没有管理员通行证", "")
	}
	s.LockAdmin()
	old := s.keyPath(host, adminOldName) + ".pub"
	if s.HasAdminKey(host) {
		// The old private key can no longer be opened: keep only its
		// public half, to find the server's old line.
		if err := os.Rename(s.keyPath(host, adminKeyName)+".pub", old); err != nil {
			return err
		}
		if err := os.Remove(s.keyPath(host, adminKeyName)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	t, err := s.bootstrapTransport(srv)
	if err != nil {
		return err
	}
	defer t.CloseMaster()
	if fileExists(old) {
		if err := s.uninstallKeys(ctx, t, host, adminOldName); err != nil {
			return valise.New("admin_rebuild_failed", "没能删去服务器上旧的管理员通行证", "稍后重试").WithDetail(err.Error())
		}
	}
	if err := s.createAdminKey(ctx, host, passphrase); err != nil {
		return err
	}
	if err := s.installKeys(ctx, t, host, adminKeyName); err != nil {
		return err
	}
	if err := os.Remove(old); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// RebuildAdminPasses rebuilds every server's admin pass in turn; each
// result is reported by step. A server that fails stays pending.
func (s *Service) RebuildAdminPasses(ctx context.Context, passphrase string, step func(AdminRebuild)) error {
	stopOp := s.BeginOperation()
	defer stopOp()
	if err := s.checkAdminPassphrase(passphrase); err != nil {
		return err
	}
	for _, host := range s.AdminPassServers() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := s.RebuildAdminPass(ctx, host, passphrase)
		r := AdminRebuild{Server: host, Done: err == nil, Pending: err != nil, Err: err}
		if step != nil {
			step(r)
		}
	}
	return nil
}
