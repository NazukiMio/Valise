package service

import (
	"testing"

	"valise/core/action"
	"valise/core/enroll"
	"valise/core/profile"
	"valise/core/rpc"
)

func TestTransactionCompatibilityBeforeEnrollmentOrExecution(t *testing.T) {
	old := &rpc.HelloResult{Protocol: "1.2"}
	current := &rpc.HelloResult{Protocol: "1.3", Capabilities: []string{action.CapabilityTransactions}}
	for _, tt := range []struct {
		method string
		params any
	}{
		{action.MethodPlan, action.PlanParams{}},
		{action.MethodStatus, action.OperationParams{}},
		{action.MethodResume, action.ResumeParams{}},
		{action.MethodCleanup, action.CleanupParams{}},
		{action.MethodRun, action.RunParams{PlanDigest: "confirmed-plan"}},
		{enroll.MethodInstanceWrite, enroll.InstanceWriteParams{Profile: profile.Profile{Actions: []profile.Action{{Transaction: &profile.ActionTransaction{Root: "state"}}}}}},
	} {
		t.Run(tt.method, func(t *testing.T) {
			if err := transactionCompatibility(old, tt.method, tt.params); err == nil {
				t.Fatal("old porter accepted transaction request")
			}
			if err := transactionCompatibility(current, tt.method, tt.params); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, method := range []string{action.MethodRun, enroll.MethodInstanceWrite, "instance_list"} {
		if err := transactionCompatibility(old, method, struct{}{}); err != nil {
			t.Fatal("legacy operation rejected", method, err)
		}
	}
}
