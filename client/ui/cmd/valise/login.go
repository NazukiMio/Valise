package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/term"

	"valise/ui/service"
)

var loginKinds = map[string]string{
	"loopback_oauth": "在本机浏览器里登录授权",
	"device_code":    "设备码",
	"paste_code":     "粘贴授权码",
	"remote_browser": "远端浏览器（尚未实现）",
}

func (c *cli) login(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	method := fs.String("method", "", "")
	pos, err := parseArgs(fs, args, 2)
	if err != nil {
		return err
	}
	host, instance := pos[0], pos[1]
	if *method == "" {
		target, err := c.svc.LoginMethods(ctx, host, instance)
		if err != nil {
			return err
		}
		if len(target.Profile.Login) == 0 {
			fmt.Fprintf(c.out, "harness 资料 %s 没有声明登录方式。\n", target.Profile.ID)
			return nil
		}
		fmt.Fprintf(c.out, "%s（harness 资料 %s）的登录方式：\n", instance, target.Profile.ID)
		for _, l := range target.Profile.Login {
			fmt.Fprintf(c.out, "  %-20s %s  %s\n", l.ID, l.DisplayName, loginKinds[l.Kind])
		}
		if target.Profile.Status != "verified" {
			fmt.Fprintln(c.out, "（harness 资料未核实：登录命令可能与当前 harness 版本不符）")
		}
		fmt.Fprintf(c.out, "使用：valise login %s %s --method <id>\n", host, instance)
		return nil
	}

	term := newLoginTerminal(c)
	defer term.restore()
	term.say("正在服务器 %s 上运行登录命令；终端里的输入会转发给它。", host)
	st, err := c.svc.RunLogin(ctx, host, instance, *method, service.LoginIO{
		Stdin: term.remote, Stdout: c.out, Cols: term.cols, Rows: term.rows,
		Status: func(s service.LoginStatus) {
			switch s.State {
			case service.LoginWaitingLink:
				term.say("状态：等待登录链接")
			case service.LoginRelayReady:
				term.say("登录授权已就绪：本机 %v 转发到服务器 %s 端口 %d（与回调地址同一端口）", s.Local, s.Remote, s.Port)
			case service.LoginBrowserOpened:
				term.say("状态：已在本机浏览器中打开 %s", s.Domain)
			case service.LoginLinkSkipped:
				term.say("状态：没有打开浏览器；需要时请手动打开上面的链接")
			case service.LoginCallbackReceived:
				term.say("状态：已收到回调（本机 %v → 服务器 %s:%d）", s.Local, s.Remote, s.Port)
			case service.LoginExited:
				if s.ExitCode < 0 {
					term.say("状态：登录命令已被结束")
				} else {
					term.say("状态：登录命令已退出，退出码 %d", s.ExitCode)
				}
			}
		},
		ConfirmOpen: func(link, domain string) bool {
			return term.confirm(fmt.Sprintf("登录链接指向 %s 。请确认这是你预期的网站。按回车在本机浏览器中打开，按 n 跳过：", domain))
		},
	})
	term.restore()
	if err != nil {
		return err
	}
	if st != nil && st.ExitCode > 0 {
		return fmt.Errorf("登录命令以退出码 %d 结束", st.ExitCode)
	}
	return nil
}

// loginTerminal puts a real terminal into raw mode so keystrokes reach the
// remote PTY unchanged, and lets Valise take over input briefly to confirm
// opening a link.
type loginTerminal struct {
	c          *cli
	remote     io.Reader
	cols, rows int
	restore    func()

	mu      sync.Mutex
	answers chan byte // non-nil while confirming
}

func newLoginTerminal(c *cli) *loginTerminal {
	t := &loginTerminal{c: c, restore: func() {}}
	r, w := io.Pipe()
	t.remote = r
	if f, ok := c.in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		if cols, rows, err := term.GetSize(int(f.Fd())); err == nil {
			t.cols, t.rows = cols, rows
		}
		if old, err := term.MakeRaw(int(f.Fd())); err == nil {
			var once sync.Once
			t.restore = func() { once.Do(func() { term.Restore(int(f.Fd()), old) }) }
		}
	}
	go func() {
		br := bufio.NewReader(c.in)
		for {
			b, err := br.ReadByte()
			if err != nil {
				w.Close()
				return
			}
			t.mu.Lock()
			ans := t.answers
			t.mu.Unlock()
			if ans != nil {
				select {
				case ans <- b:
				default:
				}
				continue
			}
			w.Write([]byte{b})
		}
	}()
	return t
}

// say prints a Valise line between the remote output (raw mode needs \r).
func (t *loginTerminal) say(format string, a ...any) {
	fmt.Fprintf(t.c.out, "\r\n[Valise] "+format+"\r\n", a...)
}

// confirm asks a yes/no question with Enter as yes.
func (t *loginTerminal) confirm(q string) bool {
	ans := make(chan byte, 1)
	t.mu.Lock()
	t.answers = ans
	t.mu.Unlock()
	defer func() {
		t.mu.Lock()
		t.answers = nil
		t.mu.Unlock()
	}()
	fmt.Fprintf(t.c.out, "\r\n[Valise] %s", q)
	for {
		select {
		case b := <-ans:
			switch b {
			case '\r', '\n', 'y', 'Y':
				fmt.Fprint(t.c.out, "\r\n")
				return true
			case 'n', 'N', 3, 27: // n, Ctrl-C, Esc
				fmt.Fprint(t.c.out, "\r\n")
				return false
			}
		case <-time.After(10 * time.Minute):
			return false
		}
	}
}

func (c *cli) open(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("open", flag.ContinueOnError)
	entry := fs.String("entry", "", "")
	idle := fs.Duration("idle", service.DefaultEntryIdle, "")
	noBrowser := fs.Bool("no-browser", false, "")
	change := fs.Bool("change-port", false, "")
	to := fs.Int("to", 0, "")
	noSeal := fs.Bool("no-seal", false, "")
	yes := fs.Bool("yes", false, "")
	pos, err := parseArgs(fs, args, 2)
	if err != nil {
		return err
	}
	if *change {
		if err := c.changePort(ctx, pos[0], pos[1], *entry, *to, *noSeal, *yes); err != nil {
			return err
		}
	} else if *to != 0 || *noSeal {
		return errUsage
	}
	steps := map[string]string{service.EntryTunnel: "隧道已建立", service.EntryRemote: "远端端口在监听", service.EntryHealth: "健康检查通过", service.EntryAuth: "管理页令牌"}
	err = c.svc.OpenEntry(ctx, pos[0], pos[1], service.OpenOptions{Entry: *entry, Idle: *idle,
		Status: func(s service.EntryStatus) {
			mark := "✓"
			if !s.OK {
				mark = "✗"
			}
			fmt.Fprintf(c.out, "  %s %s：%s\n", mark, steps[s.Step], s.Detail)
		},
		Ready: func(open, display string) {
			fmt.Fprintf(c.out, "管理页：%s\n", display)
			if open != display {
				fmt.Fprintln(c.out, "（带令牌的地址只直接交给浏览器，不显示）")
			}
			if !*noBrowser {
				if err := service.OpenURL(open); err != nil {
					fmt.Fprintln(c.out, "无法打开浏览器，请手动打开上面的地址。")
				}
			}
			how := "按 Ctrl-C 关闭"
			if *idle > 0 {
				how += fmt.Sprintf("；%s 内没有任何连接会自动关闭", strings.TrimSuffix((*idle).String(), "0s"))
			}
			fmt.Fprintln(c.out, how+"。")
		}})
	if err == nil {
		fmt.Fprintln(c.out, "管理页隧道已关闭。")
	}
	return err
}
