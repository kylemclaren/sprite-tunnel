package spriteauth

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(path, b, 0600); e != nil {
		t.Fatal(e)
	}
}
func noKey(string, string) (string, error) { return "", errors.New("no key") }
func fixture(t *testing.T) (string, string) {
	t.Helper()
	t.Setenv("SPRITES_TOKEN", "")
	t.Setenv("SPRITE_TOKEN", "")
	home := t.TempDir()
	cwd := filepath.Join(home, "project", "child")
	if e := os.MkdirAll(cwd, 0700); e != nil {
		t.Fatal(e)
	}
	return home, cwd
}
func TestUserScopedKeyringAndDirectorySelection(t *testing.T) {
	home, cwd := fixture(t)
	writeJSON(t, filepath.Join(home, ".sprites", "sprites.json"), map[string]any{"current_user": "alice", "current_selection": selection{URL: ProductionURL, Org: "other"}})
	sum := sha256.Sum256([]byte("alice"))
	writeJSON(t, filepath.Join(home, ".sprites", "users", fmt.Sprintf("alice-%x.json", sum[:8])), map[string]any{"urls": map[string]any{ProductionURL: map[string]any{"orgs": map[string]any{"work": organization{Key: "api.sprites.dev:work"}}}}})
	writeJSON(t, filepath.Join(home, "project", ".sprite"), map[string]string{"organization": "work", "sprite": "chosen"})
	got, e := load(home, cwd, func(service, key string) (string, error) {
		if service != "sprites-cli:alice" || key != "api.sprites.dev:work" {
			t.Fatalf("wrong key lookup: %s %s", service, key)
		}
		return "test-token", nil
	})
	if e != nil || got.Token != "test-token" || got.Sprite != "chosen" || got.Organization != "work" {
		t.Fatal("user-scoped credential/selection resolution failed", e)
	}
}
func TestFileFallbackAndPathHistory(t *testing.T) {
	home, cwd := fixture(t)
	writeJSON(t, filepath.Join(home, ".sprites", "sprites.json"), map[string]any{"current_selection": selection{URL: ProductionURL, Org: "work"}, "path_history": map[string]selection{filepath.Dir(cwd): {URL: ProductionURL, Org: "work", Sprite: "history"}}, "urls": map[string]any{ProductionURL: map[string]any{"orgs": map[string]any{"work": organization{Key: "sprites:org:https://api.sprites.dev:work"}}}}})
	path := filepath.Join(home, ".sprites", "keyring", "sprites-cli", "sprites-org-https-", "api.sprites.dev-work")
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, []byte("test-token\n"), 0600); e != nil {
		t.Fatal(e)
	}
	got, e := load(home, cwd, noKey)
	if e != nil || got.Token != "test-token" || got.Sprite != "history" {
		t.Fatal("file fallback failed", e)
	}
}
func TestConfigTokenAndEnvironmentOverride(t *testing.T) {
	home, cwd := fixture(t)
	writeJSON(t, filepath.Join(home, ".sprites", "config.json"), map[string]any{"current_selection": selection{URL: ProductionURL, Org: "work"}, "urls": map[string]any{ProductionURL: map[string]any{"orgs": map[string]any{"work": organization{Token: "stored"}}}}})
	got, e := load(home, cwd, noKey)
	if e != nil || got.Token != "stored" {
		t.Fatal("legacy config fallback failed", e)
	}
	t.Setenv("SPRITE_TOKEN", "override")
	got, e = load(home, cwd, noKey)
	if e != nil || got.Token != "override" {
		t.Fatal("override failed", e)
	}
}
func TestRejectOtherAPIAndMissingLogin(t *testing.T) {
	home, cwd := fixture(t)
	if _, e := load(home, cwd, noKey); e == nil {
		t.Fatal("missing login accepted")
	}
	writeJSON(t, filepath.Join(home, ".sprites", "sprites.json"), map[string]any{"current_selection": selection{URL: "https://other.example", Org: "work"}})
	called := false
	if _, e := load(home, cwd, func(string, string) (string, error) { called = true; return "", nil }); e == nil || called {
		t.Fatal("nonproduction selection touched credentials")
	}
}
