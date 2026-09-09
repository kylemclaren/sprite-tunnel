package cmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/kylemclaren/sprite-tunnel/internal/spriteauth"
	"github.com/kylemclaren/sprite-tunnel/internal/tunnel"
	"github.com/kylemclaren/sprite-tunnel/internal/ui"
	sprites "github.com/superfly/sprites-go"
)

type shareOptions struct {
	target, sprite, auth string
	verbose              bool
}

func shareArgs(args []string) (shareOptions, error) {
	var o shareOptions
	f := flag.NewFlagSet("share", flag.ContinueOnError)
	f.StringVar(&o.sprite, "sprite", "", "Sprite to use (default: Sprite CLI selection)")
	public := f.Bool("public", false, "allow anyone with the URL to connect")
	private := f.Bool("private", false, "require Sprite authentication")
	f.BoolVar(&o.verbose, "verbose", false, "show connection details")
	f.Usage = func() {
		fmt.Fprintln(f.Output(), "Usage: sprite-tunnel PORT [--sprite NAME] [--public | --private]\n\nExamples:\n  sprite-tunnel 3000\n  sprite-tunnel 3000 --public\n  sprite-tunnel localhost:3000 --sprite my-app\n\nUses your Sprite CLI login. Sets up the relay automatically.\nOmit --public/--private to keep the Sprite's current access setting.")
		f.PrintDefaults()
	}
	// Accept flags before or after the port while retaining stdlib flag validation.
	var flags, positionals []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") {
			positionals = append(positionals, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(strings.SplitN(a, "=", 2)[0], "-")
		entry := f.Lookup(name)
		if entry != nil && !strings.Contains(a, "=") {
			b, ok := entry.Value.(interface{ IsBoolFlag() bool })
			if (!ok || !b.IsBoolFlag()) && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		}
	}
	if e := f.Parse(flags); e != nil {
		return o, e
	}
	if len(positionals) != 1 {
		return o, errors.New("specify one local port: sprite-tunnel 3000")
	}
	target := positionals[0]
	if !strings.Contains(target, ":") {
		target = net.JoinHostPort("localhost", target)
	}
	host, port, e := net.SplitHostPort(target)
	if e != nil {
		return o, errors.New("target must be PORT or HOST:PORT")
	}
	n, e := strconv.Atoi(port)
	if e != nil || n < 1 || n > 65535 {
		return o, errors.New("port must be between 1 and 65535")
	}
	if host == "" {
		host = "localhost"
	}
	o.target = net.JoinHostPort(host, port)
	if *public && *private {
		return o, errors.New("choose either --public or --private")
	}
	if *public {
		o.auth = "public"
	}
	if *private {
		o.auth = "sprite"
	}
	if o.sprite != "" && !validName.MatchString(o.sprite) {
		return o, errors.New("invalid sprite name")
	}
	return o, nil
}

func share(ctx context.Context, args []string) error {
	return shareAt(ctx, args, spriteauth.ProductionURL)
}
func shareAt(ctx context.Context, args []string, base string) (err error) {
	options, e := shareArgs(args)
	if e != nil {
		return e
	}
	credentials, e := spriteauth.Load()
	if e != nil {
		return e
	}
	name := options.sprite
	if name == "" {
		name = credentials.Sprite
	}
	if name == "" {
		name = "sprite-tunnel"
	}
	if !validName.MatchString(name) {
		return errors.New("invalid selected sprite name")
	}
	if e = validateAPI(base); e != nil {
		return e
	}
	public, private, needsInstall, e := prepareShare(ctx, base, credentials.Token, name, options.auth)
	if e != nil {
		return e
	}
	if needsInstall {
		if e = installUsing(ctx, []string{"--sprite", name, "--api-url", base, "--url", public}, credentials.Token); e != nil {
			return e
		}
	}
	s, e := secret(name, "")
	if e != nil {
		return e
	}
	control, _, e := endpoint(name, public)
	if e != nil {
		return e
	}
	display := ui.New("share / " + name)
	defer func() { display.Finish(err) }()
	access := "public"
	ingress := ""
	if private {
		access = "private"
		ingress = credentials.Token
	}
	c := &tunnel.Client{URL: control, Target: options.target, Secret: s, IngressToken: ingress, Verbose: options.verbose, Logger: log.New(display, "", 0), OnConnect: func() { display.Step(fmt.Sprintf("tunnel up: %s  →  %s  (%s)", public, options.target, access)) }}
	err = c.Run(ctx)
	if err == nil {
		display.Step("tunnel closed")
	}
	return err
}

func prepareShare(ctx context.Context, base, token, name, auth string) (public string, private, needsInstall bool, err error) {
	display := ui.New("prepare / " + name)
	defer func() { display.Finish(err) }()
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	api := sprites.New(token, sprites.WithBaseURL(base), sprites.WithDisableControl())
	defer api.Close()
	display.Step("Finding your Sprite")
	remote, e := api.GetSprite(ctx, name)
	if e != nil {
		// sprites-go v0.2.0 reports GetSprite's HTTP 404 as this specific error.
		if e.Error() != "sprite not found: "+name {
			return "", false, false, e
		}
		display.Step("Creating " + name)
		if _, e = api.CreateSpriteWithOrg(ctx, name, nil, nil, []string{"app:sprite-tunnel"}); e != nil {
			return "", false, false, e
		}
		remote, e = api.GetSprite(ctx, name)
		if e != nil {
			return "", false, false, e
		}
	}
	if auth != "" && (remote.URLSettings == nil || remote.URLSettings.Auth != auth) {
		display.Step("Setting URL access: " + auth)
		if e = api.UpdateURLSettings(ctx, name, &sprites.URLSettings{Auth: auth}); e != nil {
			return "", false, false, e
		}
		remote.URLSettings = &sprites.URLSettings{Auth: auth}
	}
	_, public, e = endpoint(name, remote.URL)
	if e != nil {
		return "", false, false, e
	}
	private = remote.URLSettings == nil || remote.URLSettings.Auth != "public"
	ingress := ""
	if private {
		ingress = token
	}
	localSecret, e := secret(name, "")
	if e != nil || localSecret == "" {
		needsInstall = true
	} else {
		_, e = fetchHealth(ctx, public, ingress)
		needsInstall = e != nil
	}
	display.Step("Sprite ready")
	return public, private, needsInstall, nil
}
