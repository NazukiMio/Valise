package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CustomInstallsFile holds user-saved custom install URLs/commands in the
// client data directory (docs/INSTALL.md section 7). Selecting a saved preset
// only fills the input and never bypasses download, SHA-256 display, two-tier
// static checks, or user confirmation.
const CustomInstallsFile = "custom-installs.json"

// SavedInstall is one user-saved custom install preset stored on the client.
type SavedInstall struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Raw       string    `json:"raw"`
	ProfileID string    `json:"profile_id,omitempty"`
	SavedAt   time.Time `json:"saved_at"`
}

type savedInstallsDoc struct {
	Installs []SavedInstall `json:"installs"`
}

// LoadSavedInstalls reads custom-installs.json from d.Data; a missing file
// returns an empty slice and nil error.
func (d Dirs) LoadSavedInstalls() ([]SavedInstall, error) {
	if d.Data == "" {
		return []SavedInstall{}, nil
	}
	b, err := os.ReadFile(filepath.Join(d.Data, CustomInstallsFile))
	if errors.Is(err, os.ErrNotExist) {
		return []SavedInstall{}, nil
	}
	if err != nil {
		return nil, err
	}
	var doc savedInstallsDoc
	if err := json.Unmarshal(b, &doc); err == nil && doc.Installs != nil {
		return doc.Installs, nil
	}
	var list []SavedInstall
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, err
	}
	if list == nil {
		list = []SavedInstall{}
	}
	return list, nil
}

// SaveSavedInstall inserts or updates a saved custom install preset in
// custom-installs.json (0600 atomic write).
func (d Dirs) SaveSavedInstall(item SavedInstall) error {
	if d.Data == "" {
		return errors.New("custom install: empty data directory")
	}
	item.ID = strings.TrimSpace(item.ID)
	item.Name = strings.TrimSpace(item.Name)
	item.Raw = strings.TrimSpace(item.Raw)
	item.ProfileID = strings.TrimSpace(item.ProfileID)
	if item.Raw == "" {
		return errors.New("custom install: empty URL or command")
	}
	if item.Name == "" {
		item.Name = item.Raw
	}
	if item.SavedAt.IsZero() {
		item.SavedAt = time.Now().UTC()
	} else {
		item.SavedAt = item.SavedAt.UTC()
	}
	list, err := d.LoadSavedInstalls()
	if err != nil {
		return err
	}
	replaced := false
	for i := range list {
		if (item.ID != "" && list[i].ID == item.ID) || (list[i].Raw == item.Raw && list[i].ProfileID == item.ProfileID) {
			if item.ID == "" {
				item.ID = list[i].ID
			}
			list[i] = item
			replaced = true
			break
		}
	}
	if !replaced {
		if item.ID == "" {
			item.ID = newSavedInstallID()
		}
		list = append(list, item)
	}
	return d.writeSavedInstalls(list)
}

// DeleteSavedInstall removes a saved custom install preset by ID from
// custom-installs.json. Removing a non-existent ID is a no-op.
func (d Dirs) DeleteSavedInstall(id string) error {
	if d.Data == "" {
		return errors.New("custom install: empty data directory")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	list, err := d.LoadSavedInstalls()
	if err != nil {
		return err
	}
	out := make([]SavedInstall, 0, len(list))
	changed := false
	for _, item := range list {
		if item.ID == id {
			changed = true
			continue
		}
		out = append(out, item)
	}
	if !changed {
		return nil
	}
	return d.writeSavedInstalls(out)
}

func (d Dirs) writeSavedInstalls(list []SavedInstall) error {
	if err := os.MkdirAll(d.Data, 0o700); err != nil {
		return err
	}
	if list == nil {
		list = []SavedInstall{}
	}
	b, err := json.MarshalIndent(savedInstallsDoc{Installs: list}, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(d.Data, CustomInstallsFile, append(b, '\n'))
}

func newSavedInstallID() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return "ci_" + hex.EncodeToString(b[:])
}
