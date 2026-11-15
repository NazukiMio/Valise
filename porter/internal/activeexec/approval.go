package activeexec

import (
	"bytes"
	"context"
	"io"
	"os"
	"strconv"
	"time"

	"valise/core/action"
	"valise/core/profile"
	"valise/core/valise"
	"valise/porter/internal/environments"
)

const approvalOutputLimit = 256 << 10

// Approval executes only the current programme's accepted approval_check.
// An unmanaged result is explicit so the server can retain the old consumer;
// an unknown managed binding must never reach that fallback.
func (c Controller) Approval(ctx context.Context, id string, lock *os.File) (*action.ApprovalResult, error) {
	if ctx == nil || lock == nil {
		return nil, unavailable()
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	b, err := c.bind(ctx, id)
	if err != nil {
		return nil, err
	}
	if b.current == nil {
		return nil, ErrUnmanaged
	}
	check := b.profile.ApprovalCheck
	if check == nil {
		return &action.ApprovalResult{}, nil
	}
	result := &action.ApprovalResult{Declared: true, Pending: check.Verify == "pending", Mode: profile.ApprovalUnknown}
	cmd, err := c.commandMode(ctx, b, check.Argv, lock, true)
	if err != nil {
		result.Detail = "当前程序无法以只读方式完成审批检查，请重新检查接入资料"
		return result, nil
	}
	output := boundedApprovalOutput{cancel: cancel}
	cmd.Stdout, cmd.Stderr = &output, io.Discard
	err = cmd.Run()
	switch {
	case output.exceeded:
		result.Detail = "检查结果超过读取上限，未能判定审批设置；请重新检查或更新接入资料"
	case ctx.Err() != nil:
		result.Detail = "审批检查中断或超时，未能判定设置；请稍后重新检查"
	case err != nil:
		result.Detail = "审批检查未能完成，请在原生界面检查设置后重试"
		if cmd.ProcessState != nil && cmd.ProcessState.ExitCode() >= 0 {
			result.Detail = "审批检查退出码 " + strconv.Itoa(cmd.ProcessState.ExitCode()) + "，未能判定设置；请在原生界面检查后重试"
		}
	default:
		result.Mode, _ = profile.EvalApproval(check, output.data.Bytes())
		result.Default = result.Mode == profile.ApprovalBypass && profile.ApprovalDefault(check, output.data.Bytes())
		if result.Mode == profile.ApprovalUnknown {
			result.Detail = "检查结果未匹配已登记的审批规则，请重新检查或更新接入资料"
		}
	}
	if err != nil || output.exceeded || ctx.Err() != nil {
		return result, nil // A failed check never supplies a positive observation.
	}
	// Keep observations from different revisions out of one result. Reopening
	// is read-only and also rechecks the original source during native A.
	fresh, bindErr := c.bind(ctx, id)
	if bindErr != nil || fresh.current == nil || environments.Digest(fresh.current.Registry) != environments.Digest(b.current.Registry) ||
		fresh.current.Ref != b.current.Ref || environments.Digest(fresh.current.Program) != environments.Digest(b.current.Program) ||
		environments.Digest(fresh.record) != environments.Digest(b.record) {
		return nil, valise.New("approval_current_changed", "无法再次核实当前环境，审批检查结果尚未确认", "重新检查这个 Agent，再查看原恢复操作是否需要处理")
	}
	return result, nil
}

// Do not embed bytes.Buffer: a promoted ReadFrom could bypass Write's limit.
type boundedApprovalOutput struct {
	data     bytes.Buffer
	exceeded bool
	cancel   context.CancelFunc
}

func (b *boundedApprovalOutput) Write(p []byte) (int, error) {
	n := len(p)
	if b.exceeded {
		return n, nil
	}
	remaining := approvalOutputLimit - b.data.Len()
	if n > remaining {
		_, _ = b.data.Write(p[:remaining])
		b.exceeded = true
		if b.cancel != nil {
			b.cancel()
		}
		return n, nil
	}
	return b.data.Write(p)
}
