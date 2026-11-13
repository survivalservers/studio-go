package server

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
)

// The hosted server's sign-in (the MCP authorization spec, 2026-07-28). With Config.Resource set, it is an OAuth
// protected resource: its metadata (RFC 9728) names the sign-in server, and a request without a token gets 401 with
// a WWW-Authenticate header pointing at that metadata, which is what makes a tool such as Claude Code or Cursor open
// the browser sign-in. A signed-in tool's access token (gso_...) is made for this server only: it is checked at the
// sign-in server and swapped there for the API token made with it (token exchange, RFC 8693), on every request, so
// the token the tool sent is never passed on and a disconnected tool is refused at once. The swap is this server's
// alone: it signs in to the token endpoint as its own client (mcpExchangeClient) with the exchange secret, HTTP Basic,
// from the file GS_MCP_EXCHANGE_SECRET_FILE names (32 or more random bytes as hex, mode 0600; read again when it
// changes, so a rotation needs no restart). An API key (gsk_live_...) in the header still works as before: the API
// checks it.

const (
	mcpTokenPrefix = "gso_"
	apiKeyPrefix   = "gsk_live_"
	// mcpExchangeClient is this server's client name at the sign-in server's token endpoint, for the swap.
	mcpExchangeClient = "survivalservers-mcp"
)

type gate struct {
	cfg      Config
	metaURL  string
	client   *http.Client
	logger   *slog.Logger
	secret   *secretFile
	mu       sync.Mutex
	tokenURL string
}

func newGate(cfg Config, logger *slog.Logger) *gate {
	auth := cfg.AuthServer
	if auth == "" {
		auth = strings.TrimRight(cfg.APIURL, "/")
	}
	cfg.AuthServer = auth
	u, _ := url.Parse(cfg.Resource)
	meta := u.Scheme + "://" + u.Host + "/.well-known/oauth-protected-resource" + strings.TrimRight(u.Path, "/")
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &gate{cfg: cfg, metaURL: meta, client: client, logger: logger, secret: &secretFile{path: cfg.ExchangeSecretFile}}
}

// metadata is the protected resource metadata document.
func (g *gate) metadata(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "use GET", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"resource":                 g.cfg.Resource,
		"authorization_servers":    []string{g.cfg.AuthServer},
		"bearer_methods_supported": []string{"header"},
	})
}

func (g *gate) challenge(w http.ResponseWriter, errCode, why string) {
	v := fmt.Sprintf("Bearer resource_metadata=%q", g.metaURL)
	if errCode != "" {
		v += fmt.Sprintf(", error=%q, error_description=%q", errCode, why)
	}
	w.Header().Set("WWW-Authenticate", v)
	http.Error(w, why, http.StatusUnauthorized)
}

// wrap checks each request's token before the MCP handler sees it.
func (g *gate) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		tok := bearer(r.Header.Get("Authorization"))
		switch {
		case tok == "":
			g.challenge(w, "", "Sign in: this server takes a token from its sign-in, or an API key.")
			return
		case strings.HasPrefix(tok, apiKeyPrefix):
			next.ServeHTTP(w, r) // an API key or agent key: the API checks it on every call
			return
		case !strings.HasPrefix(tok, mcpTokenPrefix):
			g.challenge(w, "invalid_token", "That is not a token from this server's sign-in.")
			return
		}
		api, err := g.swap(r.Context(), tok)
		if errors.Is(err, errBadToken) {
			g.challenge(w, "invalid_token", "The token is expired or was revoked. Sign in again.")
			return
		}
		if err != nil {
			if g.logger != nil {
				g.logger.Warn("checking a sign-in token failed", "err", err)
			}
			http.Error(w, "The sign-in server did not answer; try again in a moment.", http.StatusServiceUnavailable)
			return
		}
		r2 := r.Clone(r.Context())
		r2.Header.Set("Authorization", "Bearer "+api)
		next.ServeHTTP(w, r2)
	})
}

var (
	errBadToken = errors.New("token refused")
	// errExchangeRefused is the sign-in server refusing this server itself (its exchange secret): every tool would be
	// told to sign in again for nothing, so it is answered as the server's own fault.
	errExchangeRefused = errors.New("the sign-in server refused this server's exchange secret (GS_MCP_EXCHANGE_SECRET_FILE)")
)

// swap trades an access token for its API token at the sign-in server, on every request: nothing is kept, so a
// disconnected tool is refused on its very next request (the swap is a lookup, quick and making no rows).
func (g *gate) swap(ctx context.Context, tok string) (string, error) {
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:token-exchange"}, "subject_token": {tok},
		"subject_token_type": {"urn:ietf:params:oauth:token-type:access_token"}}
	secret, err := g.secret.get(time.Now())
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.tokenEndpoint(ctx), strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(mcpExchangeClient, secret)
	resp, err := g.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var out struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	_ = json.Unmarshal(body, &out)
	switch {
	case resp.StatusCode == http.StatusOK && strings.HasPrefix(out.AccessToken, apiKeyPrefix):
		return out.AccessToken, nil
	case out.Error == "invalid_client":
		return "", errExchangeRefused
	case resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusUnauthorized:
		return "", errBadToken
	}
	return "", fmt.Errorf("token exchange answered HTTP %d", resp.StatusCode)
}

// tokenEndpoint reads the sign-in server's metadata once for its token endpoint (default <issuer>/oauth/token).
func (g *gate) tokenEndpoint(ctx context.Context) string {
	g.mu.Lock()
	if g.tokenURL != "" {
		defer g.mu.Unlock()
		return g.tokenURL
	}
	g.mu.Unlock()
	ep := g.cfg.AuthServer + "/oauth/token"
	if req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.cfg.AuthServer+"/.well-known/oauth-authorization-server", nil); err == nil {
		if resp, err := g.client.Do(req); err == nil {
			var md struct {
				Issuer        string `json:"issuer"`
				TokenEndpoint string `json:"token_endpoint"`
			}
			if resp.StatusCode == http.StatusOK && json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&md) == nil &&
				md.Issuer == g.cfg.AuthServer && md.TokenEndpoint != "" {
				ep = md.TokenEndpoint
			}
			resp.Body.Close()
		}
	}
	g.mu.Lock()
	g.tokenURL = ep
	g.mu.Unlock()
	return ep
}

// exchangeRecheck is how often the secret file is looked at again for a change.
const exchangeRecheck = 5 * time.Second

// secretFile is the exchange secret's file: its first value is sent (it holds one; the sign-in server's own file
// holds two during a rotation). It is read again when it changes.
type secretFile struct {
	path    string
	mu      sync.Mutex
	checked time.Time
	stat    os.FileInfo
	value   string
	err     error
}

func (f *secretFile) get(now time.Time) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.checked.IsZero() || now.Sub(f.checked) >= exchangeRecheck {
		f.checked = now
		fi, err := os.Stat(f.path)
		if f.stat == nil || err != nil || !fi.ModTime().Equal(f.stat.ModTime()) || fi.Size() != f.stat.Size() || fi.Mode() != f.stat.Mode() {
			f.stat = fi
			f.value, f.err = readExchangeSecret(f.path)
		}
	}
	return f.value, f.err
}

// readExchangeSecret reads the exchange secret file's first value. Its errors name the file and what is wrong,
// never the value.
func readExchangeSecret(path string) (string, error) {
	if path == "" {
		return "", errors.New("no exchange secret file: set GS_MCP_EXCHANGE_SECRET_FILE")
	}
	fh, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("the exchange secret file %s cannot be read", path)
	}
	defer fh.Close()
	fi, err := fh.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return "", fmt.Errorf("the exchange secret file %s is not a plain file", path)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("the exchange secret file %s can be read by others than its owner (it must be mode 0600)", path)
	}
	raw, err := io.ReadAll(io.LimitReader(fh, 4<<10))
	if err != nil {
		return "", fmt.Errorf("the exchange secret file %s cannot be read", path)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if v := strings.TrimSpace(line); v != "" {
			if b, err := hex.DecodeString(v); err != nil || len(b) < 32 {
				return "", fmt.Errorf("the exchange secret in %s is not 32 or more random bytes as hex", path)
			}
			return v, nil
		}
	}
	return "", fmt.Errorf("the exchange secret file %s is empty", path)
}
