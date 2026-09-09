package cmd

import (
	"context"
	"errors"
	"flag"
	"github.com/kylemclaren/sprite-tunnel/internal/tunnel"
	"log"
	"net/http"
	"os"
	"time"
)

func relay(ctx context.Context, args []string) error {
	f := flag.NewFlagSet("relay", flag.ContinueOnError)
	listen := f.String("listen", ":8080", "HTTP listen address")
	path := f.String("secret-file", "", "tunnel secret file (overrides TUNNEL_SECRET)")
	if e := parse(f, args); e != nil {
		return e
	}
	s, e := secret("", *path)
	if e != nil {
		return e
	}
	logger := log.New(os.Stdout, "", log.LstdFlags)
	r, e := tunnel.NewRelay(s, logger)
	if e != nil {
		return e
	}
	defer r.Close()
	server := &http.Server{Addr: *listen, Handler: r, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second}
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = r.Close()
			stop, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = server.Shutdown(stop)
		case <-done:
		}
	}()
	logger.Printf("relay listening on %s", *listen)
	e = server.ListenAndServe()
	if errors.Is(e, http.ErrServerClosed) {
		return nil
	}
	return e
}
