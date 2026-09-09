//go:build integration

package tunnel

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// This test uses a preinstalled, disposable relay; it never provisions resources.
func TestLiveIngress(t *testing.T) {
	base := os.Getenv("TEST_TUNNEL_URL")
	if base == "" {
		t.Skip("set TEST_TUNNEL_URL and TEST_TUNNEL_SECRET_FILE")
	}
	raw, e := os.ReadFile(os.Getenv("TEST_TUNNEL_SECRET_FILE"))
	if e != nil {
		t.Fatal(e)
	}
	ingress := ""
	if p := os.Getenv("TEST_INGRESS_TOKEN_FILE"); p != "" {
		b, e := os.ReadFile(p)
		if e != nil {
			t.Fatal(e)
		}
		ingress = strings.TrimSpace(string(b))
	}
	headers := http.Header{}
	if ingress != "" {
		headers.Set("Authorization", "Bearer "+ingress)
	}
	do := func(method, path, body string) (int, string, error) {
		req, e := http.NewRequest(method, base+path, strings.NewReader(body))
		if e != nil {
			return 0, "", e
		}
		req.Header = headers.Clone()
		hc := &http.Client{Timeout: 10 * time.Second}
		r, e := hc.Do(req)
		if e != nil {
			return 0, "", e
		}
		defer r.Body.Close()
		b, e := io.ReadAll(r.Body)
		return r.StatusCode, string(b), e
	}
	if code, b, e := do("GET", "/", ""); e != nil || code != 503 {
		t.Fatalf("before attach: %d %q %v", code, b, e)
	}
	local := echoServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	up := make(chan struct{}, 10)
	done := make(chan error, 1)
	c := &Client{URL: "wss" + strings.TrimPrefix(base, "https") + "/_tunnel", Target: strings.TrimPrefix(local.URL, "http://"), Secret: strings.TrimSpace(string(raw)), IngressToken: ingress, OnConnect: func() { up <- struct{}{} }}
	go func() { done <- c.Run(ctx) }()
	select {
	case <-up:
	case e := <-done:
		t.Fatal(e)
	case <-time.After(20 * time.Second):
		t.Fatal("live connect timeout")
	}
	for _, v := range []struct{ method, path, body string }{{"GET", "/live?q=1", ""}, {"POST", "/live", strings.Repeat("payload", 20000)}} {
		code, b, e := do(v.method, v.path, v.body)
		if e != nil || code != 200 || b != fmt.Sprintf("%s %s %s", v.method, v.path, v.body) {
			t.Fatalf("HTTP %s: code=%d err=%v", v.method, code, e)
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, _, e := do("GET", "/concurrent", "")
			if e != nil || code != 200 {
				t.Errorf("concurrent: %d %v", code, e)
			}
		}()
	}
	wg.Wait()
	wsCtx, stop := context.WithTimeout(ctx, 10*time.Second)
	defer stop()
	ws, _, e := websocket.Dial(wsCtx, "wss"+strings.TrimPrefix(base, "https")+"/ws", &websocket.DialOptions{HTTPHeader: headers})
	if e != nil {
		t.Fatal(e)
	}
	if e = ws.Write(wsCtx, websocket.MessageText, []byte("live websocket echo")); e != nil {
		t.Fatal(e)
	}
	_, b, e := ws.Read(wsCtx)
	if e != nil || string(b) != "live websocket echo" {
		t.Fatalf("WebSocket: %q %v", b, e)
	}
	_ = ws.CloseNow()
	t.Log("HTTP, POST, ten concurrent requests and WebSocket echo passed through real ingress")
	timer := time.NewTimer(35 * time.Second)
	defer timer.Stop()
	select {
	case <-timer.C:
	case e := <-done:
		t.Fatalf("idle disconnected: %v", e)
	}
	select {
	case <-up:
		t.Fatal("unexpected reconnect during idle")
	default:
	}
	if code, _, e := do("GET", "/after-idle", ""); e != nil || code != 200 {
		t.Fatalf("after idle: %d %v", code, e)
	}
	t.Log("same session survived 35 idle seconds with yamux keepalives")
	cancel()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stop timeout")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		code, _, e := do("GET", "/", "")
		if e == nil && code == 503 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after exit: %d %v", code, e)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
