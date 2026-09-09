package cmd

import (
	"context"
	"crypto/rand"
	"debug/elf"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/kylemclaren/sprite-tunnel/internal/ui"
	sprites "github.com/superfly/sprites-go"
)

func install(ctx context.Context, args []string) (err error) {
	f := flag.NewFlagSet("install", flag.ContinueOnError)
	name := f.String("sprite", "", "existing sprite name")
	auth := f.String("url-auth", "", "set URL access: public or sprite (default: unchanged)")
	apiURL := f.String("api-url", "https://api.sprites.dev", "Production Sprites API base URL")
	apiFile := f.String("api-token-file", "", "Sprites API token file (or SPRITES_TOKEN)")
	binary := f.String("binary", "", "prebuilt linux/amd64 sprite-tunnel")
	source := f.String("source", ".", "source directory for cross-compilation")
	raw := f.String("url", "", "override sprite public URL")
	secretFile := f.String("secret-file", "", "local tunnel secret path")
	if err = parse(f, args); err != nil {
		return err
	}
	secretPath, e := configPath(*name, ".secret")
	if e != nil {
		return e
	}
	if *secretFile != "" {
		secretPath = *secretFile
	}
	if e = validateAuth(*auth); e != nil {
		return e
	}
	token, e := apiToken(*apiFile)
	if e != nil {
		return e
	}
	if e = validateAPI(*apiURL); e != nil {
		return e
	}
	display := ui.New("install / " + *name)
	defer func() { display.Finish(err) }()
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	display.Step("1/5  Finding your sprite")
	api := sprites.New(token, sprites.WithBaseURL(*apiURL), sprites.WithDisableControl())
	defer api.Close()
	remote, e := api.GetSprite(ctx, *name)
	if e != nil {
		return fmt.Errorf("find sprite: %w", e)
	}
	rawURL := *raw
	if rawURL == "" {
		rawURL = remote.URL
	}
	_, public, e := endpoint(*name, rawURL)
	if e != nil {
		return e
	}
	display.Step("2/5  Preparing Linux binary")
	path, cleanup, e := relayBinary(ctx, *binary, *source)
	if e != nil {
		return e
	}
	defer cleanup()
	data, e := os.ReadFile(path)
	if e != nil {
		return e
	}
	display.Step("3/5  Securing and uploading relay")
	s, e := ensureSecret(secretPath)
	if e != nil {
		return e
	}
	nonce := make([]byte, 8)
	if _, e = rand.Read(nonce); e != nil {
		return e
	}
	remoteTemp := "/tmp/sprite-tunnel-" + hex.EncodeToString(nonce)
	fs := remote.Filesystem()
	if e = fs.WriteFileContext(ctx, remoteTemp, data, 0700); e != nil {
		return fmt.Errorf("upload: %w", e)
	}
	defer func() {
		clean, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		_ = fs.RemoveContext(clean, remoteTemp)
	}()
	const remoteSecret = "/home/sprite/.config/sprite-tunnel/relay.secret"
	if e = remoteExec(ctx, *apiURL, token, *name, "mkdir", "-p", "-m", "700", "/home/sprite/.config/sprite-tunnel"); e != nil {
		return fmt.Errorf("create secret directory: %w", e)
	}
	if e = fs.WriteFileContext(ctx, remoteSecret, []byte(s+"\n"), 0600); e != nil {
		return fmt.Errorf("write relay secret: %w", e)
	}
	// Rename a staged executable: writing over a running Linux executable fails ETXTBSY.
	if e = remoteExec(ctx, *apiURL, token, *name, "sudo", "install", "-m", "755", remoteTemp, "/usr/local/bin/sprite-tunnel.new"); e != nil {
		return fmt.Errorf("install binary: %w", e)
	}
	if e = remoteExec(ctx, *apiURL, token, *name, "sudo", "mv", "-f", "/usr/local/bin/sprite-tunnel.new", "/usr/local/bin/sprite-tunnel"); e != nil {
		return e
	}
	display.Step("4/5  Starting tunnel service")
	port := 8080
	stream, e := remote.CreateServiceWithDuration(ctx, "tunnel", &sprites.ServiceRequest{Cmd: "/usr/local/bin/sprite-tunnel", Args: []string{"relay", "--listen", ":8080", "--secret-file", remoteSecret}, HTTPPort: &port}, time.Second)
	if e != nil {
		return fmt.Errorf("create service: %w", e)
	}
	if e = consumeService(stream); e != nil {
		return e
	}
	// An upsert does not necessarily restart an already running executable.
	if e = remoteExec(ctx, *apiURL, token, *name, "sprite-env", "services", "restart", "tunnel"); e != nil {
		return fmt.Errorf("restart service: %w", e)
	}
	if *auth != "" {
		display.Step("Setting URL auth: " + *auth)
		if e = api.UpdateURLSettings(ctx, *name, &sprites.URLSettings{Auth: *auth}); e != nil {
			return e
		}
		remote.URLSettings = &sprites.URLSettings{Auth: *auth}
	}
	display.Step("5/5  Checking public ingress")
	ingressToken := ""
	if remote.URLSettings != nil && remote.URLSettings.Auth != "public" {
		ingressToken = token
	}
	for {
		_, e = fetchHealth(ctx, public, ingressToken)
		if e == nil {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("health check failed: %w", e)
		case <-time.After(500 * time.Millisecond):
		}
	}
	urlPath, e := configPath(*name, ".url")
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(urlPath), 0700); e != nil {
		return e
	}
	if e = os.WriteFile(urlPath, []byte(public+"\n"), 0600); e != nil {
		return e
	}
	display.Step("Relay ready: " + public)
	if ingressToken != "" {
		display.Step("Private ingress: use client --url-auth sprite with your API credential")
	}
	return nil
}
func consumeService(s *sprites.ServiceStream) error {
	defer s.Close()
	for {
		e, err := s.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		if e.Type == "error" || (e.Type == "exit" && e.ExitCode != nil && *e.ExitCode != 0) {
			return errors.New("relay service failed; inspect sprite service logs")
		}
	}
}
func ensureSecret(path string) (string, error) {
	if info, e := os.Lstat(path); e == nil {
		if !info.Mode().IsRegular() {
			return "", errors.New("secret must be a regular file")
		}
		if e = os.Chmod(path, 0600); e != nil {
			return "", e
		}
		return readSecret(path)
	} else if !os.IsNotExist(e) {
		return "", e
	}
	if e := os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return "", e
	}
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	value := hex.EncodeToString(b)
	tmp, e := os.CreateTemp(filepath.Dir(path), ".secret-*")
	if e != nil {
		return "", e
	}
	defer os.Remove(tmp.Name())
	if _, e = tmp.WriteString(value + "\n"); e != nil {
		_ = tmp.Close()
		return "", e
	}
	if e = tmp.Close(); e != nil {
		return "", e
	}
	// Link publishes the complete secret only if the destination does not exist.
	if e = os.Link(tmp.Name(), path); os.IsExist(e) {
		return ensureSecret(path)
	} else if e != nil {
		return "", e
	}
	return value, nil
}
func relayBinary(ctx context.Context, path, source string) (string, func(), error) {
	cleanup := func() {}
	if path == "" && runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
		path, _ = os.Executable()
	}
	if path == "" {
		mod, e := os.ReadFile(filepath.Join(source, "go.mod"))
		if e != nil || !strings.Contains(string(mod), "module github.com/kylemclaren/sprite-tunnel") {
			return "", cleanup, errors.New("run install from sprite-tunnel source, use --source, or provide --binary linux/amd64 executable")
		}
		dir, e := os.MkdirTemp("", "sprite-tunnel-build-")
		if e != nil {
			return "", cleanup, e
		}
		cleanup = func() { _ = os.RemoveAll(dir) }
		path = filepath.Join(dir, "sprite-tunnel")
		build := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", path, ".")
		build.Dir = source
		for _, v := range os.Environ() {
			if !strings.HasPrefix(v, "GOOS=") && !strings.HasPrefix(v, "GOARCH=") && !strings.HasPrefix(v, "CGO_ENABLED=") {
				build.Env = append(build.Env, v)
			}
		}
		build.Env = append(build.Env, "GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0")
		if output, e := build.CombinedOutput(); e != nil {
			cleanup()
			return "", func() {}, fmt.Errorf("build relay: %w: %s", e, output)
		}
	}
	file, e := elf.Open(path)
	if e != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("relay must be a Linux ELF binary: %w", e)
	}
	defer file.Close()
	if file.Machine != elf.EM_X86_64 {
		cleanup()
		return "", func() {}, errors.New("relay binary must target linux/amd64")
	}
	return path, cleanup, nil
}
