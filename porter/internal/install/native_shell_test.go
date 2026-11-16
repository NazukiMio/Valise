package install

import (
	"strings"
	"testing"
)

// The instance shell is the user's own shell as an interactive login
// shell; a POSIX shell puts the instance's PATH back after its profile.
// The command is fixed: the shell's path is its only variable part.
func TestLoginShellCommand(t *testing.T) {
	for _, sh := range []string{"/bin/sh", "/bin/bash", "/usr/bin/zsh", "/bin/dash"} {
		got := loginShell(sh)
		if len(got) != 5 || got[0] != sh || got[1] != "-l" || got[2] != "-c" || got[4] != sh ||
			!strings.Contains(got[3], `PATH="$VALISE_INSTANCE_PATH:$PATH"`) || !strings.HasSuffix(got[3], `exec "$0" -i`) {
			t.Fatalf("%s: %q", sh, got)
		}
	}
	if got := strings.Join(loginShell("/usr/bin/fish"), " "); got != "/usr/bin/fish -l -i" {
		t.Fatalf("fish: %q", got)
	}
	if got := strings.Join(loginShell("/opt/odd/shell"), " "); got != "/opt/odd/shell -i" {
		t.Fatalf("unknown shell: %q", got)
	}
}
