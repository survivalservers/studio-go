package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// fakeAPI is a stand-in for the public API: routes answer with canned JSON and every request is recorded.
type fakeAPI struct {
	t      *testing.T
	srv    *httptest.Server
	mu     sync.Mutex
	routes map[string]func(r *http.Request, body map[string]any) (int, any)
	calls  []call
}

type call struct {
	Method, Path, Query, Auth, Idem string
	Body                            map[string]any
	Raw                             []byte
}

func newFakeAPI(t *testing.T) *fakeAPI {
	f := &fakeAPI{t: t, routes: map[string]func(*http.Request, map[string]any) (int, any){}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) serve(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	f.mu.Lock()
	f.calls = append(f.calls, call{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Auth: r.Header.Get("Authorization"),
		Idem: r.Header.Get("Idempotency-Key"), Body: body, Raw: raw})
	h := f.routes[r.Method+" "+r.URL.Path]
	f.mu.Unlock()
	if h == nil {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"status":404,"code":"not_found","detail":"no such object (fake)","request_id":"req_fake"}`))
		return
	}
	status, out := h(r, body)
	switch v := out.(type) {
	case string:
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(v))
	case redirect:
		w.Header().Set("Location", string(v))
		w.WriteHeader(status)
	default:
		w.Header().Set("Content-Type", "application/json")
		if status >= 400 {
			w.Header().Set("Content-Type", "application/problem+json")
		}
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(out)
	}
}

type redirect string

func (f *fakeAPI) on(route string, h func(r *http.Request, body map[string]any) (int, any)) {
	f.mu.Lock()
	f.routes[route] = h
	f.mu.Unlock()
}

func (f *fakeAPI) json(route string, status int, v any) {
	f.on(route, func(*http.Request, map[string]any) (int, any) { return status, v })
}

// called returns the recorded calls to one route.
func (f *fakeAPI) called(method, path string) []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []call
	for _, c := range f.calls {
		if c.Method == method && c.Path == path {
			out = append(out, c)
		}
	}
	return out
}

// session connects an MCP client to a server made with cfg (its API address set to the fake) in memory.
func (f *fakeAPI) session(t *testing.T, cfg Config) *mcp.ClientSession {
	t.Helper()
	cfg.APIURL = f.srv.URL
	if cfg.Poll == 0 {
		cfg.Poll = 5 * time.Millisecond
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return connect(t, s)
}

func connect(t *testing.T, s *mcp.Server) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	st, ct := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	cs, err := c.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// callTool calls a tool and returns its text and whether it was an error.
func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

func mustOK(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) string {
	t.Helper()
	text, isErr := callTool(t, cs, name, args)
	if isErr {
		t.Fatalf("%s failed: %s", name, text)
	}
	return text
}

func mustFail(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any, want ...string) string {
	t.Helper()
	text, isErr := callTool(t, cs, name, args)
	if !isErr {
		t.Fatalf("%s should have failed; got: %s", name, text)
	}
	for _, w := range want {
		if !strings.Contains(text, w) {
			t.Errorf("%s error should mention %q; got: %s", name, w, text)
		}
	}
	return text
}

func toolNames(t *testing.T, cs *mcp.ClientSession) map[string]*mcp.Tool {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*mcp.Tool{}
	for _, tl := range res.Tools {
		out[tl.Name] = tl
	}
	return out
}

// Objects the fake hands out, shaped like the API file's examples.
var (
	appBrawl = map[string]any{"id": "app_brawl", "slug": "brawl", "name": "Brawl", "os": "windows", "tier": "standard",
		"default_placement": map[string]any{"continents": []any{"north-america"}}, "branches": map[string]any{}}
	appOther = map[string]any{"id": "app_other", "slug": "other", "name": "Other", "os": "linux", "tier": "standard",
		"default_placement": map[string]any{}, "branches": map[string]any{}}
)

func gameServer(id, app, status string) map[string]any {
	return map[string]any{"id": id, "app": app, "version": "1.0.0", "status": status, "region": "us-central", "continent": "north-america",
		"address": map[string]any{"ipv4": "203.0.113.24", "ipv6": nil, "ports": map[string]any{"game": map[string]any{"port": 31042, "protocol": "udp"}}},
		"labels":  map[string]any{}, "players": map[string]any{"count": 0}}
}
