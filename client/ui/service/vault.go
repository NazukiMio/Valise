package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"valise/client/porter"
	"valise/client/ssh"
	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
)

// withSession runs fn on a porter RPC session to host.
func (s *Service) withSession(ctx context.Context, host string, fn func(*ssh.RPCClient) error) error {
	c, err := s.session(ctx, host)
	if err != nil {
		return err
	}
	defer c.Close()
	return fn(c)
}

// KeyRegister adds this client's public key to the server's recipients.
func (s *Service) KeyRegister(ctx context.Context, host string) (*vaultrpc.RecipientAddResult, *KeyInfo, error) {
	info, err := s.KeyShow()
	if err != nil {
		return nil, nil, err
	}
	var res vaultrpc.RecipientAddResult
	err = s.withSession(ctx, host, func(c *ssh.RPCClient) error {
		return c.Call(ctx, vaultrpc.MethodRecipientAdd, vaultrpc.RecipientAddParams{Recipient: info.Recipient}, &res)
	})
	return &res, info, err
}

// Seal seals an enrolled instance on host.
func (s *Service) Seal(ctx context.Context, host string, p vaultrpc.SealParams) (*vaultrpc.SealReport, error) {
	var rep vaultrpc.SealReport
	err := s.withSession(ctx, host, func(c *ssh.RPCClient) error {
		return c.Call(ctx, vaultrpc.MethodSeal, p, &rep)
	})
	return &rep, err
}

// VaultStatus lists snapshots and sync state without any key.
func (s *Service) VaultStatus(ctx context.Context, host string) (*vaultrpc.StatusResult, error) {
	var res vaultrpc.StatusResult
	err := s.withSession(ctx, host, func(c *ssh.RPCClient) error {
		return c.Call(ctx, vaultrpc.MethodStatus, struct{}{}, &res)
	})
	return &res, err
}

// VaultVerifyRemote runs the no-key verification on the server.
func (s *Service) VaultVerifyRemote(ctx context.Context, host string) (*vaultrpc.VerifyResult, error) {
	var res vaultrpc.VerifyResult
	err := s.withSession(ctx, host, func(c *ssh.RPCClient) error {
		return c.Call(ctx, vaultrpc.MethodVerify, struct{}{}, &res)
	})
	return &res, err
}

// TargetSet adds or replaces a sync target on host.
func (s *Service) TargetSet(ctx context.Context, host string, t vaultrpc.Target) error {
	return s.withSession(ctx, host, func(c *ssh.RPCClient) error {
		return c.Call(ctx, vaultrpc.MethodTargetSet, vaultrpc.TargetSetParams{Target: t}, nil)
	})
}

// Sync copies host's vault to one of its targets.
func (s *Service) Sync(ctx context.Context, host, target string) (*vaultrpc.SyncResult, error) {
	var res vaultrpc.SyncResult
	err := s.withSession(ctx, host, func(c *ssh.RPCClient) error {
		return c.Call(ctx, vaultrpc.MethodSync, vaultrpc.SyncParams{Target: target}, &res)
	})
	return &res, err
}

// PullResult reports a pull.
type PullResult struct {
	Dir        string          `json:"dir"`
	VaultID    string          `json:"vault_id"`
	Downloaded int             `json:"downloaded"`
	Bytes      int64           `json:"bytes"`
	Present    int             `json:"present"`
	Problems   []vault.Problem `json:"problems"`
}

// pullRank orders downloads so data lands before what references it.
func pullRank(rel string) int {
	switch {
	case rel == vault.DescriptorFile:
		return 0
	case strings.HasPrefix(rel, vault.EpochsDir+"/"), strings.HasPrefix(rel, vault.KeysDir+"/"):
		return 1
	case strings.HasPrefix(rel, vault.PacksDir+"/"):
		return 2
	case strings.HasPrefix(rel, vault.IndexDir+"/"):
		return 3
	case strings.HasSuffix(rel, ".age"):
		return 4
	}
	return 5 // meta last
}

// DefaultPullDir is <data>/vaults/<vault_id>.
func (s *Service) DefaultPullDir(vaultID string) string {
	return filepath.Join(s.Dirs.Data, "vaults", vaultID)
}

// Pull copies the files of host's vault that dir lacks (the vault is
// append-only), then verifies the local copy without a key.
func (s *Service) Pull(ctx context.Context, host, dir string) (*PullResult, error) {
	t, _, err := s.transport(host)
	if err != nil {
		return nil, err
	}
	found, err := porter.Probe(ctx, t)
	if err != nil {
		return nil, err
	}
	c, _, err := ssh.Dial(ctx, t, found.Path, s.Version)
	if err != nil {
		return nil, err
	}
	var list vaultrpc.FilesResult
	err = c.Call(ctx, vaultrpc.MethodFiles, struct{}{}, &list)
	c.Close()
	if err != nil {
		return nil, err
	}
	if dir == "" {
		dir = s.DefaultPullDir(list.VaultID)
	}
	if b, err := os.ReadFile(filepath.Join(dir, vault.DescriptorFile)); err == nil && !strings.Contains(string(b), `"`+list.VaultID+`"`) {
		return nil, valise.New("pull_wrong_vault", "目标目录里已经是另一个金库", "换一个目录，或不指定 --to")
	}
	res := &PullResult{Dir: dir, VaultID: list.VaultID}
	sort.SliceStable(list.Files, func(i, j int) bool { return pullRank(list.Files[i].Path) < pullRank(list.Files[j].Path) })
	for _, f := range list.Files {
		if !vault.ValidRel(f.Path) {
			continue
		}
		local := filepath.Join(dir, filepath.FromSlash(f.Path))
		if st, err := os.Stat(local); err == nil {
			if st.Size() != f.Size {
				return res, valise.New("pull_conflict", "本机副本中的 "+f.Path+" 与服务器不一致", "金库文件不应被修改；检查本机副本是否损坏")
			}
			res.Present++
			continue
		}
		if err := s.fetch(ctx, t, found.Path, f, local); err != nil {
			return res, err
		}
		res.Downloaded++
		res.Bytes += f.Size
	}
	probs, err := vault.VerifyNoKey(dir)
	if err != nil {
		return res, err
	}
	res.Problems = probs
	if res.Problems == nil {
		res.Problems = []vault.Problem{}
	}
	return res, nil
}

func (s *Service) fetch(ctx context.Context, t *ssh.Transport, porterPath string, f vaultrpc.VaultFile, local string) error {
	if err := os.MkdirAll(filepath.Dir(local), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(local), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	cmd, err := t.Command(ssh.Quote(porterPath) + " vault-cat " + ssh.Quote(f.Path))
	if err != nil {
		tmp.Close()
		return err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		tmp.Close()
		return err
	}
	if err := cmd.Start(); err != nil {
		tmp.Close()
		return err
	}
	h := sha256.New()
	n, copyErr := io.Copy(io.MultiWriter(tmp, h), out)
	waitErr := cmd.Wait()
	tmp.Chmod(0o600)
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()
	if copyErr != nil || waitErr != nil || n != f.Size {
		return valise.New("pull_interrupted", "拉取 "+f.Path+" 时中断", "重新运行 valise pull；已拉取的文件会保留，可以断点续传").
			WithDetail(fmt.Sprintf("got %d of %d bytes", n, f.Size))
	}
	if strings.HasPrefix(f.Path, vault.PacksDir+"/") && hex.EncodeToString(h.Sum(nil)) != strings.TrimSuffix(path.Base(f.Path), ".pack") {
		return valise.New("pull_corrupt", "拉取的 pack 与它的 SHA-256 不一致", "重新拉取；如果反复出现，在服务器上运行 valise vault verify")
	}
	return os.Rename(tmp.Name(), local)
}

// VerifyLocalResult is a local verification.
type VerifyLocalResult struct {
	Dir       string          `json:"dir"`
	WithKey   bool            `json:"with_key"`
	Snapshots int             `json:"snapshots"`
	Files     int             `json:"files"`
	Problems  []vault.Problem `json:"problems"`
}

// VerifyLocal verifies a local vault copy; with a passphrase it also
// decrypts every snapshot in memory and checks every file.
func (s *Service) VerifyLocal(dir string, passphrase *string) (*VerifyLocalResult, error) {
	res := &VerifyLocalResult{Dir: dir, Problems: []vault.Problem{}}
	probs, err := vault.VerifyNoKey(dir)
	if err != nil {
		return nil, valise.New("vault_unreadable", "无法读取本机金库 "+dir, "先运行 valise pull <服务器>").WithDetail(err.Error())
	}
	res.Problems = append(res.Problems, probs...)
	v, _ := vault.Open(dir)
	res.Snapshots = len(v.Metas)
	if passphrase == nil {
		return res, nil
	}
	k, err := s.Unlock(*passphrase)
	if err != nil {
		return nil, err
	}
	defer k.Lock()
	id, err := k.Identity()
	if err != nil {
		return nil, err
	}
	res.WithKey = true
	n, probs, err := vault.VerifyWithKey(dir, nil, id)
	if err != nil {
		return nil, err
	}
	res.Files = n
	res.Problems = append(res.Problems, probs...)
	return res, nil
}
