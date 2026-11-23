package protection

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"valise/core/identity"
	"valise/core/valise"
)

const BindingSchema = "valise-protection-bindings/1"

// LocalCopy is a configuration choice only. It is not evidence that copying
// ran, that the client will be alive, or that any material is complete.
type LocalCopy struct {
	RepoID       string `json:"repo_id"`
	AfterBackup  *bool  `json:"after_backup"`
	OnClientOpen *bool  `json:"on_client_open"`
}

type ClientBinding struct {
	ClientID           string            `json:"client_id"`
	AgentRef           identity.AgentRef `json:"agent_ref"`
	Revision           Revision          `json:"revision"`
	ServerRevisionSeen Revision          `json:"server_revision_seen"`
	LocalCopies        []LocalCopy       `json:"local_copies"`
	CloudCopies        []LocalCopy       `json:"cloud_copies,omitempty"`
}

type BindingFile struct {
	SchemaVersion string          `json:"schema_version"`
	ClientID      string          `json:"client_id"`
	Bindings      []ClientBinding `json:"bindings"`
}

func decodeBindingJSON(b []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return fmt.Errorf("unexpected binding JSON content")
	}
	return nil
}

func (c *LocalCopy) UnmarshalJSON(b []byte) error {
	type plain LocalCopy
	var value plain
	if err := decodeBindingJSON(b, &value); err != nil {
		return err
	}
	*c = LocalCopy(value)
	return nil
}

func (c *ClientBinding) UnmarshalJSON(b []byte) error {
	type plain ClientBinding
	var value plain
	if err := decodeBindingJSON(b, &value); err != nil {
		return err
	}
	*c = ClientBinding(value)
	return nil
}

func ValidateLocalCopies(copies []LocalCopy) error {
	if copies == nil {
		return valise.New("request_invalid", "缺少本客户端副本完整设置", "明确提供 local_copies，空列表表示不配置")
	}
	seen := map[string]bool{}
	for _, copy := range copies {
		if copy.RepoID == "" || copy.AfterBackup == nil || copy.OnClientOpen == nil || seen[copy.RepoID] {
			return valise.New("request_invalid", "本机仓库引用重复或缺少明确的复制选择", "选择已登记的本机仓库，并明确两项复制设置")
		}
		seen[copy.RepoID] = true
	}
	return nil
}

func (b ClientBinding) Validate() error {
	if !ValidID(b.ClientID) || b.Revision == 0 {
		return valise.New("record_corrupt", "客户端保护绑定身份或版本无效", "核实客户端保护绑定记录")
	}
	if err := ValidateAgent(b.AgentRef); err != nil {
		return err
	}
	if err := ValidateLocalCopies(b.LocalCopies); err != nil {
		return err
	}
	if b.CloudCopies != nil {
		if err := ValidateLocalCopies(b.CloudCopies); err != nil {
			return err
		}
	}
	return nil
}

func (f BindingFile) Validate() error {
	if f.SchemaVersion != BindingSchema {
		return valise.New("unsupported_schema", "客户端保护绑定格式不受支持", "更新到支持此格式的客户端")
	}
	if !ValidID(f.ClientID) || f.Bindings == nil {
		return valise.New("record_corrupt", "客户端保护绑定身份或列表无效", "核实客户端保护绑定记录")
	}
	seen := map[identity.AgentRef]bool{}
	for _, b := range f.Bindings {
		if err := b.Validate(); err != nil {
			return err
		}
		if b.ClientID != f.ClientID || seen[b.AgentRef] {
			return valise.New("record_corrupt", "客户端保护绑定身份不一致或实例世代重复", "核实客户端保护绑定记录")
		}
		seen[b.AgentRef] = true
	}
	return nil
}
