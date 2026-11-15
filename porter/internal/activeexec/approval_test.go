package activeexec

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestApprovalOutputLimitCannotBeBypassedByCopy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	b := boundedApprovalOutput{cancel: cancel}
	n, err := io.Copy(&b, strings.NewReader(strings.Repeat("x", approvalOutputLimit+10)))
	if err != nil || n != approvalOutputLimit+10 || !b.exceeded || b.data.Len() != approvalOutputLimit || ctx.Err() == nil {
		t.Fatal("unbounded output or missing cancellation", n, b.data.Len(), err)
	}
	if _, err := b.Write([]byte("more")); err != nil || b.data.Len() != approvalOutputLimit {
		t.Fatal("overflow continued growing", b.data.Len(), err)
	}
}
