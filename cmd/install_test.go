package cmd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/coder/websocket"
)

func TestInstallIdempotent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("NO_COLOR", "1")
	var mu sync.Mutex
	var uploaded []string
	var services, restarts int
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer api-test" {
			t.Error("missing ingress credential")
		}
		io.WriteString(w, `{"connected":false,"since":null,"streams":0}`)
	}))
	defer health.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer api-test" {
			t.Error("missing API credential")
		}
		switch {
		case r.URL.Path == "/v1/sprites/demo":
			_ = json.NewEncoder(w).Encode(map[string]any{"name": "demo", "url": health.URL, "url_settings": map[string]string{"auth": "sprite"}})
		case strings.HasSuffix(r.URL.Path, "/fs/write"):
			if strings.HasSuffix(r.URL.Query().Get("path"), "relay.secret") {
				b, _ := io.ReadAll(r.Body)
				mu.Lock()
				uploaded = append(uploaded, string(b))
				mu.Unlock()
				if r.URL.Query().Get("mode") != "0600" {
					t.Error("secret permissions")
				}
			}
		case strings.HasSuffix(r.URL.Path, "/fs/delete"), strings.HasSuffix(r.URL.Path, "/fs/chmod"):
		case strings.HasSuffix(r.URL.Path, "/services/tunnel"):
			var body struct {
				Cmd  string   `json:"cmd"`
				Args []string `json:"args"`
				Port int      `json:"http_port"`
			}
			if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
				t.Error(e)
			}
			if body.Port != 8080 || body.Cmd != "/usr/local/bin/sprite-tunnel" || !strings.Contains(strings.Join(body.Args, " "), "--secret-file") {
				t.Errorf("bad service: %+v", body)
			}
			mu.Lock()
			services++
			mu.Unlock()
			io.WriteString(w, "{\"type\":\"started\"}\n{\"type\":\"complete\"}\n")
		case strings.HasSuffix(r.URL.Path, "/exec"):
			args := r.URL.Query()["cmd"]
			if strings.Contains(strings.Join(args, " "), "services restart tunnel") {
				mu.Lock()
				restarts++
				mu.Unlock()
			}
			c, e := websocket.Accept(w, r, nil)
			if e != nil {
				t.Error(e)
				return
			}
			_ = c.Write(r.Context(), websocket.MessageBinary, []byte{3, 0})
			_ = c.Close(websocket.StatusNormalClosure, "")
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer api.Close()
	token := filepath.Join(t.TempDir(), "token")
	if e := os.WriteFile(token, []byte("api-test"), 0600); e != nil {
		t.Fatal(e)
	}
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 2; i++ {
		if e := install(context.Background(), []string{"--sprite", "demo", "--api-url", api.URL, "--api-token-file", token, "--binary", exe}); e != nil {
			t.Fatal(e)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if services != 2 || restarts != 2 || len(uploaded) != 2 || uploaded[0] != uploaded[1] || len(strings.TrimSpace(uploaded[0])) != 64 {
		t.Fatalf("idempotency failed: services=%d restarts=%d uploads=%d", services, restarts, len(uploaded))
	}
	path, _ := configPath("demo", ".secret")
	info, e := os.Stat(path)
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("secret permissions: %v %v", info, e)
	}
	_, public, e := endpoint("demo", "")
	if e != nil || public != health.URL {
		t.Fatalf("cached URL: %q %v", public, e)
	}
}

func TestSecretConcurrentCreation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	var wg sync.WaitGroup
	values := make(chan string, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, e := ensureSecret(path)
			if e != nil {
				t.Error(e)
				return
			}
			values <- s
		}()
	}
	wg.Wait()
	close(values)
	want := ""
	for s := range values {
		if want == "" {
			want = s
		}
		if s != want {
			t.Error("concurrent callers got different secrets")
		}
	}
}
func TestEndpointValidation(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, raw := range []string{"ftp://host", "https://user:pass@host", "https://host/?token=secret", "https://host/other"} {
		if _, _, e := endpoint("", raw); e == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	control, public, e := endpoint("demo", "")
	if e != nil || control != "wss://demo.sprites.app/_tunnel" || public != "https://demo.sprites.app" {
		t.Fatalf("%s %s %v", control, public, e)
	}
	if _, e := configPath("../../escape", ".secret"); e == nil {
		t.Error("accepted path traversal")
	}
}

func TestURLAuthSwitch(t *testing.T) {
	var mode string
	var calls int
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer api-test" {
			t.Error("missing API credential")
		}
		if r.Method == http.MethodPut {
			var v struct {
				Settings struct {
					Auth string `json:"auth"`
				} `json:"url_settings"`
			}
			if e := json.NewDecoder(r.Body).Decode(&v); e != nil {
				t.Error(e)
			}
			mode = v.Settings.Auth
			calls++
		}
		io.WriteString(w, `{"name":"demo","url":"https://demo-org.sprites.app"}`)
	}))
	defer api.Close()
	for _, want := range []string{"public", "sprite"} {
		got, e := setURLAuth(context.Background(), "demo", api.URL, "api-test", want)
		if e != nil || mode != want || got != "https://demo-org.sprites.app" {
			t.Fatalf("mode=%s URL=%s error=%v", mode, got, e)
		}
	}
	if _, e := setURLAuth(context.Background(), "demo", api.URL, "api-test", "oops"); e == nil {
		t.Fatal("invalid auth accepted")
	}
	if calls != 2 {
		t.Fatalf("unexpected mutations: %d", calls)
	}
}

func TestProductionAPIOnly(t *testing.T) {
	for _, raw := range []string{"https://api.sprites.dev", "http://127.0.0.1:12345"} {
		if e := validateAPI(raw); e != nil {
			t.Errorf("rejected %s: %v", raw, e)
		}
	}
	for _, raw := range []string{"https://other.example", "http://api.sprites.dev", "https://api.sprites.dev.example", "https://user:pass@api.sprites.dev"} {
		if e := validateAPI(raw); e == nil {
			t.Errorf("accepted nonproduction URL: %s", raw)
		}
	}
}
