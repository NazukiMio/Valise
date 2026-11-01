package revive

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"valise/core/profile"
	porterenroll "valise/porter/internal/enroll"
)

// HealthTiming overrides the profile's timing (tests); zero values mean
// the defaults of docs/ARCHITECTURE.md section 8.2.
type HealthTiming struct {
	Grace, Interval, Timeout time.Duration
	Need                     int
}

func dialOK(host string, port int) bool {
	c, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 2*time.Second)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// Health waits the grace period, then checks every interval until `need`
// consecutive rounds pass or the timeout expires. It returns a log of the
// rounds (shown to the user).
func Health(ctx context.Context, e *porterenroll.Env, p profile.Profile, unit string, t HealthTiming) ([]string, bool) {
	e = e.For(&p)
	if t.Grace == 0 {
		t.Grace = 20 * time.Second
		if p.Health != nil && p.Health.GraceSeconds > 0 {
			t.Grace = time.Duration(p.Health.GraceSeconds) * time.Second
		}
	}
	if t.Interval == 0 {
		t.Interval = 5 * time.Second
	}
	if t.Timeout == 0 {
		t.Timeout = 120 * time.Second
	}
	if t.Need == 0 {
		t.Need = 3
	}
	var log []string
	time.Sleep(t.Grace)
	deadline := time.Now().Add(t.Timeout)
	streak := 0
	for time.Now().Before(deadline) {
		ok, why := healthRound(ctx, e, p, unit)
		if fault() == "health" {
			ok, why = false, "故障注入：健康检查一律失败"
		}
		if ok {
			streak++
			log = append(log, fmt.Sprintf("通过（连续 %d 次）", streak))
			if streak >= t.Need {
				return log, true
			}
		} else {
			streak = 0
			log = append(log, "未通过："+why)
		}
		time.Sleep(t.Interval)
	}
	return log, false
}

func healthRound(ctx context.Context, e *porterenroll.Env, p profile.Profile, unit string) (bool, string) {
	if p.Health == nil {
		return true, ""
	}
	entry := func(id string) *profile.Entry {
		for i := range p.Entries {
			if p.Entries[i].ID == id {
				return &p.Entries[i]
			}
		}
		return nil
	}
	for _, c := range p.Health.Checks {
		switch c.Kind {
		case "systemd_active":
			if unit != "" && unitState(ctx, e, unit) != "active" {
				return false, unit + " 不是 active"
			}
		case "tcp":
			if en := entry(c.Entry); en == nil || !dialOK(en.Host, en.Port) {
				return false, "入口 " + c.Entry + " 的端口没有监听"
			}
		case "http":
			en := entry(c.Entry)
			if en == nil {
				return false, "未知入口 " + c.Entry
			}
			cl := http.Client{Timeout: 5 * time.Second}
			resp, err := cl.Get("http://" + net.JoinHostPort(en.Host, strconv.Itoa(en.Port)) + en.Path)
			if err != nil {
				return false, "入口 " + c.Entry + " HTTP 请求失败"
			}
			resp.Body.Close()
			good := false
			for _, s := range c.ExpectStatus {
				good = good || s == resp.StatusCode
			}
			if !good {
				return false, fmt.Sprintf("入口 %s 返回 HTTP %d", c.Entry, resp.StatusCode)
			}
		case "argv":
			argv, ok := e.ExpandArgv(c.Argv, nil)
			if !ok {
				return false, "检查命令依赖未设置的环境变量"
			}
			out, err := e.Run(ctx, 60*time.Second, "", argv...)
			if err != nil {
				return false, "检查命令无法运行"
			}
			okExit := c.OKExit
			if len(okExit) == 0 {
				okExit = []int{0}
			}
			good := false
			for _, x := range okExit {
				good = good || x == out.Code
			}
			if !good {
				return false, fmt.Sprintf("检查命令退出码 %d", out.Code)
			}
		}
	}
	return true, ""
}
