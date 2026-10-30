package ssh

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"sync"
	"time"

	"valise/core/rpc"
	"valise/core/valise"
)

// RPCClient is a session-scoped `valise-porter rpc` process reached through
// `ssh -T <host> "<porter> rpc"`.
type RPCClient struct {
	t      *Transport
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	conn   *rpc.Conn
	stderr *lockedBuffer

	mu      sync.Mutex
	nextID  int64
	pending map[string]chan rpc.Response
	err     error // set once the stream is gone
	done    chan struct{}
	waited  chan struct{}
	exitErr error
}

// Dial starts the RPC session and performs hello, refusing a porter with a
// different protocol major version.
func Dial(ctx context.Context, t *Transport, porterPath, clientVersion string) (*RPCClient, *rpc.HelloResult, error) {
	return DialEnv(ctx, t, porterPath, clientVersion, nil)
}

var envRe = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*=[A-Za-z0-9_.=:-]*$`)

// DialEnv is Dial with extra environment variables for this porter process
// (used only for fault injection in drills).
func DialEnv(ctx context.Context, t *Transport, porterPath, clientVersion string, env []string) (*RPCClient, *rpc.HelloResult, error) {
	prefix := ""
	for _, kv := range env {
		if !envRe.MatchString(kv) {
			return nil, nil, fmt.Errorf("invalid environment entry %q", kv)
		}
		prefix += kv + " "
	}
	if prefix != "" {
		prefix = "env " + prefix
	}
	cmd, err := t.Command(prefix + Quote(porterPath) + " rpc")
	if err != nil {
		return nil, nil, err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	c := &RPCClient{
		t: t, cmd: cmd, stdin: stdin, stderr: &lockedBuffer{},
		pending: map[string]chan rpc.Response{},
		done:    make(chan struct{}), waited: make(chan struct{}),
	}
	cmd.Stderr = c.stderr
	c.conn = rpc.NewConn(stdout, stdin)
	if err := cmd.Start(); err != nil {
		return nil, nil, t.startError(err)
	}
	go c.readLoop()
	go func() {
		<-c.done
		c.exitErr = cmd.Wait()
		close(c.waited)
	}()

	var hello rpc.HelloResult
	err = c.Call(ctx, rpc.MethodHello, rpc.HelloParams{ClientVersion: clientVersion, Protocol: rpc.ProtocolVersion}, &hello)
	if err != nil {
		c.Close()
		return nil, nil, err
	}
	if !rpc.Compatible(hello.Protocol, rpc.ProtocolVersion) {
		c.Close()
		return nil, nil, rpc.ProtocolMismatch(rpc.ProtocolVersion, hello.Protocol)
	}
	return c, &hello, nil
}

func (c *RPCClient) readLoop() {
	var err error
	for {
		var line []byte
		line, err = c.conn.ReadLine()
		if err != nil {
			break
		}
		var resp rpc.Response
		if json.Unmarshal(line, &resp) != nil || resp.JSONRPC != rpc.JSONRPCVersion {
			err = errors.New("porter 返回了无法解析的数据")
			break
		}
		c.mu.Lock()
		ch := c.pending[string(resp.ID)]
		delete(c.pending, string(resp.ID))
		c.mu.Unlock()
		if ch != nil {
			ch <- resp
		}
	}
	c.fail(err)
}

// fail ends the session and wakes every pending call.
func (c *RPCClient) fail(cause error) {
	c.mu.Lock()
	if c.err == nil {
		if cause == nil || errors.Is(cause, io.EOF) {
			cause = io.ErrUnexpectedEOF
		}
		c.err = cause
		for id, ch := range c.pending {
			close(ch)
			delete(c.pending, id)
		}
		close(c.done)
	}
	c.mu.Unlock()
}

// Call sends one request and decodes its result into result (may be nil).
func (c *RPCClient) Call(ctx context.Context, method string, params, result any) error {
	p, err := json.Marshal(params)
	if err != nil {
		return err
	}
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return c.streamError()
	}
	c.nextID++
	id := json.RawMessage(strconv.FormatInt(c.nextID, 10))
	ch := make(chan rpc.Response, 1)
	c.pending[string(id)] = ch
	c.mu.Unlock()

	if err := c.conn.Write(rpc.Request{JSONRPC: rpc.JSONRPCVersion, ID: id, Method: method, Params: p}); err != nil {
		c.fail(err)
		return c.streamError()
	}
	select {
	case resp, ok := <-ch:
		if !ok {
			return c.streamError()
		}
		if resp.Error != nil {
			if resp.Error.Data != nil {
				return resp.Error.Data
			}
			return resp.Error
		}
		if result != nil {
			return json.Unmarshal(resp.Result, result)
		}
		return nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, string(id))
		c.mu.Unlock()
		return ctx.Err()
	}
}

// streamError explains a lost session, waiting briefly for ssh to exit so
// its status and stderr can be used.
func (c *RPCClient) streamError() error {
	var exitErr error
	select {
	case <-c.waited:
		exitErr = c.exitErr
	case <-time.After(2 * time.Second):
	}
	stderr := c.stderr.String()
	var ee *exec.ExitError
	if errors.As(exitErr, &ee) && ee.ExitCode() == ExitConnFailed {
		return c.t.ConnError(stderr)
	}
	return valise.New("porter_session_lost",
		fmt.Sprintf("与服务器 %s 上 porter 的会话中断", c.t.host),
		"检查网络后重试；如果反复出现，运行 valise doctor 检查 porter 是否完好",
	).WithDetail(fmt.Sprintf("%v\n%s", c.err, lastLines(stderr, 5)))
}

// Close ends the session: stdin is closed so porter exits on EOF, and ssh
// is killed if it does not exit promptly.
func (c *RPCClient) Close() error {
	_ = c.stdin.Close()
	select {
	case <-c.waited:
	case <-time.After(3 * time.Second):
		_ = c.cmd.Process.Kill()
		<-c.waited
	}
	return nil
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.b.Len() > 64<<10 {
		return len(p), nil
	}
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}
