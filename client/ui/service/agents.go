package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"sync"
	"time"

	"valise/client/config"
	"valise/core/enroll"
	"valise/core/identity"
	"valise/core/valise"
)

// AgentReader retains only disposable observations for this client process.
// A caller should keep one reader to display the last successful list offline.
type AgentReader struct {
	mu      sync.Mutex
	service *Service
	cache   map[string]AgentServerObservation
	now     func() time.Time
}

func NewAgentReader(s *Service) *AgentReader {
	return &AgentReader{service: s, cache: map[string]AgentServerObservation{}, now: time.Now}
}

type AgentListRequest struct {
	ConnectionIDs []string `json:"connection_ids,omitempty"`
}
type AgentSummary struct {
	ConnectionID  string                 `json:"connection_id"`
	Instance      enroll.InstanceSummary `json:"instance"`
	IdentityState string                 `json:"identity_state"`
	AgentRef      *identity.AgentRef     `json:"agent_ref,omitempty"`
	// Relations are the target-side relation summaries of this generation.
	Relations []identity.RelationSummary `json:"relations,omitempty"`
	// MigratedTo is set only when, in this same read, another server's
	// fresh generation records migration_completed from exactly AgentRef.
	MigratedTo *AgentMigration `json:"migrated_to,omitempty"`
}

// AgentMigration names where a source generation's journey continued.
type AgentMigration struct {
	ConnectionID string            `json:"connection_id"`
	AgentRef     identity.AgentRef `json:"agent_ref"`
	OperationRef string            `json:"operation_ref"`
}
type AgentServerObservation struct {
	ConnectionID  string            `json:"connection_id"`
	Value         *[]AgentSummary   `json:"value,omitempty"`
	Status        string            `json:"status"`
	ObservedAt    *time.Time        `json:"observed_at,omitempty"`
	LastAttemptAt time.Time         `json:"last_attempt_at"`
	Source        string            `json:"source"`
	Error         *valise.UserError `json:"error,omitempty"`
}
type AgentListResult struct {
	Items        []AgentSummary           `json:"items"`
	Observations []AgentServerObservation `json:"observations"`
	Partial      bool                     `json:"partial"`
}
type AgentGetRequest struct {
	ConnectionID string             `json:"connection_id,omitempty"`
	InstanceID   string             `json:"instance_id,omitempty"`
	AgentRef     *identity.AgentRef `json:"agent_ref,omitempty"`
}
type AgentObservation struct {
	Value         *AgentSummary     `json:"value,omitempty"`
	Status        string            `json:"status"`
	ObservedAt    *time.Time        `json:"observed_at,omitempty"`
	LastAttemptAt time.Time         `json:"last_attempt_at"`
	Source        string            `json:"source"`
	Error         *valise.UserError `json:"error,omitempty"`
}

func agentReadError(code string) *valise.UserError {
	reason, suggestion := "无法读取服务器上的 Agent 清单", "检查服务器连接后重新查询"
	switch code {
	case "identity_conflict":
		reason, suggestion = "不同连接返回了相同的服务器身份，尚未核实是否为同一服务器账户", "核对 SSH 主机信任和远端账户；不要按显示名合并"
	case "identity_needs_reconciliation":
		reason, suggestion = "上次接入提交尚未完成，身份需要核实", "核对接入提交记录后重新接受接入"
	}
	return valise.New(code, reason, suggestion)
}

// ServerReadTimeout bounds reading one server's agents.
var ServerReadTimeout = 30 * time.Second

// connCodes are SSH failures whose own reason and suggestion say more than
// the generic one.
var connCodes = map[string]bool{"ssh_connect_failed": true, "ssh_auth_failed": true, "host_key_mismatch": true, "ssh_not_runnable": true}

// readFailure keeps a connection failure's own code and words, says a
// timeout as one, and otherwise gives the generic reason.
func readFailure(host string, err error) *valise.UserError {
	if errors.Is(err, context.DeadlineExceeded) {
		return serverTimeout(host)
	}
	if ue, ok := valise.As(err); ok {
		switch {
		case connCodes[ue.Code]:
			return ue
		case ue.Code == "identity_conflict" || ue.Code == "identity_needs_reconciliation":
			return agentReadError(ue.Code)
		}
	}
	return agentReadError("agent_query_failed").WithDetail(err.Error())
}

func serverTimeout(host string) *valise.UserError {
	return valise.New("server_timeout", fmt.Sprintf("服务器 %s 在 %d 秒内没有回应", host, int(ServerReadTimeout/time.Second)),
		"检查服务器是否开机、网络是否通畅；刚换过网络的话，稍等片刻后重新读取")
}

// observationKey prevents stale values from being reused after a connection is
// repointed. SSH aliases alone do not establish remote identity or trust.
func (r *AgentReader) observationKey(s config.Server) string {
	cfg, _ := os.ReadFile(r.service.SSHConfigPath)
	b, _ := json.Marshal(s)
	sum := sha256.Sum256(append(b, cfg...))
	return s.Name + ":" + hex.EncodeToString(sum[:])
}

func (r *AgentReader) readServer(ctx context.Context, srv config.Server) ([]AgentSummary, error) {
	c, err := r.service.session(ctx, srv.Name)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	var list enroll.InstanceListResult
	if err := c.Call(ctx, enroll.MethodInstanceList, struct{}{}, &list); err != nil {
		return nil, err
	}
	items := make([]AgentSummary, 0, len(list.Instances))
	supported := c.hello != nil && slices.Contains(c.hello.Capabilities, identity.Capability)
	// identity/2 lists bindings with the instances; otherwise one identity_get
	// per instance. Neither read initializes an identity.
	listed := c.hello != nil && slices.Contains(c.hello.Capabilities, identity.ListCapability)
	for _, in := range list.Instances {
		listedBinding := in.Identity
		in.Identity = nil
		item := AgentSummary{ConnectionID: srv.Name, Instance: in, IdentityState: identity.LegacyUnbound}
		if supported || listed {
			var binding identity.Binding
			if listed {
				if listedBinding == nil {
					return nil, agentReadError("identity_unknown")
				}
				binding = *listedBinding
			} else if err := c.Call(ctx, identity.MethodGet, identity.GetParams{InstanceID: in.InstanceID}, &binding); err != nil {
				return nil, err
			}
			if err := applyBinding(&item, binding); err != nil {
				return nil, err
			}
		}
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Instance.InstanceID < items[j].Instance.InstanceID })
	return items, nil
}

func applyBinding(item *AgentSummary, binding identity.Binding) error {
	in := item.Instance
	item.IdentityState = binding.IdentityState
	switch binding.IdentityState {
	case identity.LegacyUnbound:
		if binding.Agent != nil || len(binding.Relations) > 0 {
			return agentReadError("identity_conflict")
		}
	case identity.Bound:
		if binding.Agent == nil {
			return agentReadError("identity_conflict")
		}
		ref := binding.Agent.Ref
		if ref.InstanceID != in.InstanceID || ref.ServerUID == "" || ref.Generation == "" || ref.InstallID == "" || (in.InstallID != "" && ref.InstallID != in.InstallID) {
			return agentReadError("identity_conflict")
		}
		for _, rel := range binding.Relations {
			if rel.TargetAgent != ref {
				return agentReadError("identity_conflict")
			}
		}
		item.AgentRef = &ref
		item.Relations = binding.Relations
	case identity.NeedsReconciliation:
		return agentReadError("identity_needs_reconciliation")
	default:
		return agentReadError("identity_unknown")
	}
	return nil
}

// annotateMigrations marks a source generation as migrated only from two
// fresh reads of different connections and an exact four-field AgentRef
// match. Names, instance ids alone and stale or unreachable servers are
// never evidence.
func annotateMigrations(observations []AgentServerObservation) {
	type located struct {
		connection string
		item       *AgentSummary
	}
	var fresh []located
	for i := range observations {
		obs := &observations[i]
		if obs.Status != "fresh" || obs.Value == nil {
			continue
		}
		for j := range *obs.Value {
			item := &(*obs.Value)[j]
			if item.AgentRef != nil && item.IdentityState == identity.Bound {
				fresh = append(fresh, located{obs.ConnectionID, item})
			}
		}
	}
	for _, target := range fresh {
		for _, rel := range target.item.Relations {
			if rel.Kind != identity.MigrationCompleted || rel.SourceAgent == nil || rel.TargetAgent != *target.item.AgentRef {
				continue
			}
			for _, source := range fresh {
				if source.connection == target.connection || *source.item.AgentRef != *rel.SourceAgent {
					continue
				}
				source.item.MigratedTo = &AgentMigration{ConnectionID: target.connection, AgentRef: rel.TargetAgent, OperationRef: rel.OperationRef}
			}
		}
	}
}

// ListAgents reads each selected server separately. Failure preserves its last
// successful value as stale, without discarding successes on other servers.
func (r *AgentReader) ListAgents(ctx context.Context, req AgentListRequest) (AgentListResult, error) {
	if err := ctx.Err(); err != nil {
		return AgentListResult{}, err
	}
	servers, err := r.service.ServerList()
	if err != nil {
		return AgentListResult{}, err
	}
	wanted := map[string]bool{}
	for _, id := range req.ConnectionIDs {
		wanted[id] = true
	}
	selected := make([]config.Server, 0, len(servers))
	for _, srv := range servers {
		if len(wanted) == 0 || wanted[srv.Name] {
			selected = append(selected, srv)
		}
	}
	for id := range wanted {
		if !slices.ContainsFunc(selected, func(s config.Server) bool { return s.Name == id }) {
			return AgentListResult{}, valise.New("server_unknown", "所选服务器连接未登记", "从服务器列表重新选择")
		}
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].Name < selected[j].Name })
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return AgentListResult{}, err
	}
	observations := make([]AgentServerObservation, len(selected))
	keys := make([]string, len(selected))
	var wg sync.WaitGroup
	slots := make(chan struct{}, 4)
	for i, srv := range selected {
		keys[i] = r.observationKey(srv)
		wg.Add(1)
		go func(i int, srv config.Server) {
			defer wg.Done()
			at := r.now().UTC()
			obs := AgentServerObservation{ConnectionID: srv.Name, Status: "unknown", LastAttemptAt: at, Source: srv.Name}
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				obs.Error = serverTimeout(srv.Name)
				observations[i] = obs
				return
			}
			defer func() { <-slots }()
			sctx, cancel := context.WithTimeout(ctx, ServerReadTimeout)
			values, err := r.readServer(sctx, srv)
			cancel()
			if err != nil {
				obs.Error = readFailure(srv.Name, err)
			} else {
				obs.Status, obs.Value, obs.ObservedAt = "fresh", &values, &at
			}
			observations[i] = obs
		}(i, srv)
	}
	// One server that does not answer never hides the others: each is
	// bounded on its own, and what was read is kept when time runs out.
	wg.Wait()
	for i := range observations {
		obs := &observations[i]
		if obs.Status == "fresh" {
			r.cache[keys[i]] = *obs
			continue
		}
		if old, ok := r.cache[keys[i]]; ok {
			obs.Value, obs.ObservedAt, obs.Status = old.Value, old.ObservedAt, "stale"
		}
	}
	// A repeated uid is evidence requiring trust verification, never permission
	// to coalesce two connections (including apparent copies of a system disk).
	uidConnections := map[string]map[string]bool{}
	for _, obs := range observations {
		if obs.Value == nil {
			continue
		}
		for _, item := range *obs.Value {
			if item.AgentRef == nil {
				continue
			}
			uid := item.AgentRef.ServerUID
			if uidConnections[uid] == nil {
				uidConnections[uid] = map[string]bool{}
			}
			uidConnections[uid][item.ConnectionID] = true
		}
	}
	// Clone before annotating conflicts, so they do not poison cached observations.
	observations = cloneAgentObservations(observations)
	result := AgentListResult{Items: []AgentSummary{}, Observations: observations}
	for i := range result.Observations {
		obs := &result.Observations[i]
		if obs.Status != "fresh" {
			result.Partial = true
		}
		if obs.Value == nil {
			continue
		}
		for j := range *obs.Value {
			item := &(*obs.Value)[j]
			if item.AgentRef != nil && len(uidConnections[item.AgentRef.ServerUID]) > 1 {
				item.IdentityState = "identity_conflict"
				obs.Error = agentReadError("identity_conflict")
				result.Partial = true
			}
		}
	}
	annotateMigrations(result.Observations)
	for _, obs := range result.Observations {
		if obs.Value != nil {
			result.Items = append(result.Items, *obs.Value...)
		}
	}
	return result, nil
}

func cloneAgentObservations(in []AgentServerObservation) []AgentServerObservation {
	b, _ := json.Marshal(in)
	var out []AgentServerObservation
	_ = json.Unmarshal(b, &out)
	return out
}

// GetAgent requires exact generation equality when AgentRef is supplied. A
// reused instance id can never satisfy a request for its previous generation.
func (r *AgentReader) GetAgent(ctx context.Context, req AgentGetRequest) (AgentObservation, error) {
	if req.AgentRef == nil && (req.ConnectionID == "" || req.InstanceID == "") {
		return AgentObservation{}, valise.New("agent_ref_required", "Agent 定位信息不完整", "选择服务器与实例，或提供完整的 AgentRef")
	}
	if req.AgentRef != nil && (req.AgentRef.ServerUID == "" || req.AgentRef.InstallID == "" || req.AgentRef.InstanceID == "" || req.AgentRef.Generation == "") {
		return AgentObservation{}, valise.New("agent_ref_required", "AgentRef 不完整", "重新选择已接入的 Agent")
	}
	listReq := AgentListRequest{}
	if req.ConnectionID != "" {
		listReq.ConnectionIDs = []string{req.ConnectionID}
	}
	result, err := r.ListAgents(ctx, listReq)
	if err != nil {
		return AgentObservation{}, err
	}
	var found *AgentObservation
	for _, obs := range result.Observations {
		if obs.Value == nil {
			continue
		}
		for _, item := range *obs.Value {
			if req.InstanceID != "" && item.Instance.InstanceID != req.InstanceID {
				continue
			}
			if req.AgentRef != nil && (item.AgentRef == nil || *item.AgentRef != *req.AgentRef) {
				continue
			}
			if item.IdentityState == "identity_conflict" {
				return AgentObservation{}, agentReadError("identity_conflict")
			}
			if found != nil {
				return AgentObservation{}, agentReadError("identity_conflict")
			}
			value := item
			found = &AgentObservation{Value: &value, Status: obs.Status, ObservedAt: obs.ObservedAt, LastAttemptAt: obs.LastAttemptAt, Source: obs.Source, Error: obs.Error}
		}
	}
	if found != nil {
		return *found, nil
	}
	if result.Partial {
		return AgentObservation{}, valise.New("result_unknown", "部分服务器无法读取，不能确认此 Agent 是否仍存在", "检查连接后重新查询")
	}
	return AgentObservation{}, valise.New("agent_not_found", "没有找到匹配该身份与世代的 Agent", "重新查看当前 Agent 清单；不要沿用已被取代的实例引用")
}
