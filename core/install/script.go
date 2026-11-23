package install

import (
	"bytes"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	reBase64Extra  = regexp.MustCompile(`\bbase64\b[^\n|;&]*\s(-[A-Za-z]*d|--decode)\b`)
	reProcSubShell = regexp.MustCompile(`\b((ba|z|da|k|c|tc|fi)?sh|python\d*|perl|ruby|node|php)\b[^\n;|&]*<\(\s*(curl|wget|fetch)\b`)
	reEnvAssign    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)
	downloaders    = map[string]bool{"curl": true, "wget": true, "fetch": true}
	shellKeywords  = map[string]bool{
		"if": true, "then": true, "elif": true, "else": true, "fi": true,
		"do": true, "done": true, "while": true, "until": true, "case": true, "esac": true,
		"{": true, "}": true, "(": true, ")": true, "!": true,
	}
)

// CheckScript runs the section 7 static checks against a downloaded script
// body. Unlike CheckCommand (which checks literal non-shell argv), normal
// shell syntax such as $(uname -m) and # comments is allowed. Unreadable or
// binary payloads, fork bombs, destructive commands, and disabling security
// services are refused without bypass; in-script secondary download pipes,
// decoding/obfuscation, and dynamic eval emit high-risk warnings that require
// explicit user confirmation.
func CheckScript(body []byte, home string) []Finding {
	if len(bytes.TrimSpace(body)) == 0 {
		return []Finding{{Refuse: true, Rule: RuleHidden, Reason: "脚本内容为空，实际执行的内容无法事先展示"}}
	}
	if !isReadableScript(body) {
		return []Finding{{Refuse: true, Rule: RuleHidden, Reason: "脚本不是可读文本或包含不可见控制字符，实际执行的内容无法事先展示"}}
	}

	var out []Finding
	var curLine int
	var curSnippet string
	add := func(refuse bool, rule, reason string) {
		// Inside a downloaded script body, only destructive and security-disabling
		// rules are permanent hard refusals; hidden/inline/pipe findings become
		// high-risk warnings requiring confirmation.
		refuse = refuse && (rule == RuleDestroy || rule == RuleSecurityOff)
		for _, f := range out {
			if f.Rule == rule && f.Reason == reason {
				return
			}
		}
		out = append(out, Finding{
			Refuse:  refuse,
			Rule:    rule,
			Reason:  reason,
			Line:    curLine,
			Snippet: curSnippet,
		})
	}

	for _, sl := range logicalScriptLines(string(body)) {
		line := sl.text
		curLine = sl.line
		curSnippet = trimSnippet(line)
		if reDecode.MatchString(line) || reBase64Extra.MatchString(line) {
			add(false, RuleHidden, "脚本里包含解码隐藏内容的命令，实际执行的内容无法事先展示")
		}
		if rePipeShell.MatchString(line) || reProcSubShell.MatchString(line) {
			add(false, RulePipeToShell, "脚本内部把网络内容直接交给解释器执行")
		}
		if reForkBomb.MatchString(line) {
			add(true, RuleDestroy, "fork 炸弹，会耗尽服务器资源")
		}
		for _, u := range reURL.FindAllString(line, -1) {
			u = strings.TrimRight(u, ");,'\"")
			if u != "" {
				add(false, RuleDownload, "会访问 "+u)
			}
		}
		if m := reSensitive.FindString(line); m != "" {
			add(false, RuleSensitive, "涉及敏感配置："+m)
		}
		checkScriptLineStatements(line, home, add)
	}
	return out
}

func trimSnippet(s string) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > 120 {
		return string(r[:120])
	}
	return s
}

func isReadableScript(body []byte) bool {
	if !utf8.Valid(body) {
		return false
	}
	for _, b := range body {
		if b == 0 || (b < 0x20 && b != '\n' && b != '\r' && b != '\t') || b == 0x7f {
			return false
		}
	}
	return true
}

type scriptLine struct {
	line int
	text string
}

// logicalScriptLines strips # comment lines, joins backslash continuations,
// drops unquoted inline # comments, and preserves the 1-based starting line
// number of each logical line.
func logicalScriptLines(text string) []scriptLine {
	raw := strings.Split(text, "\n")
	var out []scriptLine
	var cur strings.Builder
	startLine := 0
	for i, ln := range raw {
		ln = strings.TrimSuffix(ln, "\r")
		if cur.Len() == 0 && strings.HasPrefix(strings.TrimSpace(ln), "#") {
			continue
		}
		ln = stripInlineComment(ln)
		if cur.Len() == 0 && strings.TrimSpace(ln) == "" {
			continue
		}
		if startLine == 0 {
			startLine = i + 1
		}
		if hasLineContinuation(ln) {
			cur.WriteString(strings.TrimSuffix(ln, "\\"))
			cur.WriteByte(' ')
			continue
		}
		cur.WriteString(ln)
		if s := strings.TrimSpace(cur.String()); s != "" {
			out = append(out, scriptLine{line: startLine, text: s})
		}
		cur.Reset()
		startLine = 0
	}
	if s := strings.TrimSpace(cur.String()); s != "" {
		out = append(out, scriptLine{line: startLine, text: s})
	}
	return out
}

func hasLineContinuation(s string) bool {
	n := 0
	for i := len(s) - 1; i >= 0 && s[i] == '\\'; i-- {
		n++
	}
	return n%2 == 1
}

func stripInlineComment(s string) string {
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote == '\'' {
			if c == '\'' {
				quote = 0
			}
			continue
		}
		if c == '\\' && i+1 < len(s) {
			i++
			continue
		}
		if quote == '"' {
			if c == '"' {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		if c == '#' && (i == 0 || s[i-1] == ' ' || s[i-1] == '\t') {
			return s[:i]
		}
	}
	return s
}

func checkScriptLineStatements(line, home string, add func(bool, string, string)) {
	tokens, _ := tokenizeShell(line)
	type stmt struct {
		words []string
		sep   string // operator after this statement
	}
	var stmts []stmt
	var cur []string
	flush := func(sep string) {
		if len(cur) > 0 {
			stmts = append(stmts, stmt{words: cur, sep: sep})
			cur = nil
		} else if len(stmts) > 0 && sep != "" {
			stmts[len(stmts)-1].sep = sep
		}
	}
	for _, tk := range tokens {
		if tk.op != "" {
			flush(tk.op)
			continue
		}
		cur = append(cur, tk.text)
	}
	flush("")

	inDownloadPipe := false
	for _, st := range stmts {
		words := stripLeadingKeywordsAndEnv(st.words)
		if len(words) > 0 {
			if inDownloadPipe && isInterpreterTarget(words) {
				add(false, RulePipeToShell, "脚本内部把网络内容直接交给解释器执行")
			}
			if st.sep == "|" && isDownloaderStmt(words) {
				inDownloadPipe = true
			}
			if path.Base(words[0]) == "eval" {
				joined := strings.Join(words[1:], " ")
				if strings.ContainsAny(joined, "$`") || reInlineBad.MatchString(joined) {
					add(false, RuleHidden, "脚本通过 eval 执行动态或隐藏代码，实际执行的内容无法事先展示")
				} else {
					add(false, RuleInline, "脚本包含 eval 内联执行")
				}
			} else {
				checkArgv(words, home, add)
			}
		}
		if st.sep != "|" {
			inDownloadPipe = false
		}
	}
}

func stripLeadingKeywordsAndEnv(words []string) []string {
	for len(words) > 0 && shellKeywords[words[0]] {
		words = words[1:]
	}
	for len(words) > 1 && reEnvAssign.MatchString(words[0]) {
		words = words[1:]
	}
	return words
}

func isDownloaderStmt(words []string) bool {
	rest := peelWrappers(words)
	return len(rest) > 0 && downloaders[path.Base(rest[0])]
}

func isInterpreterTarget(words []string) bool {
	rest := peelWrappers(words)
	if len(rest) == 0 {
		return false
	}
	name := path.Base(rest[0])
	return shells[name] || interpreters[name] != ""
}

func peelWrappers(words []string) []string {
	rest := words
	for len(rest) > 0 {
		name := path.Base(rest[0])
		switch {
		case elevators[name]:
			rest = skipOptions(rest[1:])
		case name == "env":
			rest = rest[1:]
			for len(rest) > 0 && (strings.Contains(rest[0], "=") || strings.HasPrefix(rest[0], "-")) {
				rest = rest[1:]
			}
		case name == "nohup" || name == "nice" || name == "time" || name == "command":
			rest = rest[1:]
		default:
			return rest
		}
	}
	return rest
}

type shellTok struct {
	text string
	op   string // non-empty for unquoted shell operators: |, ;, &&, ||, &, <(, ), >, >>, <
}

// tokenizeShell splits a shell line into words and unquoted operators,
// unquoting '...' and "..." segments so flag/path checks see literal values.
// It also reports whether a quote remained unclosed.
func tokenizeShell(s string) ([]shellTok, bool) {
	var out []shellTok
	var buf strings.Builder
	hasWord := false
	procDepth := 0
	flush := func() {
		if hasWord {
			out = append(out, shellTok{text: buf.String()})
			buf.Reset()
			hasWord = false
		}
	}
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote == '\'' {
			if c == '\'' {
				quote = 0
			} else {
				buf.WriteByte(c)
			}
			continue
		}
		if quote == '"' {
			if c == '"' {
				quote = 0
			} else if c == '\\' && i+1 < len(s) && (s[i+1] == '"' || s[i+1] == '\\' || s[i+1] == '$' || s[i+1] == '`') {
				i++
				buf.WriteByte(s[i])
			} else {
				buf.WriteByte(c)
			}
			continue
		}
		switch c {
		case ' ', '\t', '\n', '\r':
			flush()
		case '\'':
			quote, hasWord = '\'', true
		case '"':
			quote, hasWord = '"', true
		case '\\':
			hasWord = true
			if i+1 < len(s) {
				i++
				buf.WriteByte(s[i])
			}
		case '|':
			flush()
			if i+1 < len(s) && s[i+1] == '|' {
				out = append(out, shellTok{op: "||"})
				i++
			} else {
				out = append(out, shellTok{op: "|"})
			}
		case '&':
			flush()
			if i+1 < len(s) && s[i+1] == '&' {
				out = append(out, shellTok{op: "&&"})
				i++
			} else {
				out = append(out, shellTok{op: "&"})
			}
		case ';':
			flush()
			out = append(out, shellTok{op: ";"})
		case '>':
			flush()
			if i+1 < len(s) && s[i+1] == '>' {
				out = append(out, shellTok{op: ">>"})
				i++
			} else {
				out = append(out, shellTok{op: ">"})
			}
		case '<':
			flush()
			if i+1 < len(s) && s[i+1] == '(' {
				procDepth++
				out = append(out, shellTok{op: "<("})
				i++
			} else {
				out = append(out, shellTok{op: "<"})
			}
		case ')':
			if procDepth > 0 {
				flush()
				procDepth--
				out = append(out, shellTok{op: ")"})
			} else {
				hasWord = true
				buf.WriteByte(c)
			}
		default:
			hasWord = true
			buf.WriteByte(c)
		}
	}
	flush()
	return out, quote != 0
}
