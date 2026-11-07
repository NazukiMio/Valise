package protection

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	cp "valise/core/protection"
)

const commandTimeout = 30 * time.Second

func names(instance string) (string, string) {
	return "valise-seal@" + instance + ".service", "valise-seal@" + instance + ".timer"
}
func (s Store) run(ctx context.Context, args ...string) (string, error) {
	if s.Env == nil {
		return "", fail("timer_failed", "无法读取服务器命令环境")
	}
	out, err := s.Env.Run(ctx, commandTimeout, s.Home, args...)
	if err != nil || out.Code != 0 {
		return "", fail("timer_failed", "服务器日程命令未成功，执行结果需要核实")
	}
	return strings.TrimSpace(string(out.Stdout)), nil
}
func (s Store) control(ctx context.Context, args ...string) error {
	_, err := s.run(ctx, append([]string{"systemctl", "--user"}, args...)...)
	return err
}
func (s Store) calendar(ctx context.Context, d cp.Desired) (string, error) {
	if !*d.Schedule.Enabled {
		return "", nil
	}
	return s.run(ctx, "systemd-analyze", "calendar", "--iterations=4", d.Schedule.Calendar())
}
func (s Store) observe(ctx context.Context, instance string) cp.TimerObservation {
	_, timer := names(instance)
	v := cp.TimerObservation{Status: "unknown"}
	out, err := s.run(ctx, "systemctl", "--user", "--timestamp=unix", "show", timer, "--property=LoadState,ActiveState,UnitFileState,LastTriggerUSec,NextElapseUSecRealtime")
	if err == nil {
		values := map[string]string{}
		for _, line := range strings.Split(out, "\n") {
			k, val, ok := strings.Cut(line, "=")
			if ok {
				values[k] = val
			}
		}
		if values["LoadState"] == "not-found" {
			v.Status = "not_applicable"
			v.ActiveState = "inactive"
			v.UnitFileState = "disabled"
		} else if values["LoadState"] == "loaded" && values["ActiveState"] != "" && values["UnitFileState"] != "" {
			v.Status = "fresh"
			v.ActiveState = values["ActiveState"]
			v.UnitFileState = values["UnitFileState"]
			v.LastTrigger = unixTimestamp(values["LastTriggerUSec"])
			v.NextTrigger = unixTimestamp(values["NextElapseUSecRealtime"])
		}
	}
	if s.Env != nil && s.Env.Get("USER") != "" {
		out, err = s.run(ctx, "loginctl", "show-user", s.Env.Get("USER"), "-p", "Linger", "--value")
		v.Linger = err == nil && out == "yes"
	}
	return v
}
func unixTimestamp(s string) string {
	if !strings.HasPrefix(s, "@") {
		return ""
	}
	seconds, fraction, _ := strings.Cut(strings.TrimPrefix(s, "@"), ".")
	sec, err := strconv.ParseInt(seconds, 10, 64)
	if err != nil {
		return ""
	}
	if len(fraction) > 9 {
		fraction = fraction[:9]
	}
	fraction += strings.Repeat("0", 9-len(fraction))
	nanos, err := strconv.ParseInt(fraction, 10, 64)
	if err != nil {
		return ""
	}
	return time.Unix(sec, nanos).UTC().Format(time.RFC3339Nano)
}

func (s Store) timerFiles(instance string, d cp.Desired) (map[string]fileImage, error) {
	svc, tmr := names(instance)
	files := map[string]fileImage{}
	dir := filepath.Join(s.Home, ".config", "systemd", "user")
	if !*d.Schedule.Enabled {
		files[filepath.Join(dir, svc)] = fileImage{}
		files[filepath.Join(dir, tmr)] = fileImage{}
		return files, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return nil, err
	}
	if strings.ContainsAny(exe, " \t\r\n\"'\\%$") {
		return nil, fail("timer_path", "porter 路径含 systemd 不可接受的字符")
	}
	service := fmt.Sprintf("[Unit]\nDescription=Valise accepted-plan backup for %s\n\n[Service]\nType=oneshot\nExecStart=%s protection-timer --instance %s\nNoNewPrivileges=yes\n", instance, exe, instance)
	timer := fmt.Sprintf("[Unit]\nDescription=Valise accepted-plan schedule for %s\n\n[Timer]\nOnCalendar=%s\n\n[Install]\nWantedBy=timers.target\n", instance, d.Schedule.Calendar())
	files[filepath.Join(dir, svc)] = fileImage{Exists: true, Data: []byte(service)}
	files[filepath.Join(dir, tmr)] = fileImage{Exists: true, Data: []byte(timer)}
	return files, nil
}
func (s Store) configure(ctx context.Context, instance string, files map[string]fileImage, active, enabled bool) error {
	_, tmr := names(instance)
	// Quiesce the old timer before replacing its fixed configuration.
	if v := s.observe(ctx, instance); v.Status != "not_applicable" {
		if v.Status == "unknown" {
			return fail("result_unknown", "无法核实当前定时器状态")
		}
		if err := s.control(ctx, "disable", "--now", tmr); err != nil {
			return err
		}
	}
	for _, path := range sortedKeys(files) {
		f := files[path]
		if f.Exists {
			mode := os.FileMode(0644)
			if strings.HasSuffix(path, ".vault.json") {
				mode = 0600
			}
			if err := atomicBytes(path, f.Data, mode); err != nil {
				return err
			}
		} else {
			if err := removeSync(path); err != nil {
				return err
			}
		}
	}
	if err := s.control(ctx, "daemon-reload"); err != nil {
		return err
	}
	if enabled {
		if err := s.control(ctx, "enable", tmr); err != nil {
			return err
		}
	}
	if active {
		if err := s.control(ctx, "start", tmr); err != nil {
			return err
		}
	}
	return nil
}
func removeSync(path string) error {
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
func (s Store) verify(ctx context.Context, instance string, enabled bool, period fileImage) error {
	v := s.observe(ctx, instance)
	if enabled {
		if v.Status != "fresh" || v.UnitFileState != "enabled" || v.ActiveState != "active" || v.NextTrigger == "" || v.NextTrigger == "n/a" {
			return fail("result_unknown", "新日程尚未核实生效")
		}
	} else {
		if v.Status == "unknown" || v.ActiveState != "inactive" || (v.UnitFileState != "disabled" && v.UnitFileState != "") {
			return fail("result_unknown", "尚未核实定时器已关闭")
		}
	}
	actual, err := image(s.periodPath(instance))
	if err != nil {
		return err
	}
	a, _ := cp.Digest(actual)
	b, _ := cp.Digest(period)
	if a != b {
		return fail("result_unknown", "周期兼容投影尚未核实一致")
	}
	return nil
}
