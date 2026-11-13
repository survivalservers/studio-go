package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// HTTPHandler is the hosted server: MCP over streamable HTTP at /mcp and a health check at /health. Each
// request's own Authorization header (Bearer <API key>) is the key for that request: the server holds no key of
// its own, never reads files, and keeps nothing between requests, so tool lists answer without a key and every
// tool call is made as the caller. With Config.Resource set, a tool signs in instead (oauth.go): every request
// then needs a token, its sign-in's or an API key. The host check on /mcp is hostGuard's, not the library's.
func HTTPHandler(cfg Config, logger *slog.Logger) http.Handler {
	cfg.Key = ""
	cfg.Local = false
	h := mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server {
		c := cfg
		c.Key = bearer(r.Header.Get("Authorization"))
		if signedIn, _ := r.Context().Value(signedInKey{}).(bool); signedIn {
			c.MaxTTL = 0 // the API holds it to the time limit its member picked
		}
		s, err := New(c)
		if err != nil {
			if logger != nil {
				logger.Error("server setup failed", "err", err)
			}
			return nil
		}
		return s
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, Logger: logger, DisableLocalhostProtection: true})
	mux := http.NewServeMux()
	if cfg.Resource != "" {
		g := newGate(cfg, logger)
		mux.Handle("/mcp", hostGuard(cfg.Resource, markSignedIn(g.wrap(h))))
		mux.HandleFunc("/.well-known/oauth-protected-resource", g.metadata)
		if u, err := url.Parse(cfg.Resource); err == nil && strings.TrimRight(u.Path, "/") != "" {
			mux.HandleFunc("/.well-known/oauth-protected-resource"+strings.TrimRight(u.Path, "/"), g.metadata)
		}
	} else {
		mux.Handle("/mcp", hostGuard("", h))
	}
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok\n"))
	})
	return mux
}

// hostGuard takes the place of the MCP library's guard against DNS rebinding. The library refuses every host name
// but a loopback one on a loopback listener, so behind our web server, which passes the visitor's host name on, it
// refused every request at the public address. Here a request that reaches a
// loopback listener must name a loopback host or this server's own public host (the host of GS_MCP_RESOURCE); any
// other name is still refused, so a page that rebinds its own name to 127.0.0.1 gets nothing. A request that
// reached another listener is left to the gate, as the library leaves it.
func hostGuard(resource string, next http.Handler) http.Handler {
	own := ""
	if u, err := url.Parse(resource); err == nil {
		own = u.Hostname()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if la, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok && la != nil && isLoopback(hostOnly(la.String())) {
			host := hostOnly(r.Host)
			if !isLoopback(host) && (own == "" || !strings.EqualFold(host, own)) {
				why := fmt.Sprintf("Forbidden: invalid Host header %q", r.Host)
				if own == "" {
					why += " (a hosted server behind a web server takes its public name from GS_MCP_RESOURCE)"
				}
				http.Error(w, why, http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// signedInKey marks a request that came with a sign-in token (gso_...). The gate lets one through only once the
// sign-in server swapped it for its agent's API token, and the API holds everything that agent starts to the time
// limit the member picked when approving it (none, if they picked No limit). GS_MAX_TTL (2 hours unless set) stays
// for keys sent as they are: applied to a signed-in tool as well, it refused anything longer before the API saw it,
// so four of the approval page's seven choices did nothing.
type signedInKey struct{}

func markSignedIn(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(bearer(r.Header.Get("Authorization")), mcpTokenPrefix) {
			r = r.WithContext(context.WithValue(r.Context(), signedInKey{}, true))
		}
		next.ServeHTTP(w, r)
	})
}

// hostOnly is the host part of "host:port", "[v6]:port", "host" or "[v6]".
func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return strings.Trim(hostport, "[]")
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.IsLoopback()
}

func bearer(h string) string {
	h = strings.TrimSpace(h)
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}
