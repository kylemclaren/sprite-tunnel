package tunnel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func setup(t *testing.T) (*Relay, *httptest.Server) {
	t.Helper()
	r, e := NewRelay("test-secret", nil)
	if e != nil {
		t.Fatal(e)
	}
	s := httptest.NewServer(r)
	t.Cleanup(func() { _ = r.Close(); s.Close() })
	return r, s
}
func startClient(t *testing.T, relay, target, secret string) (context.CancelFunc, <-chan error, <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	up := make(chan struct{}, 20)
	c := &Client{URL: "ws" + strings.TrimPrefix(relay, "http") + "/_tunnel", Target: strings.TrimPrefix(target, "http://"), Secret: secret, OnConnect: func() { up <- struct{}{} }}
	go func() { done <- c.Run(ctx) }()
	t.Cleanup(cancel)
	return cancel, done, up
}
func waitUp(t *testing.T, up <-chan struct{}) {
	t.Helper()
	select {
	case <-up:
	case <-time.After(5 * time.Second):
		t.Fatal("connection timeout")
	}
}
func request(base, method, path, body string) (int, string, error) {
	req, e := http.NewRequest(method, base+path, strings.NewReader(body))
	if e != nil {
		return 0, "", e
	}
	c := &http.Client{Timeout: 4 * time.Second}
	resp, e := c.Do(req)
	if e != nil {
		return 0, "", e
	}
	defer resp.Body.Close()
	b, e := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), e
}
func assertRequest(t *testing.T, base, method, path, body string, status int, want string) {
	t.Helper()
	code, b, e := request(base, method, path, body)
	if e != nil || code != status || b != want {
		t.Fatalf("%s: code=%d body=%q error=%v, want %d %q", path, code, b, e, status, want)
	}
}
func echoServer(t *testing.T) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ws" {
			c, e := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
			if e != nil {
				return
			}
			defer c.CloseNow()
			for {
				typ, b, e := c.Read(r.Context())
				if e != nil {
					return
				}
				if c.Write(r.Context(), typ, b) != nil {
					return
				}
			}
		}
		if r.URL.Path == "/headers" {
			_ = json.NewEncoder(w).Encode(r.Header)
			return
		}
		b, _ := io.ReadAll(r.Body)
		fmt.Fprintf(w, "%s %s %s", r.Method, r.URL.RequestURI(), b)
	}))
	t.Cleanup(s.Close)
	return s
}
func TestRoundTrips(t *testing.T) {
	r, s := setup(t)
	assertRequest(t, s.URL, "GET", "/", "", 503, "tunnel not connected\n")
	if h := r.Health(); h.Connected || h.Since != nil || h.Streams != 0 {
		t.Fatalf("health: %+v", h)
	}
	local := echoServer(t)
	cancel, done, up := startClient(t, s.URL, local.URL, "test-secret")
	waitUp(t, up)
	assertRequest(t, s.URL, "GET", "/hello?q=one", "", 200, "GET /hello?q=one ")
	payload := strings.Repeat("body", 1<<19)
	assertRequest(t, s.URL, "POST", "/echo", payload, 200, "POST /echo "+payload)
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, b, e := request(s.URL, "GET", "/parallel", "")
			if e != nil || code != 200 || b != "GET /parallel " {
				t.Errorf("concurrent: %d %q %v", code, b, e)
			}
		}()
	}
	wg.Wait()
	ctx, stop := context.WithTimeout(context.Background(), 4*time.Second)
	defer stop()
	ws, _, e := websocket.Dial(ctx, "ws"+strings.TrimPrefix(s.URL, "http")+"/ws", nil)
	if e != nil {
		t.Fatal(e)
	}
	defer ws.CloseNow()
	if e = ws.Write(ctx, websocket.MessageText, []byte("echo")); e != nil {
		t.Fatal(e)
	}
	_, b, e := ws.Read(ctx)
	if e != nil || string(b) != "echo" {
		t.Fatalf("websocket: %q %v", b, e)
	}
	if h := r.Health(); !h.Connected || h.Since == nil || h.Streams == 0 {
		t.Fatalf("health: %+v", h)
	}
	cancel()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("client failed to stop")
	}
	deadline := time.Now().Add(time.Second)
	for r.Health().Connected && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	assertRequest(t, s.URL, "GET", "/", "", 503, "tunnel not connected\n")
}
func TestTargetDown(t *testing.T) {
	_, s := setup(t)
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	target := listener.Addr().String()
	_ = listener.Close()
	_, _, up := startClient(t, s.URL, target, "test-secret")
	waitUp(t, up)
	assertRequest(t, s.URL, "GET", "/", "", 502, "Bad Gateway\n")
}
func TestReconnect(t *testing.T) {
	r, s := setup(t)
	local := echoServer(t)
	_, _, up := startClient(t, s.URL, local.URL, "test-secret")
	waitUp(t, up)
	assertRequest(t, s.URL, "GET", "/", "", 200, "GET / ")
	r.mu.Lock()
	a := r.current
	r.mu.Unlock()
	_ = a.ws.CloseNow()
	waitUp(t, up)
	assertRequest(t, s.URL, "GET", "/", "", 200, "GET / ")
}
func TestReplacement(t *testing.T) {
	_, s := setup(t)
	a := echoServer(t)
	_, done, up := startClient(t, s.URL, a.URL, "test-secret")
	waitUp(t, up)
	assertRequest(t, s.URL, "GET", "/", "", 200, "GET / ") // Populate old transport's idle pool.
	b := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "new") }))
	defer b.Close()
	_, _, up2 := startClient(t, s.URL, b.URL, "test-secret")
	waitUp(t, up2)
	select {
	case e := <-done:
		if !errors.Is(e, ErrReplaced) {
			t.Fatalf("replacement: %v", e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("replaced client did not exit")
	}
	for i := 0; i < 10; i++ {
		assertRequest(t, s.URL, "GET", "/", "", 200, "new")
	}
}
func TestUnauthorized(t *testing.T) {
	r, s := setup(t)
	_, done, _ := startClient(t, s.URL, "localhost:1", "wrong")
	select {
	case e := <-done:
		if !errors.Is(e, ErrUnauthorized) {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("auth should not retry")
	}
	if r.Health().Connected {
		t.Fatal("bad secret attached")
	}
	assertRequest(t, s.URL, "GET", "/_tunnel", "", 401, "unauthorized\n")
}
func TestHeaders(t *testing.T) {
	_, s := setup(t)
	local := echoServer(t)
	_, _, up := startClient(t, s.URL, local.URL, "test-secret")
	waitUp(t, up)
	req, _ := http.NewRequest("GET", s.URL+"/headers", nil)
	req.Header.Set("Connection", "X-Remove")
	req.Header.Set("X-Remove", "secret")
	req.Header.Set("X-Forwarded-For", "spoof")
	req.Header.Set("X-Forwarded-Proto", "spoof")
	resp, e := http.DefaultClient.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	var h http.Header
	if e = json.NewDecoder(resp.Body).Decode(&h); e != nil {
		t.Fatal(e)
	}
	if h.Get("X-Remove") != "" || strings.Contains(h.Get("X-Forwarded-For"), "spoof") || h.Get("X-Forwarded-Proto") != "http" || h.Get("X-Forwarded-Host") == "" {
		t.Fatalf("headers: %v", h)
	}
}
