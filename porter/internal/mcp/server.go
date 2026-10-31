// Package mcp is `valise-porter mcp --instance <id>`: a minimal MCP server
// on stdio with the six agent-kit tools (docs/AGENT_KIT.md sections 3 and
// 8.1). One process serves one instance.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"

	"valise/core/rpc"
	"valise/core/valise"
)

// Supported protocol versions, newest first.
var protocolVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

// Serve answers MCP requests for one instance until r reaches EOF.
func Serve(ctx context.Context, r io.Reader, w io.Writer, t *Tools) error {
	conn := rpc.NewConn(r, w)
	for {
		line, err := conn.ReadLine()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		var req rpc.Request
		if err := json.Unmarshal(line, &req); err != nil {
			conn.Write(errResp(json.RawMessage("null"), rpc.CodeParseError, "parse error"))
			continue
		}
		if len(req.ID) == 0 {
			continue // notifications (initialized, cancelled) need no answer
		}
		conn.Write(handle(ctx, t, req))
	}
}

func errResp(id json.RawMessage, code int, msg string) rpc.Response {
	return rpc.Response{JSONRPC: rpc.JSONRPCVersion, ID: id, Error: &rpc.Error{Code: code, Message: msg}}
}

func okResp(id json.RawMessage, v any) rpc.Response {
	b, err := json.Marshal(v)
	if err != nil {
		return errResp(id, rpc.CodeInternalError, err.Error())
	}
	return rpc.Response{JSONRPC: rpc.JSONRPCVersion, ID: id, Result: b}
}

func handle(ctx context.Context, t *Tools, req rpc.Request) rpc.Response {
	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		json.Unmarshal(req.Params, &p)
		v := protocolVersions[0]
		if slices.Contains(protocolVersions, p.ProtocolVersion) {
			v = p.ProtocolVersion
		}
		return okResp(req.ID, map[string]any{
			"protocolVersion": v,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": "valise", "version": t.Version},
			"instructions": "Valise protects this agent instance (" + t.Instance + "): it seals the agent's soul " +
				"(persona, memory, skills, sessions, state) into an encrypted vault. Call valise_seal before risky " +
				"changes, valise_handoff_begin/finish to hand over to a new session, valise_checkup when asked how you are.",
		})
	case "ping":
		return okResp(req.ID, map[string]any{})
	case "tools/list":
		return okResp(req.ID, map[string]any{"tools": toolList})
	case "tools/call":
		var p struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			return errResp(req.ID, rpc.CodeInvalidParams, "invalid params")
		}
		res, err := t.Call(ctx, p.Name, p.Arguments)
		if errors.Is(err, errUnknownTool) {
			return errResp(req.ID, rpc.CodeInvalidParams, "unknown tool: "+p.Name)
		}
		return okResp(req.ID, toolResult(res, err))
	default:
		return errResp(req.ID, rpc.CodeMethodNotFound, "method not found: "+req.Method)
	}
}

// toolResult renders a tool outcome; user errors become isError results
// with the reason and the next step, so the agent can tell the user.
func toolResult(res any, err error) map[string]any {
	if err != nil {
		msg := err.Error()
		if ue, ok := valise.As(err); ok && ue != nil {
			msg = ue.Reason
			if ue.Suggestion != "" {
				msg += "\n下一步：" + ue.Suggestion
			}
		}
		return map[string]any{"content": []map[string]any{{"type": "text", "text": msg}}, "isError": true}
	}
	b, _ := json.MarshalIndent(res, "", "  ")
	out := map[string]any{"content": []map[string]any{{"type": "text", "text": string(b)}}}
	if m, ok := asObject(res); ok {
		out["structuredContent"] = m
	}
	return out
}

func asObject(v any) (map[string]any, bool) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, false
	}
	var m map[string]any
	return m, json.Unmarshal(b, &m) == nil && m != nil
}
