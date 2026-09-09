package cmd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestShareArguments(t *testing.T) {
	for _, args := range [][]string{{"3000", "--sprite", "demo", "--public"}, {"--sprite", "demo", "3000", "--public"}, {"--public", "--sprite=demo", "localhost:3000"}} {
		o, e := shareArgs(args)
		if e != nil || o.target != "localhost:3000" || o.sprite != "demo" || o.auth != "public" {
			t.Fatalf("%v: %+v %v", args, o, e)
		}
	}
	for _, args := range [][]string{{}, {"0"}, {"65536"}, {"abc"}, {"3000", "4000"}, {"3000", "--public", "--private"}, {"3000", "--sprite", "../../escape"}} {
		if _, e := shareArgs(args); e == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
func TestPrepareShareCreatesLabelledSpriteAndReusesRelay(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("NO_COLOR", "1")
	t.Setenv("TUNNEL_SECRET", "")
	health := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" && r.Header.Get("Authorization") != "" {
			t.Error("missing private ingress credential")
		}
		io.WriteString(w, `{"connected":false,"since":null,"streams":0}`)
	}))
	defer health.Close()
	created := false
	updates := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			var body struct {
				Name   string   `json:"name"`
				Labels []string `json:"labels"`
			}
			if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
				t.Error(e)
			}
			if body.Name != "demo" || !reflect.DeepEqual(body.Labels, []string{"app:sprite-tunnel"}) {
				t.Errorf("wrong provisioning metadata: %+v", body)
			}
			created = true
			w.WriteHeader(http.StatusCreated)
			io.WriteString(w, `{"name":"demo"}`)
		case r.Method == http.MethodPut:
			updates++
			io.WriteString(w, `{}`)
		case !created:
			http.NotFound(w, r)
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"name": "demo", "url": health.URL, "url_settings": map[string]string{"auth": "sprite"}})
		}
	}))
	defer api.Close()
	public, private, install, e := prepareShare(context.Background(), api.URL, "test-token", "demo", "")
	if e != nil || public != health.URL || !private || !install || !created || updates != 0 {
		t.Fatalf("prepare: private=%t install=%t updates=%d error=%v", private, install, updates, e)
	}
	path, _ := configPath("demo", ".secret")
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, []byte("local-secret"), 0600); e != nil {
		t.Fatal(e)
	}
	_, _, install, e = prepareShare(context.Background(), api.URL, "test-token", "demo", "")
	if e != nil || install || updates != 0 {
		t.Fatalf("did not reuse relay: install=%t error=%v", install, e)
	}
	_, private, _, e = prepareShare(context.Background(), api.URL, "test-token", "demo", "public")
	if e != nil || private || updates != 1 {
		t.Fatalf("explicit access change failed: %v", e)
	}
}
func TestAPITokenUsesCLILogin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("SPRITES_TOKEN", "")
	t.Setenv("SPRITE_TOKEN", "")
	if e := os.MkdirAll(filepath.Join(home, ".sprites"), 0700); e != nil {
		t.Fatal(e)
	}
	s := `{"current_selection":{"url":"https://api.sprites.dev","org":"demo"},"urls":{"https://api.sprites.dev":{"orgs":{"demo":{"token":"cli-test-token"}}}}}`
	if e := os.WriteFile(filepath.Join(home, ".sprites", "sprites.json"), []byte(s), 0600); e != nil {
		t.Fatal(e)
	}
	token, e := apiToken("")
	if e != nil || strings.TrimSpace(token) != "cli-test-token" {
		t.Fatal("CLI login was not reused", e)
	}
}
