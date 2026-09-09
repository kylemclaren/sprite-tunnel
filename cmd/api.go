package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/coder/websocket"
	sprites "github.com/superfly/sprites-go"
)

func apiToken(path string) (string, error) {
	if path != "" {
		return readSecret(path)
	}
	if s := os.Getenv("SPRITES_TOKEN"); s != "" {
		return s, nil
	}
	return "", errors.New("provide SPRITES_TOKEN or --api-token-file")
}
func validateAuth(mode string) error {
	if mode != "" && mode != "public" && mode != "sprite" {
		return errors.New("--url-auth must be public or sprite")
	}
	return nil
}
func validateAPI(raw string) error {
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" {
		return errors.New("invalid API base URL")
	}
	if raw != "https://api.sprites.dev" && (u.Scheme != "http" || (u.Hostname() != "localhost" && u.Hostname() != "127.0.0.1")) {
		return errors.New("use the production API https://api.sprites.dev (loopback HTTP is allowed for local tests)")
	}
	return nil
}

// setURLAuth only runs for an explicit --url-auth choice.
func setURLAuth(ctx context.Context, name, base, token, mode string) (string, error) {
	if !validName.MatchString(name) {
		return "", errors.New("--url-auth requires --sprite NAME")
	}
	if e := validateAuth(mode); e != nil {
		return "", e
	}
	if e := validateAPI(base); e != nil {
		return "", e
	}
	api := sprites.New(token, sprites.WithBaseURL(base), sprites.WithDisableControl())
	defer api.Close()
	if e := api.UpdateURLSettings(ctx, name, &sprites.URLSettings{Auth: mode}); e != nil {
		return "", e
	}
	s, e := api.GetSprite(ctx, name)
	if e != nil {
		return "", e
	}
	return s.URL, nil
}

// remoteExec uses the documented exec wire protocol for four short setup commands.
// sprites-go v0.2.0's exec close path races with its writer (SetWriteDeadline);
// keep using the SDK for filesystem/services while using coder/websocket here.
func remoteExec(ctx context.Context, base, token, name string, args ...string) error {
	u, e := url.Parse(base)
	if e != nil {
		return e
	}
	u.Path = "/v1/sprites/" + name + "/exec"
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	q := u.Query()
	for _, arg := range args {
		q.Add("cmd", arg)
	}
	q.Set("stdin", "false")
	q.Set("tty", "false")
	u.RawQuery = q.Encode()
	headers := http.Header{"Authorization": []string{"Bearer " + token}}
	hc := &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	c, resp, e := websocket.Dial(ctx, u.String(), &websocket.DialOptions{HTTPHeader: headers, HTTPClient: hc})
	if e != nil {
		if resp != nil {
			return fmt.Errorf("remote exec handshake: HTTP %d", resp.StatusCode)
		}
		return errors.New("remote exec connection failed")
	}
	defer c.CloseNow()
	for {
		typ, b, e := c.Read(ctx)
		if e != nil {
			return errors.New("remote exec ended without exit status")
		}
		code := -1
		if typ == websocket.MessageBinary && len(b) >= 2 && b[0] == 3 {
			code = int(b[1])
		}
		if typ == websocket.MessageText {
			var event struct {
				Type string `json:"type"`
				Code int    `json:"exit_code"`
			}
			if json.Unmarshal(b, &event) == nil && event.Type == "exit" {
				code = event.Code
			}
		}
		if code == 0 {
			return nil
		}
		if code > 0 {
			return fmt.Errorf("remote command %s exited %d", strings.Join(args, " "), code)
		}
	}
}
