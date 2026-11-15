package revive

import (
	"context"
	"os"
	"strconv"

	"valise/core/environment"
	"valise/core/revive"
	"valise/core/valise"
)

type preparationReuse struct {
	Target   environment.Ref `json:"target"`
	Owners   []reuseOwner    `json:"owners"`
	Items    []reuseItem     `json:"items"`
	Complete bool            `json:"complete"`
}
type reuseOwner struct {
	Operation    string          `json:"operation"`
	Target       environment.Ref `json:"target"`
	Accepted     bool            `json:"accepted"`
	ProbeStopped bool            `json:"probe_stopped"`
}
type reuseItem struct {
	Owner     string             `json:"owner"`
	Kind      string             `json:"kind"`
	Root      string             `json:"root,omitempty"`
	Identity  revive.ManagedRoot `json:"identity"`
	Tree      string             `json:"tree,omitempty"`
	EmptyOnly bool               `json:"empty_only,omitempty"`
	Intent    bool               `json:"intent,omitempty"`
	Removed   bool               `json:"removed,omitempty"`
}

func reuseError() error {
	return valise.New("environment_reuse_unverified", "无法核实另一环境中将被覆盖的材料，没有继续准备", "当前环境仍保留；重新读取原操作和恢复条件，核实后再准备")
}

func (i reuseItem) key() string { return i.Owner + ":" + i.Kind + ":" + i.Root }

func reuseItemPath(home string, agent environment.PreparationParams, item reuseItem) (string, error) {
	p, err := loadPreparation(home, agent.Agent, item.Owner)
	if err != nil {
		return "", err
	}
	if item.Kind == "old-data" || item.Kind == "failed-data" {
		j, err := (Store{Home: home}).Load(p.Plan.OpID)
		if err != nil || j.ManagedData == nil || j.ManagedData.OperationID != p.OperationID {
			return "", reuseError()
		}
		for _, root := range j.Roots {
			if root.ID == item.Root {
				if item.Kind == "old-data" {
					return root.Prev, nil
				}
				return root.Failed, nil
			}
		}
		return "", reuseError()
	}
	return preparationResource(home, p, item.Kind, item.Root)
}

func reuseDataKind(kind string) bool {
	return kind == "old-data" || kind == "failed-data" || kind == "stage"
}

func (r *Runner) protectReuse(ctx context.Context, p *EnvironmentPreparation, item reuseItem, path string) error {
	a := NewBoundActivationRunner(r)
	kind := "program"
	if reuseDataKind(item.Kind) {
		kind = "data"
	} else if item.Kind == "workspace" {
		kind = "preparation"
	}
	return a.protectOldCleanup(ctx, &ActivationRecord{Plan: ActivationPlan{Agent: p.Params.Agent, Candidate: p.Original}}, path, kind)
}

func inspectReuse(path string, item reuseItem) (bool, error) {
	claim := item.Identity
	claim.ID = item.key()
	fact, err := managedRootIdentity(item.key(), path)
	if os.IsNotExist(err) && (item.Intent || item.Removed) {
		return false, nil
	}
	if err != nil || item.Removed || fact != claim {
		return false, reuseError()
	}
	if item.EmptyOnly {
		entries, err := os.ReadDir(path)
		if err != nil || len(entries) != 0 {
			return false, reuseError()
		}
	}
	return true, nil
}

func (r *Runner) reuseCleanupItem(p *EnvironmentPreparation, root string) (reuseItem, string, error) {
	index, err := strconv.Atoi(root)
	if err != nil || p.Reuse == nil || index < 0 || index >= len(p.Reuse.Items) || strconv.Itoa(index) != root {
		return reuseItem{}, "", reuseError()
	}
	item := p.Reuse.Items[index]
	path, err := reuseItemPath(r.Store.Home, environment.PreparationParams{Agent: p.Params.Agent}, item)
	return item, path, err
}
