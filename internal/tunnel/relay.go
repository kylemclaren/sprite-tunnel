// Package tunnel carries reverse HTTP connections over a WebSocket/yamux session.
package tunnel

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
)

const ReplacedCode websocket.StatusCode = 4001

func muxConfig() *yamux.Config {
	c := yamux.DefaultConfig()
	c.EnableKeepAlive = true
	c.KeepAliveInterval = 15 * time.Second
	c.MaxStreamWindowSize = 1 << 20
	c.LogOutput = io.Discard
	return c
}

type attachment struct {
	ws        *websocket.Conn
	session   *yamux.Session
	transport *http.Transport
	proxy     *httputil.ReverseProxy
	since     time.Time
}

// Relay is an HTTP handler. Close must be called on shutdown to close hijacked sockets.
type Relay struct {
	secret  [32]byte
	logger  *log.Logger
	mu      sync.Mutex
	current *attachment
	closed  bool
}

type Health struct {
	Connected bool       `json:"connected"`
	Since     *time.Time `json:"since"`
	Streams   int        `json:"streams"`
}

func NewRelay(secret string, logger *log.Logger) (*Relay, error) {
	if secret == "" {
		return nil, errors.New("tunnel secret is required")
	}
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}
	return &Relay{secret: sha256.Sum256([]byte("Bearer " + secret)), logger: logger}, nil
}

func (r *Relay) Health() Health {
	r.mu.Lock()
	defer r.mu.Unlock()
	a := r.current
	if a == nil || a.session.IsClosed() {
		return Health{}
	}
	since := a.since
	return Health{true, &since, a.session.NumStreams()}
}

func (r *Relay) Close() error {
	r.mu.Lock()
	r.closed = true
	a := r.current
	r.current = nil
	r.mu.Unlock()
	if a != nil {
		a.transport.CloseIdleConnections()
		return a.session.Close()
	}
	return nil
}

func (r *Relay) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	start := time.Now()
	lw := &responseLog{ResponseWriter: w}
	defer func() {
		status := lw.status
		if status == 0 {
			status = 200
		}
		r.logger.Printf("%s %q %d %d %s", req.Method, req.URL.Path, status, lw.bytes, time.Since(start))
	}()
	switch req.URL.Path {
	case "/_tunnel":
		if req.Method != "GET" {
			lw.Header().Set("Allow", "GET")
			http.Error(lw, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		r.attach(lw, req)
	case "/_tunnel/health":
		if req.Method != "GET" {
			lw.Header().Set("Allow", "GET")
			http.Error(lw, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		lw.Header().Set("Content-Type", "application/json")
		lw.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(lw).Encode(r.Health())
	default:
		r.mu.Lock()
		a := r.current
		r.mu.Unlock()
		if a == nil || a.session.IsClosed() {
			http.Error(lw, "tunnel not connected", http.StatusServiceUnavailable)
			return
		}
		a.proxy.ServeHTTP(lw, req)
	}
}

func (r *Relay) attach(w http.ResponseWriter, req *http.Request) {
	auth := req.Header.Get("X-Tunnel-Authorization")
	if auth == "" {
		auth = req.Header.Get("Authorization")
	}
	got := sha256.Sum256([]byte(auth))
	if subtle.ConstantTimeCompare(got[:], r.secret[:]) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ws, err := websocket.Accept(w, req, nil)
	if err != nil {
		return
	}
	// The handler remains alive until session shutdown; its context owns NetConn.
	conn := websocket.NetConn(req.Context(), ws, websocket.MessageBinary)
	sess, err := yamux.Client(conn, muxConfig())
	if err != nil {
		_ = ws.CloseNow()
		return
	}
	a := &attachment{ws: ws, session: sess, since: time.Now().UTC()}
	a.transport = &http.Transport{DialContext: func(ctx context.Context, _ string, _ string) (net.Conn, error) { return openStream(ctx, sess) }, MaxIdleConns: 100, MaxIdleConnsPerHost: 100, IdleConnTimeout: 90 * time.Second, ResponseHeaderTimeout: 60 * time.Second}
	a.proxy = &httputil.ReverseProxy{
		Rewrite: func(p *httputil.ProxyRequest) {
			p.SetURL(&url.URL{Scheme: "http", Host: "tunnel"})
			p.Out.Host = p.In.Host
			p.SetXForwarded()
		},
		Transport: a.transport,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			status := 502
			if sess.IsClosed() {
				status = 503
			}
			http.Error(w, http.StatusText(status), status)
		},
		ErrorLog: r.logger,
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		_ = sess.Close()
		return
	}
	old := r.current
	r.current = a
	r.mu.Unlock()
	r.logger.Print("client connected")
	if old != nil {
		// Send the close reason before closing yamux, which would send a normal close.
		_ = old.ws.Close(ReplacedCode, "replaced")
		_ = old.session.Close()
		old.transport.CloseIdleConnections()
	}
	<-sess.CloseChan()
	a.transport.CloseIdleConnections()
	_ = ws.CloseNow()
	r.mu.Lock()
	if r.current == a {
		r.current = nil
	}
	r.mu.Unlock()
	r.logger.Print("client disconnected")
}

func openStream(ctx context.Context, s *yamux.Session) (net.Conn, error) {
	type result struct {
		conn net.Conn
		err  error
	}
	ch := make(chan result)
	go func() {
		c, e := s.Open()
		select {
		case ch <- result{c, e}:
		case <-ctx.Done():
			if c != nil {
				_ = c.Close()
			}
		}
	}()
	select {
	case v := <-ch:
		return v.conn, v.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type responseLog struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *responseLog) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *responseLog) WriteHeader(code int) {
	if w.status == 0 && (code >= 200 || code == 101) {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}
func (w *responseLog) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	n, e := w.ResponseWriter.Write(p)
	w.bytes += n
	return n, e
}
func (w *responseLog) Flush() {
	if w.status == 0 {
		w.WriteHeader(200)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}
func (w *responseLog) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	c, b, e := http.NewResponseController(w.ResponseWriter).Hijack()
	if e == nil && w.status == 0 {
		w.status = 101
	}
	return c, b, e
}
