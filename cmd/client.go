package cmd

import (
	"context"
	"flag"
	"fmt"
	"github.com/kylemclaren/sprite-tunnel/internal/tunnel"
	"github.com/kylemclaren/sprite-tunnel/internal/ui"
	"log"
	"net"
	"time"
)

func client(ctx context.Context, args []string) (err error) {
	f := flag.NewFlagSet("client", flag.ContinueOnError)
	name := f.String("sprite", "", "sprite name")
	auth := f.String("url-auth", "", "set URL access: public or sprite (default: unchanged)")
	raw := f.String("url", "", "control URL (overrides sprite URL)")
	to := f.String("to", "", "local HOST:PORT")
	path := f.String("secret-file", "", "tunnel secret file")
	verbose := f.Bool("verbose", false, "log stream open/close")
	bootstrap := f.Bool("bootstrap", false, "install relay before connecting")
	apiURL := f.String("api-url", "https://api.sprites.dev", "Sprites API base URL")
	apiFile := f.String("api-token-file", "", "Sprites API token file (or SPRITES_TOKEN)")
	binary := f.String("binary", "", "prebuilt linux/amd64 binary for bootstrap")
	ingress := f.String("ingress-token-file", "", "token for private sprite ingress")
	if err = parse(f, args); err != nil {
		return err
	}
	if _, _, e := net.SplitHostPort(*to); e != nil {
		return fmt.Errorf("invalid --to: %w", e)
	}
	if e := validateAuth(*auth); e != nil {
		return e
	}
	ctx, display, finish := ui.Start(ctx, "connect")
	defer func() { finish(err) }()
	token := ""
	if *auth != "" {
		var e error
		token, e = apiToken(*apiFile)
		if e != nil {
			return e
		}
	}
	if *bootstrap {
		a := []string{"--sprite", *name, "--api-url", *apiURL}
		if *auth != "" {
			a = append(a, "--url-auth", *auth)
		}
		if *apiFile != "" {
			a = append(a, "--api-token-file", *apiFile)
		}
		if *binary != "" {
			a = append(a, "--binary", *binary)
		}
		if *raw != "" {
			a = append(a, "--url", *raw)
		}
		if *path != "" {
			a = append(a, "--secret-file", *path)
		}
		if err = install(ctx, a); err != nil {
			return err
		}
	}
	if *auth != "" && !*bootstrap {
		display.Step("Setting URL auth: " + *auth)
		updateCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		resolved, e := setURLAuth(updateCtx, *name, *apiURL, token, *auth)
		cancel()
		if e != nil {
			return e
		}
		if *raw == "" {
			*raw = resolved
		}
	}
	if *auth != "sprite" {
		token = ""
	}
	// Reuse CLI authentication for private ingress even without an access-mode change.
	if *name != "" && *raw == "" {
		if saved, e := apiToken(*apiFile); e == nil {
			resolved, private, e := authenticatedEndpoint(ctx, *apiURL, *name, saved)
			if e != nil {
				return e
			}
			*raw = resolved
			if private {
				token = saved
			}
		}
	}
	control, public, e := endpoint(*name, *raw)
	if e != nil {
		return e
	}
	s, e := secret(*name, *path)
	if e != nil {
		return e
	}
	if *ingress != "" {
		token, e = readSecret(*ingress)
		if e != nil {
			return e
		}
	}
	c := &tunnel.Client{URL: control, Target: *to, Secret: s, IngressToken: token, Logger: log.New(display, "", 0), Verbose: *verbose, OnConnect: func() { display.Step(fmt.Sprintf("tunnel up: %s  →  %s", public, *to)) }}
	err = c.Run(ctx)
	if err == nil {
		display.Step("tunnel closed")
	}
	return err
}
