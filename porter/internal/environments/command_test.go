package environments

import (
	"context"
	"testing"
)

func TestAcceptedCommandResolvesOnlyPrivateProgrammeGraph(t *testing.T) {
	s, agent, in, archive := bindingFixture(t)
	_, b, err := s.BuildOriginal(context.Background(), agent, originalInput(in, archive))
	if err != nil {
		t.Fatal(err)
	}
	for _, argv := range [][]string{{"agent", "--fixed"}, {"${STATE_DIR}/program/agent", "${HOME}"}} {
		got, err := Command(b, argv)
		if err != nil || got.Argv[0] != b.Layout.Roots["state"]+"/program/agent" || got.Home != b.Layout.Home || got.WorkingDir != b.Spec.WorkingDir {
			t.Fatal("accepted private command did not resolve", got, err)
		}
	}
	for _, argv := range [][]string{nil, {"/bin/sh"}, {"../program/agent"}, {"${STATE_DIR}/memory/agent"}, {"agent", "${env:SECRET}"}, {"agent", "bad\x00value"}} {
		if got, err := Command(b, argv); err == nil || len(got.Argv) != 0 {
			t.Fatal("command escaped fixed private programme", argv, got, err)
		}
	}
}
