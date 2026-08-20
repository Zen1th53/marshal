package cli

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"

	"github.com/Zen1th53/marshal/internal/webcontrol"
)

func (c *command) web(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: marshal web <serve|code> [options]")
	}

	switch args[0] {
	case "serve":
		return c.webServe(ctx, args[1:])
	case "code", "login":
		return c.webCode(ctx, args[1:])
	default:
		return fmt.Errorf("unknown web command: %s", args[0])
	}
}

func (c *command) webCode(ctx context.Context, args []string) error {
	store := webcontrol.NewSessionStore()
	code, err := store.CreateOneTimeCode("operator", "admin")
	if err != nil {
		return fmt.Errorf("generate login code: %w", err)
	}

	fmt.Fprintf(c.stdout, "One-Time Login Code: %s\n", code)
	fmt.Fprintf(c.stdout, "Login URL: http://127.0.0.1:8787/login?code=%s\n", code)
	fmt.Fprintf(c.stdout, "(Code expires in 5 minutes and can only be redeemed once)\n")
	return nil
}

func (c *command) webServe(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("web serve", flag.ContinueOnError)
	fs.SetOutput(c.stderr)

	host := fs.String("listen", "127.0.0.1", "Host address to listen on")
	port := fs.Int("port", 8787, "Port to listen on")
	allowInsecure := fs.Bool("allow-insecure-non-loopback", false, "Allow binding to non-loopback without TLS")

	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg := webcontrol.ServerConfig{
		Host:                     *host,
		Port:                     *port,
		AllowInsecureNonLoopback: *allowInsecure,
	}

	srv, err := webcontrol.NewServer(cfg, nil)
	if err != nil {
		return err
	}

	addr := fmt.Sprintf("%s:%d", *host, *port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	defer ln.Close()

	fmt.Fprintf(c.stdout, "MARSHAL Web Control Plane listening on http://%s\n", addr)

	if initialCode, err := srv.Sessions().CreateOneTimeCode("operator", "admin"); err == nil {
		fmt.Fprintf(c.stdout, "One-Time Login Code: %s\n", initialCode)
		fmt.Fprintf(c.stdout, "Direct Login URL:    http://%s/login?code=%s\n", addr, initialCode)
	}

	httpServer := &http.Server{
		Handler: srv.Handler(),
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.Serve(ln)
	}()

	select {
	case <-ctx.Done():
		return httpServer.Shutdown(context.Background())
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	}
}
