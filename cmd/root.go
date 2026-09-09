package cmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func Run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: sprite-tunnel <client|relay|install|status> [flags]")
	}
	var err error
	switch args[0] {
	case "version", "--version", "-v":
		fmt.Printf("sprite-tunnel %s (commit %s, built %s)\n", Version, Commit, BuildDate)
	case "relay":
		err = relay(ctx, args[1:])
	case "client":
		err = client(ctx, args[1:])
	case "install":
		err = install(ctx, args[1:])
	case "status":
		err = status(ctx, args[1:])
	case "help", "--help", "-h":
		fmt.Println("sprite-tunnel — your localhost, anywhere\n\n  client   --sprite NAME --to HOST:PORT [--bootstrap]\n  relay    --listen :8080 [--secret-file PATH]\n  install  --sprite NAME [--api-url URL] [--binary PATH]\n  status   --sprite NAME [--json]\n\nUse <command> --help for options. Set NO_COLOR=1 for plain output.")
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	return err
}

var validName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,62}$`)

func configPath(name, ext string) (string, error) {
	if !validName.MatchString(name) {
		return "", errors.New("invalid sprite name")
	}
	home, e := os.UserHomeDir()
	if e != nil {
		return "", e
	}
	return filepath.Join(home, ".config", "sprite-tunnel", name+ext), nil
}
func readSecret(path string) (string, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return "", e
	}
	s := strings.TrimSpace(string(b))
	if s == "" {
		return "", errors.New("secret file is empty")
	}
	return s, nil
}
func secret(name, path string) (string, error) {
	if path != "" {
		return readSecret(path)
	}
	if s := os.Getenv("TUNNEL_SECRET"); s != "" {
		return s, nil
	}
	p, e := configPath(name, ".secret")
	if e != nil {
		return "", errors.New("provide --secret-file or TUNNEL_SECRET")
	}
	return readSecret(p)
}
func endpoint(name, raw string) (control, public string, err error) {
	if raw == "" {
		if !validName.MatchString(name) {
			return "", "", errors.New("provide --sprite or --url")
		}
		p, e := configPath(name, ".url")
		if e != nil {
			return "", "", e
		}
		b, e := os.ReadFile(p)
		if e == nil {
			raw = strings.TrimSpace(string(b))
		} else if !os.IsNotExist(e) {
			return "", "", e
		} else {
			raw = "https://" + name + ".sprites.app"
		}
	}
	u, e := url.Parse(raw)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", "", errors.New("invalid URL: expected host without credentials, query or fragment")
	}
	switch u.Scheme {
	case "http", "ws":
		u.Scheme = "ws"
	case "https", "wss":
		u.Scheme = "wss"
	default:
		return "", "", errors.New("URL must use http(s) or ws(s)")
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/_tunnel"
	}
	if u.Path != "/_tunnel" {
		return "", "", errors.New("control URL path must be /_tunnel")
	}
	control = u.String()
	u.Path = ""
	u.RawPath = ""
	if u.Scheme == "ws" {
		u.Scheme = "http"
	} else {
		u.Scheme = "https"
	}
	return control, u.String(), nil
}
func parse(f *flag.FlagSet, args []string) error {
	if e := f.Parse(args); e != nil {
		return e
	}
	if f.NArg() != 0 {
		return errors.New("unexpected positional arguments")
	}
	return nil
}
