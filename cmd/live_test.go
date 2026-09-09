//go:build integration

package cmd

import (
	"context"
	"fmt"
	"github.com/kylemclaren/sprite-tunnel/internal/spriteauth"
	sprites "github.com/superfly/sprites-go"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestLiveCLIURLAuth(t *testing.T) {
	name := os.Getenv("TEST_SPRITE_NAME")
	if name == "" {
		t.Skip("set TEST_SPRITE_NAME, TEST_API_TOKEN_FILE, TEST_TUNNEL_SECRET_FILE, TEST_TUNNEL_URL")
	}
	file := os.Getenv("TEST_API_TOKEN_FILE")
	token, e := readSecret(file)
	if e != nil {
		t.Fatal(e)
	}
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "CLI round trip") }))
	defer local.Close()
	base := os.Getenv("TEST_TUNNEL_URL")
	for _, mode := range []string{"public", "sprite"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				done <- client(ctx, []string{"--sprite", name, "--to", strings.TrimPrefix(local.URL, "http://"), "--url-auth", mode, "--api-token-file", file, "--secret-file", os.Getenv("TEST_TUNNEL_SECRET_FILE")})
			}()
			hc := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
			deadline := time.Now().Add(30 * time.Second)
			for {
				req, _ := http.NewRequest("GET", base+"/cli", nil)
				if mode == "sprite" {
					req.Header.Set("Authorization", "Bearer "+token)
				}
				resp, e := hc.Do(req)
				if e == nil {
					b, _ := io.ReadAll(resp.Body)
					resp.Body.Close()
					if resp.StatusCode == 200 && string(b) == "CLI round trip" {
						break
					}
				}
				select {
				case e := <-done:
					t.Fatalf("client exited: %v", e)
				default:
				}
				if time.Now().After(deadline) {
					t.Fatalf("%s did not connect: %v", mode, e)
				}
				time.Sleep(200 * time.Millisecond)
			}
			if mode == "sprite" {
				resp, e := hc.Get(base + "/cli")
				if e != nil {
					t.Fatal(e)
				}
				resp.Body.Close()
				if resp.StatusCode == 200 {
					t.Fatal("private URL allowed anonymous visitor")
				}
			}
			cancel()
			select {
			case e := <-done:
				if e != nil {
					t.Fatal(e)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("client failed to exit")
			}
		})
	}
}

// TestLiveShare exercises the real binary using only the existing Sprite CLI login.
// It creates a labelled disposable Sprite and removes it after the test.
func TestLiveShare(t *testing.T) {
	binary := os.Getenv("TEST_SHARE_BINARY")
	if binary == "" {
		t.Skip("set TEST_SHARE_BINARY to test sharing with your Sprite CLI login")
	}
	t.Setenv("SPRITES_TOKEN", "")
	t.Setenv("SPRITE_TOKEN", "")
	credentials, e := spriteauth.Load()
	if e != nil {
		t.Fatal(e)
	}
	api := sprites.New(credentials.Token, sprites.WithBaseURL(spriteauth.ProductionURL), sprites.WithDisableControl())
	defer api.Close()
	name := fmt.Sprintf("tunnel-share-%d", time.Now().UnixNano())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if e := api.DeleteSprite(ctx, name); e != nil {
			t.Errorf("remove disposable Sprite: %v", e)
		}
		for _, suffix := range []string{".secret", ".url"} {
			if path, e := configPath(name, suffix); e == nil {
				_ = os.Remove(path)
			}
		}
	})
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "simple share works") }))
	defer local.Close()
	target := strings.TrimPrefix(local.URL, "http://")
	var previousPID int
	for _, mode := range []string{"private", "public"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			args := []string{target, "--sprite", name}
			if mode == "public" {
				args = append(args, "--public")
			}
			process := exec.CommandContext(ctx, binary, args...)
			process.Stdout = io.Discard
			process.Stderr = os.Stderr
			if e := process.Start(); e != nil {
				t.Fatal(e)
			}
			done := make(chan error, 1)
			go func() { done <- process.Wait() }()
			deadline := time.Now().Add(90 * time.Second)
			var public string
			for {
				remote, e := api.GetSprite(ctx, name)
				if e == nil {
					public = remote.URL
					req, _ := http.NewRequestWithContext(ctx, "GET", public+"/simple", nil)
					if mode == "private" {
						req.Header.Set("Authorization", "Bearer "+credentials.Token)
					}
					hc := &http.Client{Timeout: 3 * time.Second}
					resp, e := hc.Do(req)
					if e == nil {
						b, _ := io.ReadAll(resp.Body)
						resp.Body.Close()
						if resp.StatusCode == 200 && string(b) == "simple share works" {
							labelled := false
							for _, label := range remote.Labels {
								if label == "app:sprite-tunnel" {
									labelled = true
								}
							}
							if !labelled {
								t.Fatal("created Sprite is missing app label")
							}
							break
						}
					}
				}
				select {
				case e := <-done:
					t.Fatalf("share exited: %v", e)
				default:
				}
				if time.Now().After(deadline) {
					t.Fatal("share did not become ready")
				}
				time.Sleep(500 * time.Millisecond)
			}
			svc, e := api.GetService(ctx, name, "tunnel")
			if e != nil {
				t.Fatal(e)
			}
			if mode == "public" && svc.State.PID != previousPID {
				t.Fatal("second share unnecessarily restarted the relay")
			}
			previousPID = svc.State.PID
			if e = process.Process.Signal(os.Interrupt); e != nil {
				t.Fatal(e)
			}
			select {
			case e := <-done:
				if e != nil {
					t.Fatal(e)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("share did not stop")
			}
			t.Logf("%s sharing succeeded at %s using CLI login", mode, public)
		})
	}
}
