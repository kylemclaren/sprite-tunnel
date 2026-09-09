package cmd

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/kylemclaren/sprite-tunnel/internal/tunnel"
	"github.com/kylemclaren/sprite-tunnel/internal/ui"
	"net/http"
	"os"
	"time"
)

func fetchHealth(ctx context.Context, public, token string) (tunnel.Health, error) {
	var h tunnel.Health
	req, e := http.NewRequestWithContext(ctx, "GET", public+"/_tunnel/health", nil)
	if e != nil {
		return h, e
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	hc := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	resp, e := hc.Do(req)
	if e != nil {
		return h, e
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return h, fmt.Errorf("health: HTTP %d", resp.StatusCode)
	}
	e = json.NewDecoder(resp.Body).Decode(&h)
	return h, e
}
func status(ctx context.Context, args []string) (err error) {
	f := flag.NewFlagSet("status", flag.ContinueOnError)
	name := f.String("sprite", "", "sprite name")
	raw := f.String("url", "", "override public or control URL")
	ingress := f.String("ingress-token-file", "", "private ingress credential")
	asJSON := f.Bool("json", false, "machine-readable JSON")
	if err = parse(f, args); err != nil {
		return err
	}
	_, public, e := endpoint(*name, *raw)
	if e != nil {
		return e
	}
	token := ""
	if *ingress != "" {
		token, e = readSecret(*ingress)
		if e != nil {
			return e
		}
	}
	var display *ui.UI
	if !*asJSON {
		display = ui.New("status")
		defer func() { display.Finish(err) }()
		display.Step("Checking " + public)
	}
	h, e := fetchHealth(ctx, public, token)
	if e != nil {
		return e
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(h)
	}
	if h.Connected && h.Since != nil {
		display.Step(fmt.Sprintf("Connected  •  %d streams  •  since %s", h.Streams, h.Since.Format(time.RFC3339)))
	} else {
		display.Step("Relay ready  •  waiting for a client")
	}
	return nil
}
