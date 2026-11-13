package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSignIn stands in for the sign-in server: its metadata and the token exchange, with one good access token.
type fakeSignIn struct {
	srv     *httptest.Server
	mu      sync.Mutex
	good    map[string]string // access token -> API token
	secret  string            // the exchange secret it takes
	swaps   int
	refuse  bool
	unavail bool
}

func newFakeSignIn(t *testing.T) *fakeSignIn {
	f := &fakeSignIn{good: map[string]string{}, secret: testExchangeSecret}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server":
			_ = json.NewEncoder(w).Encode(map[string]any{"issuer": f.srv.URL, "token_endpoint": f.srv.URL + "/oauth/token"})
		case "/oauth/token":
			_ = r.ParseForm()
			f.mu.Lock()
			defer f.mu.Unlock()
			f.swaps++
			if id, secret, ok := r.BasicAuth(); !ok || id != mcpExchangeClient || secret != f.secret {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":"invalid_client"}`))
				return
			}
			if f.unavail {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			api, ok := f.good[r.PostForm.Get("subject_token")]
			if r.PostForm.Get("grant_type") != "urn:ietf:params:oauth:grant-type:token-exchange" || !ok || f.refuse {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": api, "token_type": "Bearer", "expires_in": 3600})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// testExchangeSecret is the exchange secret in these tests (32 bytes as hex; no real one).
var testExchangeSecret = strings.Repeat("7c", 32)

// secretFileFor writes a 0600 exchange secret file.
func secretFileFor(t *testing.T, value string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "mcp-exchange-secret")
	if err := os.WriteFile(p, []byte(value+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// The hosted server with a sign-in: no token gets 401 pointing at the metadata, the metadata names the sign-in
// server, a sign-in token is swapped (with this server's exchange secret) and never reaches the API, a revoked one
// gets 401, and API keys still work.
func TestHostedSignIn(t *testing.T) {
	api := newFakeAPI(t)
	api.json("GET /v0/apps", 200, map[string]any{"data": []any{appBrawl}, "next_cursor": nil})
	si := newFakeSignIn(t)
	si.good["gso_kaaaaaaaaaaaa_good"] = "gsk_live_EXAMPLE_not_a_real_key"
	const resource = "https://mcp.example.com/mcp"
	var logs bytes.Buffer
	srv := httptest.NewServer(HTTPHandler(Config{APIURL: api.srv.URL, AuthServer: si.srv.URL, Resource: resource,
		ExchangeSecretFile: secretFileFor(t, testExchangeSecret)}, slog.New(slog.NewTextHandler(&logs, nil))))
	t.Cleanup(srv.Close)

	rpc := func(auth, msg string) (*http.Response, string) {
		req, _ := http.NewRequest("POST", srv.URL+"/mcp", strings.NewReader(msg))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("MCP-Protocol-Version", "2025-06-18")
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		return resp, string(data)
	}
	const initMsg = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"t","version":"1"}}}`
	const listApps = `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_apps","arguments":{}}}`

	resp, _ := rpc("", initMsg)
	if resp.StatusCode != 401 {
		t.Fatalf("no token: %d", resp.StatusCode)
	}
	want := `Bearer resource_metadata="https://mcp.example.com/.well-known/oauth-protected-resource/mcp"`
	if got := resp.Header.Get("WWW-Authenticate"); got != want {
		t.Fatalf("WWW-Authenticate %q, want %q", got, want)
	}
	for _, path := range []string{"/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-protected-resource"} {
		r, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		var md map[string]any
		_ = json.NewDecoder(r.Body).Decode(&md)
		r.Body.Close()
		if md["resource"] != resource || md["authorization_servers"].([]any)[0] != si.srv.URL {
			t.Fatalf("%s: %v", path, md)
		}
	}

	// A sign-in token: swapped, and only the swapped token reaches the API.
	if resp, out := rpc("Bearer gso_kaaaaaaaaaaaa_good", listApps); resp.StatusCode != 200 || !strings.Contains(out, "brawl") {
		t.Fatalf("signed-in call: %d %.300s", resp.StatusCode, out)
	}
	for _, c := range api.called("GET", "/v0/apps") {
		if c.Auth != "Bearer gsk_live_EXAMPLE_not_a_real_key" {
			t.Fatalf("the API saw %q: the tool's token must never be passed on", c.Auth)
		}
	}
	// The sign-in server refusing this server's own secret: the server's fault (503, logged), not the tool's, so no
	// tool is sent to sign in again; and neither the answer nor the log carries the secret.
	si.mu.Lock()
	si.secret = strings.Repeat("7d", 32)
	si.mu.Unlock()
	if resp, out := rpc("Bearer gso_kaaaaaaaaaaaa_good", listApps); resp.StatusCode != 503 || strings.Contains(out, testExchangeSecret) {
		t.Fatalf("a refused exchange secret: %d %s", resp.StatusCode, out)
	}
	if !strings.Contains(logs.String(), "refused this server's exchange secret") || strings.Contains(logs.String(), testExchangeSecret) {
		t.Fatalf("the log: %s", logs.String())
	}
	si.mu.Lock()
	si.secret = testExchangeSecret
	si.mu.Unlock()
	// Revoked (disconnected): the same token is refused on its very next request, 401 with invalid_token.
	si.mu.Lock()
	si.refuse = true
	si.mu.Unlock()
	if resp, _ := rpc("Bearer gso_kaaaaaaaaaaaa_good", listApps); resp.StatusCode != 401 ||
		!strings.Contains(resp.Header.Get("WWW-Authenticate"), `error="invalid_token"`) {
		t.Fatalf("a refused token: %d %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}
	// Something that is neither: refused without asking anyone.
	before := si.swaps
	if resp, _ := rpc("Bearer eyJhbGciOiJIUzI1NiJ9.x.y", listApps); resp.StatusCode != 401 || si.swaps != before {
		t.Fatalf("a foreign token: %d, swaps %d -> %d", resp.StatusCode, before, si.swaps)
	}
	// The sign-in server down: 503, never let through.
	si.mu.Lock()
	si.unavail = true
	si.mu.Unlock()
	if resp, _ := rpc("Bearer gso_kdddddddddddd_other", listApps); resp.StatusCode != 503 {
		t.Fatalf("sign-in server down: %d", resp.StatusCode)
	}
	// An API key (or agent key) still works as before.
	if resp, out := rpc("Bearer gsk_live_EXAMPLE", listApps); resp.StatusCode != 200 || !strings.Contains(out, "brawl") {
		t.Fatalf("API key: %d %.300s", resp.StatusCode, out)
	}
}

// The exchange secret file: only a 0600 file whose first value is 32 or more random bytes as hex; the errors name the
// file, never the value; a changed file is read again; and the hosted server with a sign-in will not start without one.
func TestExchangeSecretFile(t *testing.T) {
	dir := t.TempDir()
	write := func(name, text string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(text), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	short := strings.Repeat("ab", 16)
	for _, c := range []struct{ why, path string }{
		{"no file named", ""},
		{"a missing file", filepath.Join(dir, "missing")},
		{"readable by others", write("open", testExchangeSecret+"\n", 0o644)},
		{"16 bytes", write("short", short+"\n", 0o600)},
		{"empty", write("empty", "\n", 0o600)},
	} {
		v, err := readExchangeSecret(c.path)
		if err == nil || v != "" {
			t.Errorf("%s: taken", c.why)
		} else if strings.Contains(err.Error(), testExchangeSecret) || strings.Contains(err.Error(), short) {
			t.Errorf("%s: the error carries the value: %v", c.why, err)
		}
	}
	p := write("secret", testExchangeSecret+"\n", 0o600)
	f := &secretFile{path: p}
	now := time.Now()
	if v, err := f.get(now); err != nil || v != testExchangeSecret {
		t.Fatalf("get: %v", err)
	}
	next := strings.Repeat("9e", 32)
	write("secret", next+"\n", 0o600)
	if err := os.Chtimes(p, time.Now().Add(time.Minute), time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if v, _ := f.get(now.Add(exchangeRecheck)); v != next {
		t.Error("a rotated file is read again without a restart")
	}

	// Serve refuses to start the sign-in without the secret.
	t.Setenv("GS_API_URL", "https://api.example.com")
	t.Setenv("GS_MCP_RESOURCE", "https://mcp.example.com/mcp")
	t.Setenv("GS_MCP_EXCHANGE_SECRET_FILE", write("open2", testExchangeSecret+"\n", 0o644))
	var stderr bytes.Buffer
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // had it started, it would stop at once and answer nil
	err := Serve(ctx, []string{"-http", "127.0.0.1:0"}, io.Discard, &stderr)
	if err == nil || !strings.Contains(err.Error(), "needs the exchange secret") || strings.Contains(err.Error()+stderr.String(), testExchangeSecret) {
		t.Errorf("Serve without a usable secret file: %v", err)
	}
}
