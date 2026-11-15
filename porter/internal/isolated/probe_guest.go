package isolated

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ProbeGuest is the fixed internal porter entry mounted by GatewayProbe. It
// accepts no command/path arguments and only reads its private read-only
// descriptor. It refuses host execution before starting or stopping anything.
func ProbeGuest(stdout io.Writer) error {
	if !privateInit() {
		return ErrUnsupported
	}
	if err := protectController(); err != nil {
		return ErrUnsupported
	}
	f, err := os.Open(probeConfigPath)
	if err != nil {
		return ErrWorkspace
	}
	d := json.NewDecoder(io.LimitReader(f, 64<<10))
	d.DisallowUnknownFields()
	var descriptor probeDescriptor
	err = d.Decode(&descriptor)
	var extra any
	if err != nil || d.Decode(&extra) != io.EOF {
		f.Close()
		return ErrWorkspace
	}
	f.Close()
	s, check := descriptor.Spec, descriptor.Check
	if !check.validAt(s.home()) || s.home() != os.Getenv("HOME") || !privatePathAt(s.home(), s.WorkingDir) || len(s.Argv) == 0 || !privatePathAt(s.home(), s.Argv[0]) ||
		time.Until(descriptor.Deadline) <= 0 || time.Until(descriptor.Deadline) > 2*time.Minute {
		return ErrWorkspace
	}
	ctx, cancel := context.WithDeadline(context.Background(), descriptor.Deadline)
	defer cancel()
	result := ProbeResult{ProofKind: "http"}
	if check.Offline != nil {
		result.ProofKind = "unix-json"
	}
	cmd := exec.Command(s.Argv[0], s.Argv[1:]...)
	cmd.Dir = s.WorkingDir
	cmd.Env = os.Environ() // bwrap supplied only the fixed private environment
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Child streams are disconnected, including stdout: only the trusted
	// helper writes the bounded structured result. No terminal content is read.
	wait := make(chan struct{})
	if err := cmd.Start(); err != nil {
		result.FailureCode = "program_start_failed"
		result.ProcessesStopped = stopPrivateChildren()
		result.Completed = true
		return json.NewEncoder(stdout).Encode(result)
	}
	result.ProcessStarted = true
	go func() { cmd.Wait(); close(wait) }()
	result = awaitGateway(ctx, check, wait, result)
	// All remaining processes belong to this private PID namespace. First
	// permit the program to terminate; then verify/kill detached descendants.
	syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	timer := time.NewTimer(150 * time.Millisecond)
	select {
	case <-wait:
		if cmd.ProcessState != nil {
			code := cmd.ProcessState.ExitCode()
			result.ExitCode = &code
		}
	case <-timer.C:
	}
	timer.Stop()
	result.ProcessesStopped = stopPrivateChildren()
	if result.ExitCode == nil {
		select {
		case <-wait:
			if cmd.ProcessState != nil {
				code := cmd.ProcessState.ExitCode()
				result.ExitCode = &code
			}
		case <-time.After(200 * time.Millisecond):
			result.ProcessesStopped = false
		}
	}
	if !result.ProcessesStopped {
		result.FailureCode = "candidate_stop_unknown"
	}
	result.Completed = true
	return json.NewEncoder(stdout).Encode(result)
}

func awaitGateway(ctx context.Context, check ProbeCheck, wait <-chan struct{}, result ProbeResult) ProbeResult {
	if check.Offline != nil {
		return awaitOffline(ctx, check, wait, result)
	}
	transport := &http.Transport{DialContext: (&net.Dialer{Timeout: 250 * time.Millisecond}).DialContext, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 500 * time.Millisecond,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	address := net.JoinHostPort(check.Host, strconv.Itoa(check.Port))
	endpoint := (&url.URL{Scheme: "http", Host: address, Path: check.Path}).String()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			result.FailureCode = "gateway_not_ready"
			return result
		case <-wait:
			result.FailureCode = "program_exited"
			return result
		default:
		}
		if conn, err := (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext(ctx, "tcp", address); err == nil {
			conn.Close()
			result.PortListening = true
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err == nil {
			if resp, err := client.Do(req); err == nil {
				result.HTTPResponded, result.HTTPStatus = true, resp.StatusCode
				resp.Body.Close() // never read, parse, store or log the body
				if slices.Contains(check.Statuses, resp.StatusCode) {
					select {
					case <-wait:
						result.FailureCode = "program_exited"
						return result
					default:
					}
					result.GatewayPassed = true
					return result
				}
			}
		}
		select {
		case <-ctx.Done():
		case <-ticker.C:
		}
	}
}

func privateInit() bool {
	if runtime.GOOS != "linux" || os.Getpid() <= 1 || os.Getppid() != 1 || !ValidHome(os.Getenv("HOME")) {
		return false
	}
	exe, err := os.Executable()
	if err != nil || exe != "/valise/porter" {
		return false
	}
	init, err := os.Readlink("/proc/1/exe")
	return err == nil && init == "/usr/bin/bwrap"
}

// This function MUST NOT run without the private-init proof. Neither host
// process IDs nor caller-supplied IDs are ever accepted for termination.
func stopPrivateChildren() bool {
	if !privateInit() {
		return false
	}
	until := time.Now().Add(700 * time.Millisecond)
	for {
		entries, err := os.ReadDir("/proc")
		if err != nil {
			return false
		}
		live := false
		for _, e := range entries {
			pid, err := strconv.Atoi(e.Name())
			if err != nil || pid <= 1 || pid == os.Getpid() {
				continue
			}
			stat, err := os.ReadFile("/proc/" + e.Name() + "/stat")
			if os.IsNotExist(err) {
				continue
			}
			i := strings.LastIndex(string(stat), ") ")
			if err != nil || i < 0 || len(stat) <= i+2 {
				return false
			}
			if stat[i+2] == 'Z' || stat[i+2] == 'X' {
				continue
			}
			live = true
			if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
				return false
			}
		}
		if !live {
			return true
		}
		if time.Now().After(until) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}
