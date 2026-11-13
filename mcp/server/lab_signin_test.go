package server_test

// TestLabSignIn proves the hosted server's sign-in for AI tools on a test deployment, with the official Go MCP SDK as the tool:
// it is given only the server's address, gets the 401, reads the protected resource metadata and our sign-in
// server's metadata, registers, and runs the authorization with PKCE. The member's part (sign-in and Approve) is a
// helper the test runs with the authorization address (GS_LAB_APPROVE: ops/testcell/aiconnect.py approve), which
// approves through the panel's own call as the journey's owner and hands the code back to this test's loopback
// address, as a browser would. Then, through the hosted server:
//
//	view only: game servers are listed; a start is refused (agent_not_allowed)
//	start and stop, a tiny cap and a short time limit: starts go through until the agent's spending reaches the
//	cap, then the next is refused (agent_spending_cap); the game servers stop at the time limit (stop reason ttl)
//	Disconnect: the tool's next call is refused (the SDK goes back to the member's sign-in, who declines this
//	time), and the old token gets 401 from the hosted server
//
// Every call and answer goes to GS_LAB_OUT/signin-<flow>.jsonl; tokens never do. It never runs without the variables:
//
//	GS_LAB_MCP_URL=http://127.0.0.1:8460/mcp GS_LAB_APPROVE=ops/testcell/aiconnect.py GS_LAB_OUT=... GS_LAB_APP=<slug> \
//	  mcp.test -test.run TestLabSignIn -test.v -test.timeout 40m
//
// Optional: GS_LAB_REGION (europe-west), GS_LAB_SERVERS (3) game servers started against the cap, GS_LAB_CAP ("0.01"), GS_LAB_TIME_LIMIT ("6m").

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

// signInRun is one tool connected with one approval.
type signInRun struct {
	t        *testing.T
	flow     string
	endpoint string
	cs       *mcp.ClientSession
	handler  *auth.AuthorizationCodeHandler
	out      *os.File
	// After Disconnect the tool's token is refused and the SDK sends the member back to sign in; the member then
	// declines (aiconnect.py deny), and asked counts how often that happened.
	declined bool
	asked    int
}

func TestLabSignIn(t *testing.T) {
	endpoint, approve, outDir, app := os.Getenv("GS_LAB_MCP_URL"), os.Getenv("GS_LAB_APPROVE"), os.Getenv("GS_LAB_OUT"), os.Getenv("GS_LAB_APP")
	if endpoint == "" || approve == "" || outDir == "" || app == "" {
		t.Skip("GS_LAB_MCP_URL, GS_LAB_APPROVE, GS_LAB_OUT and GS_LAB_APP are not set")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	servers, _ := strconv.Atoi(envOr("GS_LAB_SERVERS", "3"))
	regions := []string{envOr("GS_LAB_REGION", "europe-west")}
	limit := envOr("GS_LAB_TIME_LIMIT", "6m")

	// The tool knows only the address: a request without a token is told where to sign in.
	resp, err := http.Post(endpoint, "application/json", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 || !strings.Contains(resp.Header.Get("WWW-Authenticate"), "resource_metadata=") {
		t.Fatalf("no token: %d %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}

	t.Run("view-only", func(t *testing.T) {
		r := newSignInRun(t, "view-only", endpoint, approve, `{"ends_in_days": 7}`)
		defer r.close()
		r.call("list_game_servers", map[string]any{}, false, "")
		r.call("start_game_server", map[string]any{"app": app, "regions": regions}, true, "was not allowed")
	})

	t.Run("cap-and-time-limit", func(t *testing.T) {
		grant := fmt.Sprintf(`{"permissions": ["start"], "spend_cap": %q, "time_limit": %q, "ends_in_days": 7}`, envOr("GS_LAB_CAP", "0.01"), limit)
		r := newSignInRun(t, "cap", endpoint, approve, grant)
		defer r.close()
		var started []string
		for i := 0; i < servers; i++ {
			text := r.call("start_game_server", map[string]any{"app": app, "regions": regions, "ttl": "1h", "wait_seconds": 300}, false, "")
			if id := firstID(text, "gs_"); id != "" {
				started = append(started, id)
			}
		}
		if len(started) != servers {
			t.Fatalf("started %d of %d", len(started), servers)
		}
		// Running costs money: once the agent's month reaches its cap, the next start is refused until the 1st.
		deadline := time.Now().Add(20 * time.Minute)
		for {
			text, isErr := r.try("start_game_server", map[string]any{"app": app, "regions": regions, "wait_seconds": 300})
			if isErr && strings.Contains(text, "spending cap") {
				break
			}
			if !isErr {
				if id := firstID(text, "gs_"); id != "" {
					started = append(started, id)
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("no cap refusal after 20 minutes: %s", text)
			}
			time.Sleep(30 * time.Second)
		}
		// The time limit: every game server it started stops on its own (stop reason ttl), whatever ttl it asked for.
		r.note(map[string]any{"step": "started", "game_servers": started})
		deadline = time.Now().Add(15 * time.Minute)
		for _, id := range started {
			for {
				text := r.call("wait_for_game_server", map[string]any{"id": id, "wait_seconds": 0}, false, "")
				if strings.Contains(text, "stopped") {
					if !strings.Contains(text, "ttl") {
						t.Errorf("%s stopped, but not at its time limit: %s", id, text)
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("%s did not stop at the time limit: %s", id, text)
				}
				time.Sleep(20 * time.Second)
			}
		}
		// Disconnect: the member's call on the AI agents page; then the tool is refused, and its token gets 401.
		tok, err := r.accessToken()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(approve, "disconnect", "MCP proof client")
		cmd.Stderr = os.Stderr
		if out, err := cmd.Output(); err != nil {
			t.Fatalf("disconnect: %v %s", err, out)
		}
		r.note(map[string]any{"step": "disconnected"})
		r.declined = true
		if text, isErr := r.try("list_game_servers", map[string]any{}); !isErr {
			t.Fatalf("a tool call after Disconnect worked: %s", text)
		}
		if r.asked == 0 {
			t.Fatal("after Disconnect the tool was never sent back to sign in")
		}
		req, _ := http.NewRequest("POST", endpoint, strings.NewReader(`{"jsonrpc":"2.0","id":9,"method":"tools/list"}`))
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		r.note(map[string]any{"step": "after disconnect", "status": resp.StatusCode, "www_authenticate": resp.Header.Get("WWW-Authenticate")})
		if resp.StatusCode != 401 {
			t.Fatalf("the disconnected token got %d", resp.StatusCode)
		}
	})
}

func newSignInRun(t *testing.T, flow, endpoint, approve, grant string) *signInRun {
	f, err := os.OpenFile(filepath.Join(os.Getenv("GS_LAB_OUT"), "signin-"+flow+".jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	r := &signInRun{t: t, flow: flow, endpoint: endpoint, out: f}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	redirect := fmt.Sprintf("http://127.0.0.1:%d/callback", ln.Addr().(*net.TCPAddr).Port)
	codes := make(chan *auth.AuthorizationResult, 1)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		if q.Get("error") != "" {
			http.Error(w, "refused: "+q.Get("error"), http.StatusBadRequest)
			codes <- nil
			return
		}
		_, _ = w.Write([]byte("Connected. You can close this tab."))
		codes <- &auth.AuthorizationResult{Code: q.Get("code"), State: q.Get("state"), Iss: q.Get("iss")}
	}), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	r.handler, err = auth.NewAuthorizationCodeHandler(&auth.AuthorizationCodeHandlerConfig{
		DynamicClientRegistrationConfig: &auth.DynamicClientRegistrationConfig{Metadata: &oauthex.ClientRegistrationMetadata{
			ClientName: "MCP proof client", RedirectURIs: []string{redirect}, GrantTypes: []string{"authorization_code", "refresh_token"},
			ResponseTypes: []string{"code"}, TokenEndpointAuthMethod: "none"}},
		RedirectURL:         redirect,
		RequestRefreshToken: true,
		AuthorizationCodeFetcher: func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
			// The member's part, as the browser does it: sign in, approve, follow the answer back to this address.
			r.note(map[string]any{"step": "authorize", "address": redact(args.URL), "member_declines": r.declined})
			cmd := exec.CommandContext(ctx, approve, "approve", args.URL, grant)
			if r.declined {
				r.asked++
				cmd = exec.CommandContext(ctx, approve, "deny", args.URL)
			}
			cmd.Stderr = os.Stderr
			if out, err := cmd.Output(); err != nil {
				return nil, fmt.Errorf("approve: %v %s", err, out)
			}
			select {
			case res := <-codes:
				if res == nil {
					return nil, errors.New("the member refused")
				}
				return res, nil
			case <-time.After(2 * time.Minute):
				return nil, errors.New("no answer came back to the loopback address")
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	transport := &mcp.StreamableClientTransport{Endpoint: endpoint, OAuthHandler: r.handler, DisableStandaloneSSE: true,
		HTTPClient: &http.Client{Timeout: 16 * time.Minute}}
	c := mcp.NewClient(&mcp.Implementation{Name: "mcp-proof-client", Version: "0"}, nil)
	if r.cs, err = c.Connect(ctx, transport, nil); err != nil {
		t.Fatalf("connect through the sign-in: %v", err)
	}
	res, err := r.cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("tools/list after sign-in: %v", err)
	}
	r.note(map[string]any{"step": "signed in", "tools": len(res.Tools)})
	return r
}

func (r *signInRun) accessToken() (string, error) {
	ts, err := r.handler.TokenSource(context.Background())
	if err != nil || ts == nil {
		return "", fmt.Errorf("token source: %v", err)
	}
	tok, err := ts.Token()
	if err != nil {
		return "", err
	}
	return tok.AccessToken, nil
}

func (r *signInRun) try(tool string, args map[string]any) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	start := time.Now()
	res, err := r.cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		r.note(map[string]any{"step": "call", "tool": tool, "args": args, "transport_error": err.Error()})
		return err.Error(), true
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	r.note(map[string]any{"step": "call", "tool": tool, "args": args, "ms": time.Since(start).Milliseconds(), "is_error": res.IsError, "text": b.String()})
	return b.String(), res.IsError
}

// call calls a tool and wants it to fail (with want in its text) or not.
func (r *signInRun) call(tool string, args map[string]any, wantErr bool, want string) string {
	r.t.Helper()
	text, isErr := r.try(tool, args)
	if isErr != wantErr || (want != "" && !strings.Contains(text, want)) {
		r.t.Fatalf("%s: error %v (want %v): %s", tool, isErr, wantErr, text)
	}
	return text
}

func (r *signInRun) note(v map[string]any) {
	v["at"], v["flow"] = time.Now().UTC().Format(time.RFC3339Nano), r.flow
	b, _ := json.Marshal(v)
	_, _ = r.out.Write(append(b, '\n'))
}

func (r *signInRun) close() {
	if r.cs != nil {
		_ = r.cs.Close()
	}
	_ = r.out.Close()
}

// redact keeps an authorization address readable without its state and PKCE challenge.
func redact(u string) string {
	if i := strings.IndexByte(u, '?'); i >= 0 {
		return u[:i] + "?..."
	}
	return u
}

// firstID finds the first ID with a prefix in a tool's answer.
func firstID(text, prefix string) string {
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Split(bufio.ScanWords)
	for sc.Scan() {
		w := strings.Trim(sc.Text(), `"',.:;()[]{}`)
		if strings.HasPrefix(w, prefix) && len(w) > len(prefix)+10 {
			return w
		}
	}
	return ""
}
