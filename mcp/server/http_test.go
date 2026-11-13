package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The hosted server runs on 127.0.0.1 behind our web server, which passes the visitor's host name on. Its own
// public name must get through; any other name on the loopback listener is refused, as the library's guard did
// (every request at the public address once got 403).
func TestHostedServerTakesItsPublicNameOnly(t *testing.T) {
	f := newFakeAPI(t)
	post := func(srv *httptest.Server, host string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest("POST", srv.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`))
		req.Host = host
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("MCP-Protocol-Version", "2025-06-18")
		req.Header.Set("Authorization", "Bearer gsk_live_EXAMPLE_not_a_real_key")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(data)
	}
	signin := httptest.NewServer(HTTPHandler(Config{APIURL: f.srv.URL, Resource: "https://mcp.example.com/mcp"}, nil))
	t.Cleanup(signin.Close)
	plain := httptest.NewServer(HTTPHandler(Config{APIURL: f.srv.URL}, nil))
	t.Cleanup(plain.Close)
	loop := strings.TrimPrefix(signin.URL, "http://")

	for _, c := range []struct {
		srv  *httptest.Server
		host string
		ok   bool
	}{
		{signin, "mcp.example.com", true},
		{signin, "MCP.Example.com:443", true},
		{signin, loop, true},
		{signin, "localhost", true},
		{signin, "[::1]:8080", true},
		{signin, "rebind.example.net", false},
		{signin, "mcp.example.com.rebind.example.net", false},
		{plain, strings.TrimPrefix(plain.URL, "http://"), true},
		{plain, "mcp.example.com", false},
	} {
		code, body := post(c.srv, c.host)
		if c.ok && (code != 200 || !strings.Contains(body, "list_apps")) {
			t.Errorf("Host %q: %d %.200s", c.host, code, body)
		}
		if !c.ok && (code != 403 || !strings.Contains(body, "invalid Host header")) {
			t.Errorf("Host %q must be refused: %d %.200s", c.host, code, body)
		}
	}
	if code, body := post(plain, "mcp.example.com"); !strings.Contains(body, "GS_MCP_RESOURCE") {
		t.Errorf("the refusal without a public name should say where it comes from: %d %s", code, body)
	}
}

// A tool signed in through the hosted server keeps the time limit its member picked: the API holds it there, so the
// server's own GS_MAX_TTL must not refuse a longer run first (8 hours approved, 2 given).
// A key sent as it is still meets GS_MAX_TTL.
func TestHostedSignedInToolKeepsItsOwnTimeLimit(t *testing.T) {
	api := newFakeAPI(t)
	api.json("GET /v0/apps/brawl", 200, appBrawl)
	api.on("POST /v0/game-servers", func(*http.Request, map[string]any) (int, any) {
		return 201, gameServer("gs_1", "app_brawl", "starting")
	})
	si := newFakeSignIn(t)
	si.good["gso_kaaaaaaaaaaaa_good"] = "gsk_live_EXAMPLE_not_a_real_key"
	srv := httptest.NewServer(HTTPHandler(Config{APIURL: api.srv.URL, AuthServer: si.srv.URL,
		Resource: "https://mcp.example.com/mcp", MaxTTL: 2 * time.Hour, ExchangeSecretFile: secretFileFor(t, testExchangeSecret)}, nil))
	t.Cleanup(srv.Close)
	start := func(auth string) string {
		t.Helper()
		req, _ := http.NewRequest("POST", srv.URL+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"start_game_server","arguments":{"app":"brawl","version":"1.0.0","ttl":"8h","wait_seconds":0}}}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("MCP-Protocol-Version", "2025-06-18")
		req.Header.Set("Authorization", auth)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(resp.Body)
		return string(data)
	}
	if out := start("Bearer gso_kaaaaaaaaaaaa_good"); strings.Contains(out, "longer than this server allows") {
		t.Fatalf("a signed-in tool was held to GS_MAX_TTL: %.300s", out)
	}
	calls := api.called("POST", "/v0/game-servers")
	if len(calls) != 1 || calls[0].Body["ttl"] != "8h" || calls[0].Auth != "Bearer gsk_live_EXAMPLE_not_a_real_key" {
		t.Fatalf("the signed-in start should reach the API with its 8h: %+v", calls)
	}
	if out := start("Bearer gsk_live_EXAMPLE"); !strings.Contains(out, "longer than this server allows") {
		t.Errorf("a key sent as it is must still meet GS_MAX_TTL: %.300s", out)
	}
	if n := len(api.called("POST", "/v0/game-servers")); n != 1 {
		t.Errorf("the refused start reached the API: %d starts", n)
	}
}
