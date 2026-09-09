//go:build integration

package cmd

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
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
