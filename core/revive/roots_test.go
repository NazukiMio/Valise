package revive

import (
	"reflect"
	"testing"
)

func TestCanonicalRootsUseOutermostParentAndStableOrder(t *testing.T) {
	paths := map[string]string{"deep": "/home/user/state/workspace/deep", "workspace": "/home/user/state/workspace", "external": "/home/user/outside", "state": "/home/user/state"}
	want := []RootPlan{{ID: "state", Path: paths["state"]}, {ID: "deep", Path: paths["deep"], SwapRoot: "state", Rel: "workspace/deep"}, {ID: "external", Path: paths["external"]}, {ID: "workspace", Path: paths["workspace"], SwapRoot: "state", Rel: "workspace"}}
	for range 50 {
		got, err := CanonicalRoots(paths)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatal(got, err)
		}
	}
}

func TestCanonicalRootsRejectAliasesAndUncleanPaths(t *testing.T) {
	for _, paths := range []map[string]string{
		{}, {"state": "/home/state", "workspace": "/home/state"}, {"state": "relative"}, {"state": "/home/../state"}, {"state": "/"}, {"../bad": "/home/state"}, {"state": "/home/state\nother"},
	} {
		if _, err := CanonicalRoots(paths); err == nil {
			t.Fatal("ambiguous layout accepted", paths)
		}
	}
}
