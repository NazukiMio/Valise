package install

import (
	"net/url"
	"path"
	"regexp"
	"strings"

	"valise/core/valise"
)

// CustomInstallSpec is a parsed user-pasted official install URL or
// one-line install command (docs/INSTALL.md section 7). Scripts are always
// downloaded by the client first and never executed through curl|bash on
// the server.
type CustomInstallSpec struct {
	Raw         string   `json:"raw"`
	Kind        string   `json:"kind"` // "script" or "argv"
	URL         string   `json:"url,omitempty"`
	Interpreter []string `json:"interpreter,omitempty"`
	Args        []string `json:"args,omitempty"`
	Sudo        bool     `json:"sudo,omitempty"`
	Argv        []string `json:"argv,omitempty"`
}

var (
	reEnvName    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
	allowedShell = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true}
)

// ParseCustomInstall parses an official HTTPS script URL, a common
// downloader|shell pipeline, a process-substitution command like
// `bash <(curl -fsSL https://...)`, or a direct `npm install` command.
// It rejects plaintext http://, command substitutions, obfuscation,
// chained commands, and insecure TLS flags.
func ParseCustomInstall(raw string) (*CustomInstallSpec, error) {
	norm, err := normalizeCustomInput(raw)
	if err != nil {
		return nil, err
	}
	if strings.Contains(norm, "$(") || strings.Contains(norm, "`") {
		return nil, valise.New("install_custom_refused", "指令里包含命令替换，无法在客户端先下载并核对实际内容",
			"请改用直接的官方 HTTPS 安装脚本地址，或去除命令替换后重试")
	}
	if reDecode.MatchString(norm) || reBase64Extra.MatchString(norm) {
		return nil, valise.New("install_custom_refused", "指令包含编码或解码执行，Valise 一律不执行",
			"只支持直接的官方 HTTPS 安装脚本地址或标准安装指令")
	}
	if reForkBomb.MatchString(norm) {
		return nil, valise.New("install_custom_refused", "检测到有害指令，Valise 一律不执行", "")
	}

	toks, unclosed := tokenizeShell(norm)
	if unclosed {
		return nil, valise.New("install_custom_invalid", "指令里的引号没有成对闭合", "检查引号是否完整后重试")
	}
	if len(toks) == 0 {
		return nil, valise.New("install_custom_empty", "请先输入官方安装脚本地址或安装指令", "支持 https://... 脚本地址、curl ... | bash 等常见一行指令")
	}

	// Single bare URL (even if preceded by nothing).
	if len(toks) == 1 && toks[0].op == "" && looksLikeURL(toks[0].text) {
		u, err := validateHTTPSURL(toks[0].text)
		if err != nil {
			return nil, err
		}
		return &CustomInstallSpec{Raw: norm, Kind: "script", URL: u, Interpreter: []string{"bash"}}, nil
	}

	for _, tk := range toks {
		if tk.op == "" && strings.Contains(strings.ToLower(tk.text), "http://") {
			return nil, insecureURLError(tk.text)
		}
		if tk.op != "" && tk.op != "|" && tk.op != "<(" && tk.op != ")" {
			return nil, valise.New("install_custom_unsupported", "暂不支持包含「"+tk.op+"」的多条或重定向命令",
				"请只粘贴单条 HTTPS 脚本地址、curl ... | bash、bash <(curl ...) 或 npm install 命令")
		}
	}

	pipeIdx := findPipeIndex(toks)
	procIdx := findOpIndex(toks, "<(")
	switch {
	case pipeIdx >= 0 && procIdx >= 0:
		return nil, valise.New("install_custom_unsupported", "暂不支持同时包含管道和进程替换的复合命令",
			"请只粘贴单条官方 HTTPS 脚本地址或标准一行安装指令")
	case pipeIdx >= 0:
		return parsePipeInstall(norm, toks, pipeIdx)
	case procIdx >= 0:
		return parseProcSubInstall(norm, toks, procIdx)
	default:
		return parseDirectArgvInstall(norm, toks)
	}
}

func normalizeCustomInput(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", valise.New("install_custom_empty", "请先输入官方安装脚本地址或安装指令", "支持 https://... 脚本地址、curl ... | bash 等常见一行指令")
	}
	lines := strings.Split(s, "\n")
	if len(lines) == 1 {
		return strings.TrimSuffix(lines[0], "\r"), nil
	}
	var b strings.Builder
	for i, ln := range lines {
		ln = strings.TrimSpace(strings.TrimSuffix(ln, "\r"))
		if ln == "" {
			continue
		}
		if hasLineContinuation(ln) {
			b.WriteString(strings.TrimSuffix(ln, "\\"))
			b.WriteByte(' ')
			continue
		}
		if i < len(lines)-1 && strings.HasSuffix(ln, "|") {
			b.WriteString(ln)
			b.WriteByte(' ')
			continue
		}
		if b.Len() > 0 && i == len(lines)-1 {
			b.WriteString(ln)
			continue
		}
		if i < len(lines)-1 {
			return "", valise.New("install_custom_multiline", "暂不支持一次输入多行多条命令", "请只粘贴单条官方安装地址或单条安装指令")
		}
		b.WriteString(ln)
	}
	return strings.TrimSpace(b.String()), nil
}

func findPipeIndex(toks []shellTok) int {
	idx := -1
	for i, tk := range toks {
		if tk.op == "|" {
			if idx >= 0 {
				return -2 // multiple pipes
			}
			idx = i
		}
	}
	if idx == -2 {
		return -2
	}
	return idx
}

func findOpIndex(toks []shellTok, op string) int {
	for i, tk := range toks {
		if tk.op == op {
			return i
		}
	}
	return -1
}

func wordsOnly(toks []shellTok) ([]string, bool) {
	words := make([]string, 0, len(toks))
	for _, tk := range toks {
		if tk.op != "" {
			return nil, false
		}
		words = append(words, tk.text)
	}
	return words, true
}

func parsePipeInstall(norm string, toks []shellTok, pipeIdx int) (*CustomInstallSpec, error) {
	if pipeIdx < 0 {
		return nil, valise.New("install_custom_unsupported", "暂不支持包含多级管道的安装命令", "只支持单个 downloader | shell 形式，或直接粘贴 HTTPS 脚本地址")
	}
	leftWords, ok1 := wordsOnly(toks[:pipeIdx])
	rightWords, ok2 := wordsOnly(toks[pipeIdx+1:])
	if !ok1 || !ok2 || len(leftWords) == 0 || len(rightWords) == 0 {
		return nil, valise.New("install_custom_unsupported", "无法拆解这条管道命令", "请检查格式是否为 curl ... https://... | bash")
	}
	dlURL, dlSudo, err := parseDownloader(leftWords)
	if err != nil {
		return nil, err
	}
	interp, args, shSudo, err := parseShellTarget(rightWords)
	if err != nil {
		return nil, err
	}
	return &CustomInstallSpec{
		Raw: norm, Kind: "script", URL: dlURL,
		Interpreter: interp, Args: args, Sudo: dlSudo || shSudo,
	}, nil
}

func parseProcSubInstall(norm string, toks []shellTok, procIdx int) (*CustomInstallSpec, error) {
	closeIdx := -1
	for i := procIdx + 1; i < len(toks); i++ {
		if toks[i].op == ")" {
			closeIdx = i
			break
		}
		if toks[i].op != "" {
			return nil, valise.New("install_custom_unsupported", "无法拆解进程替换里的命令", "请检查格式是否为 bash <(curl -fsSL https://...)")
		}
	}
	if closeIdx <= procIdx+1 {
		return nil, valise.New("install_custom_unsupported", "进程替换括号内缺少下载命令", "请检查格式是否为 bash <(curl -fsSL https://...)")
	}
	shellWords, ok1 := wordsOnly(toks[:procIdx])
	dlWords, ok2 := wordsOnly(toks[procIdx+1 : closeIdx])
	postWords, ok3 := wordsOnly(toks[closeIdx+1:])
	if !ok1 || !ok2 || !ok3 || len(shellWords) == 0 {
		return nil, valise.New("install_custom_unsupported", "无法拆解进程替换命令", "请检查格式是否为 bash <(curl -fsSL https://...)")
	}
	dlURL, dlSudo, err := parseDownloader(dlWords)
	if err != nil {
		return nil, err
	}
	interp, shArgs, shSudo, err := parseShellTarget(append(shellWords, postWords...))
	if err != nil {
		return nil, err
	}
	return &CustomInstallSpec{
		Raw: norm, Kind: "script", URL: dlURL,
		Interpreter: interp, Args: shArgs, Sudo: dlSudo || shSudo,
	}, nil
}

func parseDownloader(words []string) (rawURL string, sudo bool, err error) {
	rest := words
	for len(rest) > 0 && reEnvName.MatchString(rest[0]) {
		rest = rest[1:]
	}
	if len(rest) > 0 && path.Base(rest[0]) == "sudo" {
		sudo = true
		rest = skipOptions(rest[1:])
	}
	if len(rest) == 0 || !downloaders[path.Base(rest[0])] {
		return "", false, valise.New("install_custom_unsupported", "管道左侧只支持 curl、wget 或 fetch 下载官方安装脚本",
			"请粘贴官方提供的 https://... 地址或 curl/wget 安装命令")
	}
	args := rest[1:]
	skipNext := false
	var urls []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if skipNext {
			skipNext = false
			continue
		}
		if a == "-k" || a == "--insecure" || a == "--no-check-certificate" {
			return "", false, valise.New("install_custom_refused", "指令禁用了 HTTPS 证书校验（"+a+"），Valise 拒绝执行",
				"请去掉禁用证书校验的参数后重试")
		}
		if a == "-K" || a == "--config" || a == "-i" && path.Base(rest[0]) == "wget" || a == "--input-file" {
			return "", false, valise.New("install_custom_refused", "不支持从外部文件读取下载列表或配置（"+a+"）", "请直接写出官方 HTTPS 下载地址")
		}
		if a == "-o" || a == "--output" || a == "--output-document" || (path.Base(rest[0]) == "wget" && a == "-O") {
			if i+1 >= len(args) || args[i+1] != "-" {
				return "", false, valise.New("install_custom_unsupported", "下载命令写入了本地文件而不是标准输出，无法拆解", "请只粘贴官方 HTTPS 脚本地址或标准 curl ... | bash 指令")
			}
			skipNext = true
			continue
		}
		if isDownloaderValueFlag(a) {
			skipNext = true
			continue
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		if looksLikeURL(a) {
			u, err := validateHTTPSURL(a)
			if err != nil {
				return "", false, err
			}
			urls = append(urls, u)
		} else {
			return "", false, valise.New("install_custom_unsupported", "下载命令里包含未知参数或非 HTTPS 地址："+a,
				"请只保留官方 HTTPS 脚本地址和常见下载参数")
		}
	}
	if len(urls) != 1 {
		return "", false, valise.New("install_custom_unsupported", "下载命令必须明确给出唯一一个官方 HTTPS 地址",
			"请粘贴完整的 https://... 地址或 curl -fsSL https://... | bash")
	}
	return urls[0], sudo, nil
}

func isDownloaderValueFlag(a string) bool {
	if strings.Contains(a, "=") {
		return false
	}
	switch a {
	case "-H", "--header", "-A", "--user-agent", "--proto", "--proto-redir",
		"--connect-timeout", "--max-time", "-m", "--retry", "--retry-delay", "-u", "--user",
		"-e", "--referer", "--resolve", "--ciphers":
		return true
	}
	return false
}

func parseShellTarget(words []string) (interp []string, args []string, sudo bool, err error) {
	rest := words
	var envAssigns []string
	for len(rest) > 0 {
		base := path.Base(rest[0])
		switch {
		case base == "sudo":
			sudo = true
			rest = skipOptions(rest[1:])
		case base == "env":
			rest = rest[1:]
			for len(rest) > 0 && (reEnvName.MatchString(rest[0]) || strings.HasPrefix(rest[0], "-")) {
				if reEnvName.MatchString(rest[0]) {
					envAssigns = append(envAssigns, rest[0])
				}
				rest = rest[1:]
			}
		case reEnvName.MatchString(rest[0]):
			envAssigns = append(envAssigns, rest[0])
			rest = rest[1:]
		default:
			goto shellDone
		}
	}
shellDone:
	if len(rest) == 0 || !allowedShell[path.Base(rest[0])] {
		return nil, nil, false, valise.New("install_custom_unsupported", "目前只支持交由 sh、bash、zsh、dash 或 ksh 运行官方安装脚本",
			"也可以直接只粘贴 https://... 脚本地址，由 Valise 用 bash 执行")
	}
	sh := path.Base(rest[0])
	if len(envAssigns) > 0 {
		interp = append(append([]string{"env"}, envAssigns...), sh)
	} else {
		interp = []string{sh}
	}
	tail := rest[1:]
	for _, a := range tail {
		if a == "-c" || (strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a[1:], "c")) {
			return nil, nil, false, valise.New("install_custom_refused", "不支持在管道右侧使用 -c 内联执行代码",
				"请使用 | bash 或 | bash -s -- 参数")
		}
	}
	// Strip stdin markers (-s, -) and separator (--) passed for stdin mode so
	// running the downloaded script file receives the intended positional args.
	i := 0
	for i < len(tail) && (tail[i] == "-s" || tail[i] == "-") {
		i++
	}
	if i < len(tail) && tail[i] == "--" {
		i++
	}
	if i < len(tail) {
		args = append([]string{}, tail[i:]...)
	}
	return interp, args, sudo, nil
}

func parseDirectArgvInstall(norm string, toks []shellTok) (*CustomInstallSpec, error) {
	words, ok := wordsOnly(toks)
	if !ok || len(words) == 0 {
		return nil, valise.New("install_custom_unsupported", "未能识别这条安装指令",
			"请粘贴官方 HTTPS 脚本地址（https://...）、curl ... | bash、bash <(curl ...) 或 npm install -g 命令")
	}
	check := words
	sudo := false
	if path.Base(check[0]) == "sudo" {
		sudo = true
		check = skipOptions(check[1:])
	}
	if len(check) >= 2 && path.Base(check[0]) == "npm" && (check[1] == "install" || check[1] == "i") {
		return &CustomInstallSpec{Raw: norm, Kind: "argv", Argv: words, Sudo: sudo}, nil
	}
	return nil, valise.New("install_custom_unsupported", "未能识别这条安装指令",
		"请粘贴官方 HTTPS 脚本地址（https://...）、curl ... | bash、bash <(curl ...) 或 npm install -g 命令")
}

func looksLikeURL(s string) bool {
	ls := strings.ToLower(s)
	return strings.HasPrefix(ls, "https://") || strings.HasPrefix(ls, "http://") || strings.Contains(ls, "://")
}

func validateHTTPSURL(raw string) (string, error) {
	if strings.HasPrefix(strings.ToLower(raw), "http://") {
		return "", insecureURLError(raw)
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Host == "" {
		return "", valise.New("install_custom_invalid_url", "这不是有效的官方 HTTPS 安装地址："+raw,
			"请检查地址是否以 https:// 开头且域名完整")
	}
	return raw, nil
}

func insecureURLError(raw string) error {
	return valise.New("install_custom_insecure_url", "只支持 HTTPS 官方地址，拒绝不安全的 HTTP 地址",
		"请改用 https:// 开头的官方地址").WithDetail(raw)
}

// SafeScriptFilename returns a safe filename under the staging directory
// for a script downloaded from rawURL.
func SafeScriptFilename(rawURL string) string {
	name := "install.sh"
	if u, err := url.Parse(rawURL); err == nil {
		base := path.Base(u.Path)
		if base != "" && base != "." && base != "/" {
			var b strings.Builder
			for _, r := range base {
				if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
					b.WriteRune(r)
				}
			}
			if s := strings.Trim(b.String(), "."); s != "" {
				name = s
			}
		}
	}
	return name
}
