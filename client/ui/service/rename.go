package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"valise/client/ssh"
	"valise/core/valise"
	"valise/core/vault"
	"valise/core/vaultrpc"
)

// Renaming a server (docs/ARCHITECTURE.md section 3): the name is the
// folder of its escape pods in every repository and station, so those
// folders are renamed with it, one by one. A station that cannot be
// reached is recorded and finished before the next copy to it.

// RenamePlace is one folder a rename moves.
type RenamePlace struct {
	Kind string `json:"kind"` // repo, station, other
	Name string `json:"name"`
	From string `json:"from"`
	To   string `json:"to"`
	// Missing: nothing was copied there yet; only the new name is used.
	Missing bool `json:"missing,omitempty"`
	// Done, Pending and Err are filled by RenameServer.
	Done    bool   `json:"done,omitempty"`
	Pending bool   `json:"pending,omitempty"`
	Err     string `json:"error,omitempty"`
}

// RenamePlan lists what a rename changes.
type RenamePlan struct {
	Host   string        `json:"host"`
	Old    string        `json:"old"`
	New    string        `json:"new"`
	Places []RenamePlace `json:"places"`
}

// PlanRename lists the folders renaming host to name moves; nothing changes.
func (s *Service) PlanRename(ctx context.Context, host, name string) (*RenamePlan, error) {
	st, err := s.VaultStatus(ctx, host)
	if err != nil {
		return nil, err
	}
	switch {
	case st.ServerName == "":
		return nil, valise.New("server_name_unset", host+" 还没有名字", "先起名（服务器设置里按 n）")
	case st.ServerName == name:
		return nil, valise.New("server_name_same", host+" 已经叫 "+name, "")
	}
	plan := &RenamePlan{Host: host, Old: st.ServerName, New: name}
	for _, r := range s.Repos() {
		pl := RenamePlace{Kind: "repo", Name: r.Name, From: filepath.Join(r.Path, st.ServerName), To: filepath.Join(r.Path, name)}
		if _, err := os.Stat(pl.From); err != nil {
			pl.Missing = true
		}
		if _, err := os.Stat(pl.To); err == nil {
			return nil, valise.New("rename_taken", "仓库 "+r.Name+" 里已经有 "+name+" 文件夹", "换一个名字，或先处理那个文件夹").WithDetail(pl.To)
		}
		plan.Places = append(plan.Places, pl)
	}
	for _, t := range st.Targets {
		dest := strings.TrimRight(t.Dest, "/")
		if s.IsStation(t.Name) {
			stn, _ := s.station(t.Name)
			base := strings.TrimRight(stn.Path, "/")
			plan.Places = append(plan.Places, RenamePlace{Kind: "station", Name: t.Name, From: stn.Address + ":" + base + "/" + st.ServerName, To: stn.Address + ":" + base + "/" + name})
			continue
		}
		// A target the server reaches with its own SSH setup: Valise has
		// no login there, so the user renames that folder.
		plan.Places = append(plan.Places, RenamePlace{Kind: "other", Name: t.Name, From: dest + "/" + st.ServerName, To: dest + "/" + name})
	}
	return plan, nil
}

// RenameServer carries out a plan: the server first, then each folder.
// It fails only when the server cannot be renamed; a folder that fails is
// reported in its place (stations are retried before their next copy).
func (s *Service) RenameServer(ctx context.Context, plan *RenamePlan) error {
	var res vaultrpc.ServerRenameResult
	if err := s.call(ctx, plan.Host, vaultrpc.MethodServerRename, vaultrpc.ServerRenameParams{Name: plan.New}, &res); err != nil {
		return err
	}
	for i := range plan.Places {
		pl := &plan.Places[i]
		var err error
		switch pl.Kind {
		case "repo":
			if !pl.Missing {
				err = renameLocal(pl.From, pl.To, plan.Old, plan.New)
			}
			pl.Done = err == nil
		case "station":
			if err = s.renameStation(ctx, plan.Host, pl.Name, plan.Old, plan.New); err != nil {
				pl.Pending = true
				s.recordPendingRename(plan.Host, pl.Name, plan.Old, plan.New)
			} else {
				pl.Done = true
			}
		}
		if err != nil {
			pl.Err = errText1(err)
		}
	}
	return nil
}

// renameLocal moves a repository folder and records the old name in TREE.
func renameLocal(from, to, old, name string) error {
	if err := os.Rename(from, to); err != nil {
		return err
	}
	return rewriteTree(filepath.Join(to, vault.TreeFile), old, name)
}

func rewriteTree(path, old, name string) error {
	d := vault.TreeDesc{Format: vault.TreeFormat, CreatedAt: time.Now().UTC()}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &d)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	b := renamedTree(d, old, name)
	return os.WriteFile(path, b, 0o600)
}

func renamedTree(d vault.TreeDesc, old, name string) []byte {
	if d.Format == "" {
		d.Format = vault.TreeFormat
	}
	d.ServerName, d.RenamedFrom = name, append(d.RenamedFrom, old)
	b, _ := json.MarshalIndent(d, "", "  ")
	return append(b, '\n')
}

// renameStation moves the folder on a managed station from this computer,
// rewrites its TREE, then attaches the station again: the server's
// certificate names its folder.
func (s *Service) renameStation(ctx context.Context, host, name, old, newName string) error {
	stn, err := s.station(name)
	if err != nil {
		return err
	}
	base := strings.TrimRight(stn.Path, "/")
	from, to := ssh.Quote(base+"/"+old), ssh.Quote(base+"/"+newName)
	t := s.stationTransport(*stn)
	// 3: the new folder exists already; nothing moved.
	r, err := t.Run(ctx, "if [ -e "+to+" ]; then exit 3; fi; if [ -d "+from+" ]; then mv "+from+" "+to+"; fi; cat "+to+"/"+vault.TreeFile+" 2>/dev/null; true", nil)
	if err != nil {
		return err
	}
	if r.ExitCode == 3 {
		return valise.New("rename_taken", "空间站 "+name+" 上已经有 "+newName+" 文件夹", "先处理那个文件夹，再上云一次会接着改名")
	}
	if r.ExitCode != 0 {
		return valise.New("rename_station_failed", "没能在空间站 "+name+" 上改名", "检查空间站后再上云一次，会接着改名").WithDetail(strings.TrimSpace(string(r.Stderr)))
	}
	var d vault.TreeDesc
	if json.Unmarshal(r.Stdout, &d) == nil && d.ServerName != "" {
		w, err := t.Run(ctx, "cat > "+to+"/"+vault.TreeFile, strings.NewReader(string(renamedTree(d, old, newName))))
		if err == nil && w.ExitCode != 0 {
			err = errors.New(strings.TrimSpace(string(w.Stderr)))
		}
		if err != nil {
			return valise.New("rename_station_failed", "空间站 "+name+" 上的 TREE 没有改好", "再上云一次会接着改").WithDetail(err.Error())
		}
	}
	st, err := s.VaultStatus(ctx, host)
	if err != nil {
		return err
	}
	if err := s.attachStation(ctx, host, *stn, st); err != nil {
		return err
	}
	s.clearPendingRename(host, name)
	return nil
}

// Renames not yet done on a station: host → station → [old, new].
const pendingRenameFile = "rename-pending.json"

func (s *Service) pendingRenames() map[string]map[string][2]string {
	out := map[string]map[string][2]string{}
	if b, err := os.ReadFile(filepath.Join(s.Dirs.Data, pendingRenameFile)); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

func (s *Service) savePendingRenames(all map[string]map[string][2]string) {
	b, _ := json.MarshalIndent(all, "", "  ")
	_ = os.WriteFile(filepath.Join(s.Dirs.Data, pendingRenameFile), append(b, '\n'), 0o600)
}

func (s *Service) recordPendingRename(host, station, old, name string) {
	all := s.pendingRenames()
	if all[host] == nil {
		all[host] = map[string][2]string{}
	}
	if prev, ok := all[host][station]; ok {
		old = prev[0] // renamed twice before the station was reached
	}
	all[host][station] = [2]string{old, name}
	s.savePendingRenames(all)
}

func (s *Service) clearPendingRename(host, station string) {
	all := s.pendingRenames()
	if _, ok := all[host][station]; ok {
		delete(all[host], station)
		s.savePendingRenames(all)
	}
}

// finishRename completes a pending rename on station before copying to it.
func (s *Service) finishRename(ctx context.Context, host, station string) error {
	p, ok := s.pendingRenames()[host][station]
	if !ok {
		return nil
	}
	return s.renameStation(ctx, host, station, p[0], p[1])
}
