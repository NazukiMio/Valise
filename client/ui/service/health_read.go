package service

import (
	"context"
	"errors"

	"valise/core/action"
	"valise/core/agentkit"
	"valise/core/enroll"
	"valise/core/vaultrpc"
)

// HealthCheck separates coverage from the problems found by a check.
// Reason deliberately excludes raw remote errors and command output.
type HealthCheck struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	State  string `json:"state"` // ok, failed, unsupported
	Reason string `json:"reason,omitempty"`
}

// HealthReadError is safe for both human output and JSON. Remote errors
// may contain credentials; only locally identified causes are exposed.
func HealthReadError(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "检查已取消，请重新刷新"
	case errors.Is(err, context.DeadlineExceeded):
		return "读取超时，请检查服务器连接后刷新"
	default:
		return "读取失败，请检查服务器连接和 porter 版本后刷新"
	}
}

func (r *HealthReport) read(id, title string, err error) {
	check := HealthCheck{ID: id, Title: title, State: "ok"}
	if err != nil {
		check.State, check.Reason = "failed", HealthReadError(err)
	}
	r.Checks = append(r.Checks, check)
}

// Incomplete counts failed checks, independently of warning dismissals.
func (r *HealthReport) Incomplete() int {
	n := 0
	for _, c := range r.Checks {
		if c.State == "failed" {
			n++
		}
	}
	return n
}

// The sources are also used by fault-injection tests of partial results.
// Production uses the same RPC-backed services as the individual pages.
type healthSources struct {
	vault     func() (*vaultrpc.StatusResult, error)
	instances func() ([]enroll.InstanceSummary, error)
	timer     func() (*vaultrpc.TimerStatus, error)
	kit       func() (*agentkit.KitStatusResult, error)
	approval  func() (*action.ApprovalResult, error)
}
