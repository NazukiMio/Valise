package install

import (
	"reflect"
	"testing"
)

func TestParseCustomInstallValid(t *testing.T) {
	cases := []struct {
		name   string
		raw    string
		kind   string
		url    string
		interp []string
		args   []string
		sudo   bool
		argv   []string
	}{
		{
			name: "bare_https", raw: "  https://openclaw.ai/install-cli.sh  ",
			kind: "script", url: "https://openclaw.ai/install-cli.sh", interp: []string{"bash"},
		},
		{
			name: "curl_pipe_bash", raw: "curl -fsSL https://openclaw.ai/install.sh | bash",
			kind: "script", url: "https://openclaw.ai/install.sh", interp: []string{"bash"},
		},
		{
			name: "curl_pipe_bash_args", raw: "curl -fsSL https://openclaw.ai/install-cli.sh | bash -s -- --version 2026.10.9 --no-onboard",
			kind: "script", url: "https://openclaw.ai/install-cli.sh", interp: []string{"bash"}, args: []string{"--version", "2026.10.9", "--no-onboard"},
		},
		{
			name: "curl_tls_query_sh", raw: "curl --proto '=https' --tlsv1.2 -LsSf 'https://example.com/install.sh?a=1&b=2' | sh -s -- --yes",
			kind: "script", url: "https://example.com/install.sh?a=1&b=2", interp: []string{"sh"}, args: []string{"--yes"},
		},
		{
			name: "curl_pipe_sudo_bash", raw: "curl -fsSL https://example.com/install.sh | sudo -E bash -s -- --prefix=/opt",
			kind: "script", url: "https://example.com/install.sh", interp: []string{"bash"}, args: []string{"--prefix=/opt"}, sudo: true,
		},
		{
			name: "wget_pipe_sh", raw: "wget -qO- https://example.com/setup.sh | sh",
			kind: "script", url: "https://example.com/setup.sh", interp: []string{"sh"},
		},
		{
			name: "proc_sub", raw: "bash <(curl -fsSL https://example.com/install.sh) -- --flag",
			kind: "script", url: "https://example.com/install.sh", interp: []string{"bash"}, args: []string{"--flag"},
		},
		{
			name: "line_continuation", raw: "curl -fsSL https://example.com/install.sh \\\n  | bash -s -- --fast",
			kind: "script", url: "https://example.com/install.sh", interp: []string{"bash"}, args: []string{"--fast"},
		},
		{
			name: "npm_global", raw: "npm install -g openclaw@2026.10.9",
			kind: "argv", argv: []string{"npm", "install", "-g", "openclaw@2026.10.9"},
		},
	}
	for _, tc := range cases {
		spec, err := ParseCustomInstall(tc.raw)
		if err != nil {
			t.Fatalf("%s: unexpected error %v", tc.name, err)
		}
		if spec.Kind != tc.kind || spec.URL != tc.url || spec.Sudo != tc.sudo ||
			!reflect.DeepEqual(spec.Interpreter, tc.interp) ||
			!reflect.DeepEqual(spec.Args, tc.args) ||
			!reflect.DeepEqual(spec.Argv, tc.argv) {
			t.Errorf("%s: got %+v", tc.name, spec)
		}
	}
}

func TestParseCustomInstallRejected(t *testing.T) {
	bad := []struct {
		name string
		raw  string
	}{
		{"empty", "   "},
		{"http_bare", "http://example.com/install.sh"},
		{"http_curl", "curl -fsSL http://example.com/install.sh | bash"},
		{"cmd_sub", "bash -c \"$(curl -fsSL https://example.com/install.sh)\""},
		{"backtick", "curl -fsSL https://example.com/install.sh | bash -s -- `id`"},
		{"base64", "echo aGk= | base64 -d | bash"},
		{"insecure_curl", "curl -k https://example.com/install.sh | bash"},
		{"chained", "curl -fsSL https://example.com/install.sh | bash; rm -rf /"},
		{"multiline", "curl -fsSL https://example.com/install.sh | bash\nid"},
		{"multi_pipe", "curl -fsSL https://example.com/install.sh | tee x | bash"},
		{"bash_c", "curl -fsSL https://example.com/install.sh | bash -c 'evil'"},
		{"output_file", "curl -o /tmp/x https://example.com/install.sh | bash"},
		{"arbitrary_cmd", "rm -rf /tmp/foo"},
		{"unclosed_quote", "curl -fsSL 'https://example.com/install.sh | bash"},
	}
	for _, tc := range bad {
		if spec, err := ParseCustomInstall(tc.raw); err == nil {
			t.Errorf("%s: expected error, got %+v", tc.name, spec)
		}
	}
}
