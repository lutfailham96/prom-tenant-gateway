package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tokens used by the run/main tests (>= 32 chars, so no weak-token warning).
const (
	tokenA = "run-test-token-tenant-a-0123456789abcdef"
	tokenB = "run-test-token-tenant-b-0123456789abcdef"
)

// testClient does not keep connections alive. http.DefaultClient's pool can
// hold a dialed connection that never sent a request; fasthttp only treats
// such a connection as idle 5s after accept, which would make every shutdown
// in these tests wait that long.
var testClient = &http.Client{
	Transport: &http.Transport{DisableKeepAlives: true},
	Timeout:   5 * time.Second,
}

// syncBuffer is an io.Writer safe for the run goroutine to write while the
// test reads.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

// lines returns the JSON log records written so far.
func (b *syncBuffer) lines(t *testing.T) []map[string]any {
	t.Helper()
	b.mu.Lock()
	raw := b.buf.String()
	b.mu.Unlock()

	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &rec), line)
		out = append(out, rec)
	}
	return out
}

// find returns the first log record with the given message, or nil.
func (b *syncBuffer) find(t *testing.T, message string) map[string]any {
	t.Helper()
	for _, rec := range b.lines(t) {
		if rec["message"] == message {
			return rec
		}
	}
	return nil
}

// isolateEnv blanks every variable read at startup (empty means unset for
// config.Load and the token store), including TENANT_* inherited from the
// environment, then applies env.
func isolateEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for _, key := range []string{
		"PORT", "HTTP_PORT", "LOG_LEVEL", "RATE_LIMIT_ENABLED", "RATE_LIMIT_MAX",
		"RATE_LIMIT_WINDOW", "TRUSTED_PROXIES", "VALID_TOKENS_JSON", "MEMORY_LIMIT_BYTES",
	} {
		t.Setenv(key, "")
	}
	for _, kv := range os.Environ() {
		if key, _, _ := strings.Cut(kv, "="); strings.HasPrefix(key, "TENANT_") {
			t.Setenv(key, "")
		}
	}
	for k, v := range env {
		t.Setenv(k, v)
	}
}

// listenRecorder records the address run asks for and serves on an
// ephemeral loopback port instead. It also reports when the server first
// reads from a connection (see serverRead).
type listenRecorder struct {
	network, address string
	ln               chan net.Listener

	mu    sync.Mutex
	reads map[string]chan struct{} // by client address
}

func newListenRecorder() *listenRecorder {
	return &listenRecorder{ln: make(chan net.Listener, 1), reads: map[string]chan struct{}{}}
}

func (r *listenRecorder) listen(network, address string) (net.Listener, error) {
	r.network, r.address = network, address
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	wrapped := &readNotifyListener{Listener: ln, rec: r}
	r.ln <- wrapped
	return wrapped, nil
}

// serverRead returns a channel closed once the server has read data from the
// connection whose client side has address clientAddr. By then the server has
// accepted the connection and counts it as open, so a Shutdown started
// afterwards has to wait for it.
func (r *listenRecorder) serverRead(clientAddr string) chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	ch, ok := r.reads[clientAddr]
	if !ok {
		ch = make(chan struct{})
		r.reads[clientAddr] = ch
	}
	return ch
}

type readNotifyListener struct {
	net.Listener
	rec *listenRecorder
}

func (l *readNotifyListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &readNotifyConn{Conn: c, read: l.rec.serverRead(c.RemoteAddr().String())}, nil
}

type readNotifyConn struct {
	net.Conn
	read chan struct{}
	once sync.Once
}

func (c *readNotifyConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.once.Do(func() { close(c.read) })
	}
	return n, err
}

// running is a run invocation in progress.
type running struct {
	base   string
	ln     net.Listener
	rec    *listenRecorder
	logs   *syncBuffer
	cancel context.CancelFunc
	done   chan int
}

// startRun starts run with env and waits until /healthz answers.
func startRun(t *testing.T, env map[string]string, shutdown time.Duration) *running {
	t.Helper()
	isolateEnv(t, env)

	r := &running{rec: newListenRecorder(), logs: &syncBuffer{}, done: make(chan int, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	t.Cleanup(cancel)

	go func() {
		r.done <- run(ctx, runOptions{stdout: r.logs, listen: r.rec.listen, shutdownTimeout: shutdown})
	}()

	select {
	case r.ln = <-r.rec.ln:
	case code := <-r.done:
		t.Fatalf("run exited with %d before listening; logs: %v", code, r.logs.lines(t))
	case <-time.After(5 * time.Second):
		t.Fatal("run did not listen")
	}
	r.base = "http://" + r.ln.Addr().String()
	require.Eventually(t, func() bool {
		resp, err := testClient.Get(r.base + "/healthz")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 5*time.Second, 10*time.Millisecond)
	return r
}

// wait returns run's exit code.
func (r *running) wait(t *testing.T) int {
	t.Helper()
	select {
	case code := <-r.done:
		return code
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return")
		return -1
	}
}

func (r *running) validate(t *testing.T, token string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, r.base+"/validate-token", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := testClient.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	return resp
}

// Invalid configuration is reported and makes run return 1 before
// listening.
func TestRun_StartupValidation(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		message string
		errPart string
	}{
		{"no tokens", nil, "No valid tokens configured", "TENANT_*_TOKEN"},
		{"bare TENANT_TOKEN is not a tenant", map[string]string{"TENANT_TOKEN": tokenA}, "No valid tokens configured", ""},
		{"broken VALID_TOKENS_JSON", map[string]string{"VALID_TOKENS_JSON": "{bad"}, "Invalid token configuration", "parse VALID_TOKENS_JSON"},
		{"placeholder token", map[string]string{"TENANT_PROD_TOKEN": "CHANGE_ME_PROD_TOKEN"}, "Invalid token configuration", "placeholder"},
		{"federated tenant ID", map[string]string{"VALID_TOKENS_JSON": `{"a|b":"` + tokenA + `"}`}, "Invalid token configuration", "invalid character '|'"},
		{"token shared by two tenants", map[string]string{"TENANT_A_TOKEN": tokenA, "TENANT_B_TOKEN": tokenA}, "Invalid token configuration", "is shared by tenants"},
		{"invalid TRUSTED_PROXIES", map[string]string{"TENANT_A_TOKEN": tokenA, "TRUSTED_PROXIES": "10.0.0.0/33"}, "Invalid configuration", "TRUSTED_PROXIES"},
		{"negative RATE_LIMIT_MAX", map[string]string{"TENANT_A_TOKEN": tokenA, "RATE_LIMIT_MAX": "-1"}, "Invalid configuration", "must be positive"},
		{"negative RATE_LIMIT_WINDOW", map[string]string{"TENANT_A_TOKEN": tokenA, "RATE_LIMIT_WINDOW": "-5s"}, "Invalid configuration", "must be positive"},
		{"invalid MEMORY_LIMIT_BYTES", map[string]string{"TENANT_A_TOKEN": tokenA, "MEMORY_LIMIT_BYTES": "256Mi"}, "Invalid configuration", "MEMORY_LIMIT_BYTES"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isolateEnv(t, tt.env)
			logs := &syncBuffer{}
			listened := false
			listen := func(string, string) (net.Listener, error) {
				listened = true
				return nil, errors.New("must not listen")
			}

			code := run(context.Background(), runOptions{stdout: logs, listen: listen, shutdownTimeout: time.Second})

			assert.Equal(t, 1, code)
			assert.False(t, listened, "must fail before listening")
			rec := logs.find(t, tt.message)
			require.NotNil(t, rec, "missing %q log; got %v", tt.message, logs.lines(t))
			assert.Equal(t, "error", rec["level"])
			assert.Contains(t, rec["error"], tt.errPart)
		})
	}
}

// Environment variables reach the running server: port, log level, rate
// limit, trusted proxies and both token sources.
func TestRun_AppliesEnvConfig(t *testing.T) {
	r := startRun(t, map[string]string{
		"HTTP_PORT":         "9191", // fallback when PORT is unset
		"LOG_LEVEL":         "debug",
		"RATE_LIMIT_MAX":    "2",
		"RATE_LIMIT_WINDOW": "1m",
		"TRUSTED_PROXIES":   "192.0.2.1",
		"TENANT_A_TOKEN":    tokenA,
		"VALID_TOKENS_JSON": `{"legacy-b":"` + tokenB + `"}`,
	}, time.Second)

	assert.Equal(t, ":9191", r.rec.address)
	assert.Equal(t, "tcp4", r.rec.network, "same network as fiber's app.Listen")

	resp := r.validate(t, tokenA)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "a", resp.Header.Get("X-Scope-OrgID"))
	assert.Equal(t, "legacy-b", r.validate(t, tokenB).Header.Get("X-Scope-OrgID"))

	// RATE_LIMIT_MAX=2: the third failure is limited, a valid token is not.
	assert.Equal(t, http.StatusUnauthorized, r.validate(t, "wrong").StatusCode)
	assert.Equal(t, http.StatusUnauthorized, r.validate(t, "wrong").StatusCode)
	assert.Equal(t, http.StatusTooManyRequests, r.validate(t, "wrong").StatusCode)
	assert.Equal(t, http.StatusOK, r.validate(t, tokenA).StatusCode)

	r.cancel()
	assert.Equal(t, 0, r.wait(t))

	limiter := r.logs.find(t, "Rate limiting enabled on /validate-token")
	require.NotNil(t, limiter)
	assert.Equal(t, float64(2), limiter["max_failed_attempts"])
	assert.Equal(t, "1m0s", limiter["window"])

	start := r.logs.find(t, "Starting authentication service")
	require.NotNil(t, start)
	assert.Equal(t, ":9191", start["address"])

	assert.Nil(t, r.logs.find(t, "TRUSTED_PROXIES not set: X-Forwarded-For is ignored and rate limiting keys on the proxy IP"))
	loaded := r.logs.find(t, "Loaded tenant tokens")
	require.NotNil(t, loaded)
	assert.Equal(t, float64(2), loaded["count"])

	// LOG_LEVEL=debug: successful requests are logged.
	var debugSuccess bool
	for _, rec := range r.logs.lines(t) {
		if rec["message"] == "HTTP request" && rec["level"] == "debug" && rec["tenant_id"] == "a" {
			debugSuccess = true
		}
	}
	assert.True(t, debugSuccess, "LOG_LEVEL=debug logs successful requests")
}

// Defaults: PORT=8080 and rate limiting with the TRUSTED_PROXIES warning.
func TestRun_Defaults(t *testing.T) {
	r := startRun(t, map[string]string{"TENANT_A_TOKEN": tokenA}, time.Second)

	assert.Equal(t, ":8080", r.rec.address)
	assert.NotNil(t, r.logs.find(t, "TRUSTED_PROXIES not set: X-Forwarded-For is ignored and rate limiting keys on the proxy IP"))
	limiter := r.logs.find(t, "Rate limiting enabled on /validate-token")
	require.NotNil(t, limiter)
	assert.Equal(t, float64(100), limiter["max_failed_attempts"])

	// LOG_LEVEL defaults to info: successful requests are not logged.
	assert.Equal(t, http.StatusOK, r.validate(t, tokenA).StatusCode)
	for _, rec := range r.logs.lines(t) {
		assert.NotEqual(t, "debug", rec["level"])
	}

	r.cancel()
	assert.Equal(t, 0, r.wait(t))
}

// Cancelling the context (what SIGTERM/SIGINT do) shuts the server down
// cleanly: exit code 0, shutdown logged in order, port closed.
func TestRun_GracefulShutdown(t *testing.T) {
	r := startRun(t, map[string]string{"TENANT_A_TOKEN": tokenA, "RATE_LIMIT_ENABLED": "false"}, 5*time.Second)
	assert.Equal(t, http.StatusOK, r.validate(t, tokenA).StatusCode)

	start := time.Now()
	r.cancel()
	assert.Equal(t, 0, r.wait(t))
	assert.Less(t, time.Since(start), 2*time.Second, "idle server shuts down without waiting for the timeout")

	var messages []string
	for _, rec := range r.logs.lines(t) {
		messages = append(messages, rec["message"].(string))
	}
	assert.Subset(t, messages, []string{"Rate limiting disabled", "Shutdown signal received, stopping server...", "Shutdown complete"})
	assert.Greater(t, indexOf(messages, "Shutdown complete"), indexOf(messages, "Shutdown signal received, stopping server..."))
	assert.Nil(t, r.logs.find(t, "Error during shutdown"))

	_, err := net.DialTimeout("tcp", r.ln.Addr().String(), time.Second)
	assert.Error(t, err, "listener closed after shutdown")
}

// An idle keep-alive connection, as ingress proxies keep to the gateway,
// does not block the graceful shutdown.
func TestRun_ShutdownWithIdleKeepAliveConnection(t *testing.T) {
	r := startRun(t, map[string]string{"TENANT_A_TOKEN": tokenA}, 3*time.Second)

	conn, err := net.Dial("tcp", r.ln.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	_, err = io.WriteString(conn, "GET /validate-token HTTP/1.1\r\nHost: test\r\nAuthorization: Bearer "+tokenA+"\r\n\r\n")
	require.NoError(t, err)
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	// conn stays open and idle, like a proxy's keep-alive pool.

	r.cancel()
	assert.Equal(t, 0, r.wait(t), "logs: %v", r.logs.lines(t))
	assert.NotNil(t, r.logs.find(t, "Shutdown complete"))
}

// A request still in flight when the shutdown timeout expires makes run
// return 1 instead of hanging.
func TestRun_ShutdownTimeout(t *testing.T) {
	const timeout = 200 * time.Millisecond
	r := startRun(t, map[string]string{"TENANT_A_TOKEN": tokenA}, timeout)

	// Headers announce a body that never fully arrives: the connection
	// stays active (not idle), so Shutdown has to wait for it.
	conn, err := net.Dial("tcp", r.ln.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	_, err = io.WriteString(conn, "GET /validate-token HTTP/1.1\r\nHost: test\r\nContent-Length: 100\r\n\r\npartial")
	require.NoError(t, err)
	// Shut down only once the server is reading the request: earlier, the
	// connection may still sit unaccepted in the listen backlog and
	// Shutdown would complete at once.
	select {
	case <-r.rec.serverRead(conn.LocalAddr().String()):
	case <-time.After(5 * time.Second):
		t.Fatal("server did not read the request")
	}

	start := time.Now()
	r.cancel()
	assert.Equal(t, 1, r.wait(t))
	elapsed := time.Since(start)
	assert.GreaterOrEqual(t, elapsed, timeout)
	assert.Less(t, elapsed, 5*time.Second)

	rec := r.logs.find(t, "Error during shutdown")
	require.NotNil(t, rec, "logs: %v", r.logs.lines(t))
	assert.Contains(t, rec["error"], "deadline exceeded")
	assert.Nil(t, r.logs.find(t, "Shutdown complete"))
}

// failingListener fails every Accept with a permanent error.
type failingListener struct{ net.Listener }

func (failingListener) Accept() (net.Conn, error) { return nil, errors.New("accept: boom") }

// Server failures make run return 1 without waiting for a signal.
func TestRun_ServerFailure(t *testing.T) {
	t.Run("listen error", func(t *testing.T) {
		isolateEnv(t, map[string]string{"TENANT_A_TOKEN": tokenA, "PORT": "8181"})
		logs := &syncBuffer{}
		var gotAddr string
		listen := func(_, address string) (net.Listener, error) {
			gotAddr = address
			return nil, errors.New("listen tcp4 :8181: bind: address already in use")
		}

		code := run(context.Background(), runOptions{stdout: logs, listen: listen, shutdownTimeout: time.Second})

		assert.Equal(t, 1, code)
		assert.Equal(t, ":8181", gotAddr)
		rec := logs.find(t, "Server failed")
		require.NotNil(t, rec)
		assert.Contains(t, rec["error"], "address already in use")
		assert.Nil(t, logs.find(t, "Starting authentication service"))
	})

	t.Run("accept error", func(t *testing.T) {
		isolateEnv(t, map[string]string{"TENANT_A_TOKEN": tokenA})
		logs := &syncBuffer{}
		listen := func(string, string) (net.Listener, error) {
			ln, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				return nil, err
			}
			return failingListener{ln}, nil
		}

		done := make(chan int, 1)
		go func() {
			done <- run(context.Background(), runOptions{stdout: logs, listen: listen, shutdownTimeout: time.Second})
		}()

		select {
		case code := <-done:
			assert.Equal(t, 1, code)
		case <-time.After(5 * time.Second):
			t.Fatal("run did not return after the server failed")
		}
		rec := logs.find(t, "Server failed")
		require.NotNil(t, rec)
		assert.Contains(t, rec["error"], "boom")
	})

	// fasthttp reports a listener closed underneath it as a clean stop; run
	// must still exit instead of hanging without a server.
	t.Run("listener closed unexpectedly", func(t *testing.T) {
		r := startRun(t, map[string]string{"TENANT_A_TOKEN": tokenA}, time.Second)

		require.NoError(t, r.ln.Close())

		assert.Equal(t, 1, r.wait(t))
		rec := r.logs.find(t, "Server failed")
		require.NotNil(t, rec)
		assert.Equal(t, "server stopped unexpectedly", rec["error"])
	})
}

// notifyShutdown turns SIGTERM and SIGINT into context cancellation instead
// of killing the process.
func TestNotifyShutdown(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT} {
		t.Run(sig.String(), func(t *testing.T) {
			ctx, stop := notifyShutdown()
			defer stop()

			require.NoError(t, syscall.Kill(os.Getpid(), sig))

			select {
			case <-ctx.Done():
			case <-time.After(5 * time.Second):
				t.Fatalf("context not cancelled by %v", sig)
			}
		})
	}
}

func TestResolveVersionFrom(t *testing.T) {
	info := func(v string) func() (*debug.BuildInfo, bool) {
		return func() (*debug.BuildInfo, bool) {
			bi := &debug.BuildInfo{}
			bi.Main.Version = v
			return bi, true
		}
	}
	noInfo := func() (*debug.BuildInfo, bool) { return nil, false }

	assert.Equal(t, "1.2.3", resolveVersionFrom("1.2.3", info("v9.9.9")), "ldflags value wins")
	assert.Equal(t, "v1.4.0", resolveVersionFrom("dev", info("v1.4.0")), "module version from go install")
	assert.Equal(t, "dev", resolveVersionFrom("dev", info("(devel)")))
	assert.Equal(t, "dev", resolveVersionFrom("dev", info("")))
	assert.Equal(t, "dev", resolveVersionFrom("dev", noInfo))
}

// TestMainProcess is not a test on its own: TestMain_* re-execute the test
// binary with PTG_TEST_MAIN=1 to run the real main(), including os.Exit and
// OS signal delivery, in a child process.
func TestMainProcess(t *testing.T) {
	if os.Getenv("PTG_TEST_MAIN") != "1" {
		t.Skip("helper process for TestMain_*")
	}
	main()
}

// mainProcess prepares the test binary to run main() with env, with
// TENANT_* and config variables from the parent removed.
func mainProcess(t *testing.T, env ...string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestMainProcess$")
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		switch {
		case strings.HasPrefix(key, "TENANT_"), key == "VALID_TOKENS_JSON", key == "PORT", key == "HTTP_PORT",
			key == "TRUSTED_PROXIES", strings.HasPrefix(key, "RATE_LIMIT_"), key == "LOG_LEVEL", key == "GOCOVERDIR",
			key == "MEMORY_LIMIT_BYTES":
			continue
		}
		cmd.Env = append(cmd.Env, kv)
	}
	cmd.Env = append(cmd.Env, "PTG_TEST_MAIN=1")
	cmd.Env = append(cmd.Env, env...)
	return cmd
}

func exitCode(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	return exitErr.ExitCode()
}

// main exits with status 1 on invalid configuration.
func TestMain_ExitsOneOnInvalidConfig(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		env        []string
	}{
		{"no tokens", "No valid tokens configured", nil},
		{"invalid TRUSTED_PROXIES", "Invalid configuration", []string{"TENANT_A_TOKEN=" + tokenA, "TRUSTED_PROXIES=nope"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout bytes.Buffer
			cmd := mainProcess(t, tc.env...)
			cmd.Stdout = &stdout

			assert.Equal(t, 1, exitCode(t, cmd.Run()))
			assert.Contains(t, stdout.String(), tc.want)
		})
	}
}

// main shuts down gracefully and exits 0 on SIGTERM and SIGINT.
func TestMain_ExitsZeroOnSignal(t *testing.T) {
	for _, sig := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT} {
		t.Run(sig.String(), func(t *testing.T) {
			cmd := mainProcess(t, "TENANT_A_TOKEN="+tokenA, "PORT=0")
			stdout, err := cmd.StdoutPipe()
			require.NoError(t, err)
			require.NoError(t, cmd.Start())
			t.Cleanup(func() { _ = cmd.Process.Kill() })

			// Wait until the server is listening, keeping the output.
			var out strings.Builder
			scanner := bufio.NewScanner(stdout)
			ready := make(chan struct{})
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				signalled := false
				for scanner.Scan() {
					out.WriteString(scanner.Text() + "\n")
					if !signalled && strings.Contains(scanner.Text(), "Starting authentication service") {
						signalled = true
						close(ready)
					}
				}
			}()
			select {
			case <-ready:
			case <-time.After(10 * time.Second):
				t.Fatal("main did not start")
			}

			require.NoError(t, cmd.Process.Signal(sig))

			waitErr := make(chan error, 1)
			go func() { <-finished; waitErr <- cmd.Wait() }()
			select {
			case err := <-waitErr:
				assert.Equal(t, 0, exitCode(t, err), out.String())
			case <-time.After(15 * time.Second):
				t.Fatal("main did not exit after the signal")
			}
			assert.Contains(t, out.String(), "Shutdown signal received")
			assert.Contains(t, out.String(), "Shutdown complete")
		})
	}
}

func indexOf(s []string, v string) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}
