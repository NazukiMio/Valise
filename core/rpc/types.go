// Package rpc defines the control-plane protocol between the client and
// valise-porter: line-delimited JSON-RPC 2.0 over SSH stdio.
package rpc

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"valise/core/action"
	"valise/core/valise"
)

// ProtocolVersion is "<major>.<minor>". Peers with a different major refuse
// to talk; minor bumps are backwards-compatible additions.
const ProtocolVersion = "1.3"

// JSONRPCVersion is the JSON-RPC version string.
const JSONRPCVersion = "2.0"

// MaxMessageBytes bounds a single control-plane line.
const MaxMessageBytes = 16 << 20

// Method names.
const (
	MethodHello = "hello"
)

// JSON-RPC error codes. Application codes are in the -32000 range.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
	CodeHelloRequired  = -32000
	CodeUserError      = -32001
)

// Request is a JSON-RPC request. ID is absent for notifications.
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response is a JSON-RPC response.
type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

// Error is a JSON-RPC error. Data carries the user-facing error when there is
// one.
type Error struct {
	Code    int               `json:"code"`
	Message string            `json:"message"`
	Data    *valise.UserError `json:"data,omitempty"`
}

func (e *Error) Error() string {
	if e.Data != nil {
		return e.Data.Error()
	}
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

// HelloParams is sent by the client as the first request.
type HelloParams struct {
	ClientVersion string `json:"client_version"`
	Protocol      string `json:"protocol"`
}

// HelloResult describes the server side.
type HelloResult struct {
	PorterVersion  string                 `json:"porter_version"`
	Protocol       string                 `json:"protocol"`
	OS             string                 `json:"os"`
	Arch           string                 `json:"arch"`
	Home           string                 `json:"home"`
	Tools          []Tool                 `json:"tools"`
	PendingOps     []PendingOp            `json:"pending_ops"`
	Capabilities   []string               `json:"capabilities,omitempty"`
	PendingActions []action.PendingAction `json:"pending_actions,omitempty"`
}

// Tool names reported by hello.
const (
	ToolSystemctlUser = "systemctl --user"
	ToolGit           = "git"
	ToolNPM           = "npm"
	ToolUV            = "uv"
	ToolRsync         = "rsync"
	ToolRclone        = "rclone"
)

// ToolNames is the fixed order hello reports tools in.
var ToolNames = []string{ToolSystemctlUser, ToolGit, ToolNPM, ToolUV, ToolRsync, ToolRclone}

// Tool is the availability of one server-side tool.
type Tool struct {
	Name      string `json:"name"`
	Available bool   `json:"available"`
	Path      string `json:"path,omitempty"`
	Detail    string `json:"detail,omitempty"`
}

// PendingOp is an unfinished swap journal.
type PendingOp struct {
	OpID       string `json:"op_id"`
	InstanceID string `json:"instance_id,omitempty"`
	Mode       string `json:"mode,omitempty"`
	State      string `json:"state,omitempty"`
}

// VersionInfo is printed by `valise-porter version --json`.
type VersionInfo struct {
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Protocol     string   `json:"protocol"`
	OS           string   `json:"os"`
	Arch         string   `json:"arch"`
	SelfSHA256   string   `json:"self_sha256"`
	Capabilities []string `json:"capabilities,omitempty"`
}

// Major returns the major number of a "<major>.<minor>" version.
func Major(v string) (int, error) {
	maj, _, ok := strings.Cut(v, ".")
	if !ok {
		return 0, fmt.Errorf("协议版本格式无效：%q", v)
	}
	n, err := strconv.Atoi(maj)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("协议版本格式无效：%q", v)
	}
	return n, nil
}

// Compatible reports whether two protocol versions share a major number.
func Compatible(a, b string) bool {
	ma, err1 := Major(a)
	mb, err2 := Major(b)
	return err1 == nil && err2 == nil && ma == mb
}

// ProtocolMismatch is the user error for incompatible protocol versions.
func ProtocolMismatch(client, server string) *valise.UserError {
	return valise.New("protocol_mismatch",
		fmt.Sprintf("客户端（协议 %s）与服务器上的 porter（协议 %s）版本不兼容", client, server),
		"在服务器上重新部署与客户端同一版本的 porter：valise porter deploy <服务器>")
}
