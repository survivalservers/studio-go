package server

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	api "github.com/survivalservers/studio-go"
)

// Serve runs the MCP server for "survivalservers mcp", from the arguments after "mcp", until ctx ends:
//
//	survivalservers mcp                          local server over stdio (what an editor starts); the key is GS_API_KEY
//	survivalservers mcp -http 127.0.0.1:8080     hosted server over HTTP at /mcp; each request brings its own key
//
// Settings (local server): GS_API_KEY, GS_API_URL (default https://studio.survivalservers.com), GS_READ_ONLY=1, GS_APPS=slug,slug, GS_MAX_TTL=2h (0: no limit).
// Hosted server's sign-in for AI tools: GS_MCP_RESOURCE (its own address), GS_AUTH_URL (the sign-in server; default
// GS_API_URL) and GS_MCP_EXCHANGE_SECRET_FILE (the 0600 file with the secret it swaps tokens with). A flag error has
// already been written to stderr; ErrUsage says so.
func Serve(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("survivalservers mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	httpAddr := fs.String("http", "", "serve the hosted server over HTTP on this address instead of stdio")
	version := fs.Bool("version", false, "print the version and exit")
	if err := fs.Parse(args); err != nil {
		return ErrUsage
	}
	if *version {
		fmt.Fprintln(stdout, Version)
		return nil
	}
	for _, a := range fs.Args() {
		if strings.HasPrefix(a, "gsk_") {
			return errors.New("an API key was passed as an argument, where other programs on this computer can read it; set GS_API_KEY in the server's environment instead, and revoke that key")
		}
	}
	cfg, err := ConfigFromEnv()
	if err != nil {
		return err
	}
	// With no GS_API_URL the address is the public API's own (ConfigFromEnv). A malformed one is refused here, before
	// anything starts, locally or hosted (the hosted server makes its clients per request).
	if _, err := api.New(cfg.APIURL, "", nil, ""); err != nil {
		return err
	}
	if *httpAddr != "" {
		logger := slog.New(slog.NewTextHandler(stderr, nil))
		if cfg.Key != "" {
			logger.Warn("GS_API_KEY is ignored by the hosted server: each request brings its own key")
		}
		if cfg.Resource != "" {
			// Without its secret no signed-in tool could work: refuse to start rather than answer every one with an error.
			if _, err := readExchangeSecret(cfg.ExchangeSecretFile); err != nil {
				return fmt.Errorf("the sign-in for AI tools (GS_MCP_RESOURCE) needs the exchange secret: %w", err)
			}
		}
		srv := &http.Server{Addr: *httpAddr, Handler: HTTPHandler(cfg, logger), ReadHeaderTimeout: 10 * time.Second}
		go func() {
			<-ctx.Done()
			shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = srv.Shutdown(shutdown)
		}()
		if cfg.Resource != "" {
			logger.Info("sign-in for AI tools is on", "resource", cfg.Resource, "sign_in_server", cfg.AuthServer)
		}
		logger.Info("hosted MCP server", "address", "http://"+*httpAddr+"/mcp")
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
	cfg.Local = true
	s, err := New(cfg)
	if err != nil {
		return err
	}
	if err := s.Run(ctx, &mcp.StdioTransport{}); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// ErrUsage is Serve's answer to a wrong command line, already explained on stderr.
var ErrUsage = errors.New("wrong command line")
