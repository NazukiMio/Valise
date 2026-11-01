package forced

import (
	"slices"
	"testing"
)

func TestDaily(t *testing.T) {
	ok := map[string][]string{
		`'$HOME/.local/bin/valise-porter' rpc`:                      {"rpc"},
		`"$HOME/.local/bin/valise-porter" version --json`:           {"version", "--json"},
		`/home/u/.local/bin/valise-porter vault-cat 'a/2026' 'x y'`: {"vault-cat", "a/2026", "x y"},
		`valise-porter rpc`: {"rpc"},
	}
	for in, want := range ok {
		got, err := Daily(in)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("Daily(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{
		"", "sh", "bash -c id", "valise-porter", "valise-porter stage-write --op x",
		"valise-porter login-run s", "valise-porter rpc; id", "valise-porter rpc | sh",
		"env A=1 valise-porter rpc", "/tmp/evil rpc", "valise-porter 'rpc", "valise-porter $(id)",
		"valise-porter mcp --instance a", "valise-porter seal-timer --instance a",
	}
	for _, in := range bad {
		if got, err := Daily(in); err == nil {
			t.Errorf("Daily(%q) = %q, want refusal", in, got)
		}
	}
}
