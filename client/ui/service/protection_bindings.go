package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"valise/client/config"
	"valise/core/identity"
	"valise/core/protection"
)

// ProtectionBindingStore is independent of the server protection transaction.
// It only manages configuration; it does not install automatic copy execution.
type ProtectionBindingStore struct {
	mu    sync.Mutex
	dir   string
	repos []config.Repo
	// clouds are the registered cloud names a CloudCopies entry may refer to.
	clouds []config.Cloud
}

// WithClouds teaches the store the registered clouds so cloud_copies entries
// can be validated. A nil list rejects any cloud entry as unknown.
func (s *ProtectionBindingStore) WithClouds(clouds []config.Cloud) *ProtectionBindingStore {
	s.clouds = append([]config.Cloud(nil), clouds...)
	return s
}

func NewProtectionBindingStore(stateDir string, repos []config.Repo) *ProtectionBindingStore {
	return &ProtectionBindingStore{dir: stateDir, repos: append([]config.Repo(nil), repos...)}
}

func (s *ProtectionBindingStore) readFile() (protection.BindingFile, bool, error) {
	var file protection.BindingFile
	p := filepath.Join(s.dir, "protection-bindings.json")
	st, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		return file, false, nil
	}
	if err != nil || !st.Mode().IsRegular() || st.Size() > 4<<20 {
		return file, false, protectionError("record_corrupt", "客户端保护绑定文件不可读")
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return file, false, protectionError("record_corrupt", "无法读取客户端保护绑定")
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&file); err != nil {
		return file, false, protectionError("unsupported_schema", "客户端保护绑定包含无效或不支持的字段")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return file, false, protectionError("record_corrupt", "客户端保护绑定包含多余内容")
	}
	if err = file.Validate(); err != nil {
		return file, false, err
	}
	return file, true, nil
}

// Read returns nil when no accepted binding exists. An absent file is not
// initialized by reading; server settings and legacy repo automation stay intact.
func (s *ProtectionBindingStore) Read(ctx context.Context, agent identity.AgentRef) (*protection.ClientBinding, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := protection.ValidateAgent(agent); err != nil {
		return nil, err
	}
	if s.dir == "" {
		return nil, protectionError("request_invalid", "缺少客户端状态目录")
	}
	file, exists, err := s.readFile()
	if err != nil || !exists {
		return nil, err
	}
	for _, b := range file.Bindings {
		if b.AgentRef == agent {
			copy := cloneProtection(b)
			return &copy, nil
		}
	}
	return nil, nil
}

// CompareAndSwap replaces this client's choices for exactly one AgentRef.
// A stale local revision is rejected even when the server plan has committed.
// Callers must report a local failure separately; this store never rolls back
// or touches a server, and never changes legacy repository auto settings.
//
// CompareAndSwap keeps any cloud choices already saved; CompareAndSwapCopies
// replaces both lists (a nil cloud list keeps the saved cloud choices).
func (s *ProtectionBindingStore) CompareAndSwap(ctx context.Context, agent identity.AgentRef, expected, serverRevisionSeen protection.Revision, copies []protection.LocalCopy) (*protection.ClientBinding, error) {
	return s.CompareAndSwapCopies(ctx, agent, expected, serverRevisionSeen, copies, nil)
}

func (s *ProtectionBindingStore) CompareAndSwapCopies(ctx context.Context, agent identity.AgentRef, expected, serverRevisionSeen protection.Revision, copies, clouds []protection.LocalCopy) (*protection.ClientBinding, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := protection.ValidateAgent(agent); err != nil {
		return nil, err
	}
	if err := protection.ValidateLocalCopies(copies); err != nil {
		return nil, err
	}
	if s.dir == "" {
		return nil, protectionError("request_invalid", "缺少客户端状态目录")
	}
	if expected == protection.Revision(math.MaxUint64) {
		return nil, protectionError("revision_conflict", "客户端保护绑定版本已到上限")
	}
	copies = cloneProtection(copies)
	registered := map[string]bool{}
	for _, repo := range s.repos {
		registered[repo.Name] = true
	}
	for _, copy := range copies {
		if !registered[copy.RepoID] {
			return nil, protectionError("repo_unknown", "本机保护设置引用了未登记的仓库")
		}
	}
	if clouds != nil {
		if err := protection.ValidateLocalCopies(clouds); err != nil {
			return nil, err
		}
		clouds = cloneProtection(clouds)
		knownCloud := map[string]bool{}
		for _, c := range s.clouds {
			knownCloud[c.Name] = true
		}
		for _, c := range clouds {
			if !knownCloud[c.RepoID] {
				return nil, protectionError("cloud_unknown", "本机保护设置引用了未登记的云盘")
			}
		}
		sort.Slice(clouds, func(i, j int) bool { return clouds[i].RepoID < clouds[j].RepoID })
	}
	// The immutable order also makes equivalent configurations easy to compare.
	sort.Slice(copies, func(i, j int) bool { return copies[i].RepoID < copies[j].RepoID })
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.dir, 0700); err != nil {
		return nil, protectionError("record_corrupt", "无法创建客户端状态目录")
	}
	unlock, err := protectionStoreLock(filepath.Join(s.dir, ".protection-bindings.lock"))
	if err != nil {
		return nil, err
	}
	defer unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, exists, err := s.readFile()
	if err != nil {
		return nil, err
	}
	if !exists {
		file = protection.BindingFile{SchemaVersion: protection.BindingSchema, Bindings: []protection.ClientBinding{}}
	}
	index := -1
	for i, b := range file.Bindings {
		if b.AgentRef == agent {
			index = i
			break
		}
	}
	var revision protection.Revision
	if index >= 0 {
		revision = file.Bindings[index].Revision
	}
	if revision != expected {
		return nil, protectionError("revision_conflict", "本客户端的保护设置已被另一个操作修改")
	}
	if file.ClientID == "" {
		file.ClientID, err = randomJourneyID()
		if err != nil {
			return nil, err
		}
	}
	if clouds == nil && index >= 0 {
		clouds = file.Bindings[index].CloudCopies
	}
	binding := protection.ClientBinding{ClientID: file.ClientID, AgentRef: agent, Revision: revision + 1, ServerRevisionSeen: serverRevisionSeen, LocalCopies: copies, CloudCopies: clouds}
	if index >= 0 {
		file.Bindings[index] = binding
	} else {
		file.Bindings = append(file.Bindings, binding)
	}
	if err = file.Validate(); err != nil {
		return nil, err
	}
	if err = protectionAtomic(filepath.Join(s.dir, "protection-bindings.json"), file); err != nil {
		return nil, err
	}
	binding = cloneProtection(binding)
	return &binding, nil
}

// Delete removes any saved local protection binding for agent. It is a no-op
// when the bindings file does not exist or contains no matching entry.
func (s *ProtectionBindingStore) Delete(ctx context.Context, agent identity.AgentRef) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := protection.ValidateAgent(agent); err != nil {
		return err
	}
	if s.dir == "" {
		return protectionError("request_invalid", "缺少客户端状态目录")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := os.Stat(s.dir); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return protectionError("record_corrupt", "无法读取客户端状态目录")
	}
	unlock, err := protectionStoreLock(filepath.Join(s.dir, ".protection-bindings.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	file, exists, err := s.readFile()
	if err != nil || !exists {
		return err
	}
	kept := make([]protection.ClientBinding, 0, len(file.Bindings))
	removed := false
	for _, b := range file.Bindings {
		if b.AgentRef == agent {
			removed = true
			continue
		}
		kept = append(kept, b)
	}
	if !removed {
		return nil
	}
	file.Bindings = kept
	if err := file.Validate(); err != nil {
		return err
	}
	return protectionAtomic(filepath.Join(s.dir, "protection-bindings.json"), file)
}
