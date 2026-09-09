// Package spriteauth reads the Sprite CLI's production selection and credentials.
// It never writes, migrates, or disables the CLI's credential storage.
package spriteauth

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zalando/go-keyring"
)

const ProductionURL = "https://api.sprites.dev"

type Credentials struct{ Token, Organization, Sprite string }
type selection struct {
	URL    string `json:"url"`
	Org    string `json:"org"`
	Sprite string `json:"sprite"`
}
type organization struct {
	Key      string `json:"keyring_key"`
	Token    string `json:"token"`
	APIToken string `json:"api_token"`
	User     string `json:"user_id"`
}
type config struct {
	Selection selection `json:"current_selection"`
	URLs      map[string]struct {
		Orgs map[string]organization `json:"orgs"`
	} `json:"urls"`
	CurrentUser string               `json:"current_user"`
	History     map[string]selection `json:"path_history"`
}

func readConfig(path string) (config, error) {
	var c config
	b, e := os.ReadFile(path)
	if e != nil {
		return c, e
	}
	if json.Unmarshal(b, &c) != nil {
		return c, errors.New("cannot parse Sprite CLI configuration")
	}
	return c, nil
}

func Load() (Credentials, error) {
	home, e := os.UserHomeDir()
	if e != nil {
		return Credentials{}, e
	}
	cwd, e := os.Getwd()
	if e != nil {
		return Credentials{}, e
	}
	return load(home, cwd, keyring.Get)
}

func load(home, cwd string, getKey func(string, string) (string, error)) (Credentials, error) {
	c, e := readConfig(filepath.Join(home, ".sprites", "sprites.json"))
	if os.IsNotExist(e) {
		c, e = readConfig(filepath.Join(home, ".sprites", "config.json"))
	}
	if e != nil && !os.IsNotExist(e) {
		return Credentials{}, e
	}
	selected := c.Selection
	// Nearest directory selection wins over global selection.
	for dir := cwd; ; dir = filepath.Dir(dir) {
		path := filepath.Join(dir, ".sprite")
		if info, e := os.Stat(path); e == nil && info.IsDir() {
			path = filepath.Join(path, "selected")
		}
		if b, e := os.ReadFile(path); e == nil {
			var local struct {
				Org    string `json:"organization"`
				Sprite string `json:"sprite"`
			}
			if json.Unmarshal(b, &local) != nil {
				return Credentials{}, errors.New("cannot parse .sprite selection")
			}
			selected.Org, selected.Sprite = local.Org, local.Sprite
			selected.URL = ProductionURL
			break
		} else if !os.IsNotExist(e) {
			return Credentials{}, errors.New("cannot read .sprite selection")
		}
		if h, ok := c.History[dir]; ok {
			selected = h
			break
		}
		if filepath.Dir(dir) == dir {
			break
		}
	}
	result := Credentials{Organization: selected.Org, Sprite: selected.Sprite}
	if selected.URL != "" && selected.URL != ProductionURL {
		return result, errors.New("sprite CLI has selected a non-production API; select https://api.sprites.dev first")
	}
	// Explicit environment overrides still work without a CLI installation.
	for _, name := range []string{"SPRITES_TOKEN", "SPRITE_TOKEN"} {
		if token := os.Getenv(name); token != "" {
			result.Token = token
			return result, nil
		}
	}
	configs := []config{c}
	if c.CurrentUser != "" && safeComponent(c.CurrentUser) {
		sum := sha256.Sum256([]byte(c.CurrentUser))
		path := filepath.Join(home, ".sprites", "users", fmt.Sprintf("%s-%x.json", c.CurrentUser, sum[:8]))
		user, e := readConfig(path)
		if e == nil {
			configs = append([]config{user}, configs...)
		} else if !os.IsNotExist(e) {
			return result, e
		}
	}
	for _, cfg := range configs {
		org, ok := cfg.URLs[ProductionURL].Orgs[selected.Org]
		if !ok {
			continue
		}
		for _, token := range []string{org.Token, org.APIToken} {
			if token != "" {
				result.Token = token
				return result, nil
			}
		}
		if org.Key == "" || strings.ContainsRune(org.Key, 0) {
			continue
		}
		user := c.CurrentUser
		if org.User != "" {
			user = org.User
		}
		services := []string{"sprites-cli:manual-tokens", "sprites-cli"}
		if user != "" && safeComponent(user) {
			services = append([]string{"sprites-cli:" + user}, services...)
		}
		for _, service := range services {
			if token, e := getKey(service, org.Key); e == nil && strings.TrimSpace(token) != "" {
				result.Token = strings.TrimSpace(token)
				return result, nil
			}
			for _, root := range []string{".sprites", ".sprite"} {
				base := filepath.Join(home, root, "keyring", strings.ReplaceAll(service, ":", "-"))
				path := filepath.Join(base, strings.ReplaceAll(org.Key, ":", "-"))
				rel, e := filepath.Rel(base, path)
				if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					continue
				}
				if b, e := os.ReadFile(path); e == nil && strings.TrimSpace(string(b)) != "" {
					result.Token = strings.TrimSpace(string(b))
					return result, nil
				}
			}
		}
	}
	return result, errors.New("not logged in to Sprites; run `sprite login` first")
}
func safeComponent(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, "/\\\x00")
}
