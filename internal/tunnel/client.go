package tunnel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
)

var ErrReplaced = errors.New("replaced by another client")
var ErrUnauthorized = errors.New("unauthorized (check tunnel secret and sprite URL authentication)")

type Client struct {
	IngressToken string
	URL          string
	Target       string
	Secret       string
	Logger       *log.Logger
	Verbose      bool
	// Header permits ingress cookies without changing the tunnel bearer credential.
	Header http.Header
	// OnConnect runs synchronously for each successful connection.
	OnConnect func()
}

func (c *Client) Run(ctx context.Context) error {
	u, e := url.Parse(c.URL)
	if e != nil || u.Host == "" || (u.Scheme != "ws" && u.Scheme != "wss") || u.User != nil {
		return errors.New("expected ws:// or wss:// control URL without userinfo")
	}
	if _, _, e := net.SplitHostPort(c.Target); e != nil {
		return fmt.Errorf("invalid target: %w", e)
	}
	if c.Secret == "" {
		return errors.New("tunnel secret is required")
	}
	logger := c.Logger
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	delay := 500 * time.Millisecond
	for {
		if ctx.Err() != nil {
			return nil
		}
		start := time.Now()
		logger.Print("connecting")
		err := c.connect(ctx, logger)
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrReplaced) {
			return err
		}
		if time.Since(start) > 30*time.Second {
			delay = 500 * time.Millisecond
		}
		// Equal jitter: first retry is 250–500ms and the cap is 30s.
		wait := delay/2 + time.Duration(rand.Int64N(int64(delay/2)))
		logger.Printf("disconnected: %v; reconnecting in %s", err, wait.Round(time.Millisecond))
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		delay = min(2*delay, 30*time.Second)
	}
}

// observedConn preserves the application close code before yamux turns it into EOF.
type observedConn struct {
	net.Conn
	replaced atomic.Bool
}

func (c *observedConn) Read(p []byte) (int, error) {
	n, e := c.Conn.Read(p)
	if websocket.CloseStatus(e) == ReplacedCode {
		c.replaced.Store(true)
	}
	return n, e
}

func (c *Client) connect(ctx context.Context, logger *log.Logger) error {
	headers := c.Header.Clone()
	if headers == nil {
		headers = make(http.Header)
	}
	headers.Set("Authorization", "Bearer "+c.Secret)
	if c.IngressToken != "" {
		headers.Set("Authorization", "Bearer "+c.IngressToken)
		headers.Set("X-Tunnel-Authorization", "Bearer "+c.Secret)
	}
	hc := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	ws, resp, err := websocket.Dial(ctx, c.URL, &websocket.DialOptions{HTTPHeader: headers, HTTPClient: hc})
	if err != nil {
		if resp != nil && (resp.StatusCode == 401 || resp.StatusCode == 403) {
			return ErrUnauthorized
		}
		// Do not include response bodies or URLs (which may contain credentials).
		if resp != nil {
			return fmt.Errorf("WebSocket handshake: HTTP %d", resp.StatusCode)
		}
		return errors.New("WebSocket connection failed")
	}
	defer ws.CloseNow()
	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	conn := &observedConn{Conn: websocket.NetConn(sessionCtx, ws, websocket.MessageBinary)}
	session, err := yamux.Server(conn, muxConfig())
	if err != nil {
		return err
	}
	defer session.Close()
	logger.Print("connected")
	if c.OnConnect != nil {
		c.OnConnect()
	}
	var workers sync.WaitGroup
	defer workers.Wait()
	// Cancel local dials/copies immediately when yamux shuts down.
	go func() {
		select {
		case <-session.CloseChan():
			cancel()
		case <-sessionCtx.Done():
		}
	}()
	for {
		stream, err := session.Accept()
		if err != nil {
			cancel()
			if conn.replaced.Load() {
				return ErrReplaced
			}
			return err
		}
		workers.Add(1)
		go func() { defer workers.Done(); c.forward(sessionCtx, stream, logger) }()
	}
}

func (c *Client) forward(ctx context.Context, stream net.Conn, logger *log.Logger) {
	defer stream.Close()
	local, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, "tcp", c.Target)
	if err != nil {
		logger.Print("local target dial failed")
		return
	}
	defer local.Close()
	if c.Verbose {
		logger.Print("stream opened")
		defer logger.Print("stream closed")
	}
	stop := context.AfterFunc(ctx, func() { _ = local.Close(); _ = stream.Close() })
	defer stop()
	done := make(chan struct{})
	go func() { _, _ = io.Copy(local, stream); _ = local.Close(); _ = stream.Close(); close(done) }()
	_, _ = io.Copy(stream, local)
	_ = local.Close()
	_ = stream.Close()
	<-done
}
