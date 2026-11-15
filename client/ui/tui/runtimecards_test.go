package tui

import (
	"strings"
	"testing"
	"valise/core/enroll"
)

func TestRuntimeCardsDoNotTreatUnknownManagedAsForegroundOrStopped(t *testing.T) {
	in := enroll.InstanceSummary{Runtime: &enroll.RuntimeState{}}
	for _, text := range []string{runState(in), runStateShort(in)} {
		if !strings.Contains(text, "尚未核实") || strings.Contains(text, "前台运行") || strings.Contains(text, "已停止") {
			t.Fatal(text)
		}
	}
	in.Runtime.Known, in.Runtime.Running = true, true
	if !strings.Contains(runStateShort(in), "运行中") {
		t.Fatal(runStateShort(in))
	}
}
