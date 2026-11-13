package server_test

// TestLab drives the real MCP server ("survivalservers mcp") with a scripted MCP client against a real API (a test deployment), the way
// an editor or an AI tool would: over stdio ("survivalservers mcp" started as a subprocess, the key in its environment) or hosted
// ("survivalservers mcp -http" somewhere, each request bringing the key in its Authorization header). For a Windows app and a
// Linux app it creates the app, a build (the zip upload of that mode, or an image), waits for it, makes a version,
// waits for it to stage, starts a game server, lists it, reads its timeline, log and files, stops it and reads the
// end. Every tool call and answer goes to GS_LAB_OUT/<flow>.jsonl. It never runs without the variables below.
//
// How to run it (build first, then run: it mostly waits):
//
//	go build -C ../../cli -o /tmp/x/survivalservers ./cmd/survivalservers
//	go test -c -o /tmp/x/mcp.test .
//	export HTTP_PROXY=http://127.0.0.1:18888 http_proxy=http://127.0.0.1:18888 NO_PROXY=127.0.0.1,localhost no_proxy=127.0.0.1,localhost
//	GS_LAB_MODE=stdio GS_LAB_CLI=/tmp/x/survivalservers GS_LAB_API_URL=http://127.0.0.1:18080 GS_LAB_KEY_FILE=~/keys/studio \
//	  GS_LAB_ZIP=plain-server-windows.zip GS_LAB_IMAGE=127.0.0.1:5311/x/plain-server@sha256:... GS_LAB_OUT=/tmp/x/out \
//	  /tmp/x/mcp.test -test.run TestLab -test.v -test.timeout 40m
//	GS_LAB_MODE=hosted GS_LAB_MCP_URL=http://127.0.0.1:PORT/mcp ... (the same, without GS_LAB_CLI)
//
// Optional: GS_LAB_REGION (europe-west), GS_LAB_FLOWS (windows,linux), GS_LAB_WIN_TIER (standard),
// GS_LAB_LINUX_TIER (high-frequency): a version stages only on a machine of its tier. GS_LAB_API_URL is also used,
// with the key, to stop a game server the run started if the run fails before its own stop.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type labRun struct {
	t      *testing.T
	mode   string
	flow   string
	cs     *mcp.ClientSession
	out    *os.File
	key    string
	apiURL string
	mu     sync.Mutex
}

func TestLab(t *testing.T) {
	mode := os.Getenv("GS_LAB_MODE")
	keyFile, apiURL, outDir := os.Getenv("GS_LAB_KEY_FILE"), os.Getenv("GS_LAB_API_URL"), os.Getenv("GS_LAB_OUT")
	if mode == "" || keyFile == "" || apiURL == "" || outDir == "" {
		t.Skip("GS_LAB_MODE, GS_LAB_KEY_FILE, GS_LAB_API_URL and GS_LAB_OUT are not set")
	}
	raw, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	key := strings.TrimSpace(string(raw))
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	flows := strings.Split(envOr("GS_LAB_FLOWS", "windows,linux"), ",")
	stamp := time.Now().UTC().Format("0102150405")

	// The tool list and its shape, with no key at all (hosted answers tool lists without one).
	if mode == "hosted" {
		t.Run("nokey", func(t *testing.T) {
			r := newLabRun(t, mode, "nokey", "", apiURL, outDir)
			r.connect()
			names := r.tools()
			for _, want := range []string{"begin_windows_upload", "complete_windows_upload", "create_linux_build", "start_game_server"} {
				if !slices.Contains(names, want) {
					t.Errorf("hosted tool list has no %s: %v", want, names)
				}
			}
			if slices.Contains(names, "upload_windows_build") {
				t.Errorf("hosted tool list offers upload_windows_build, which reads local files")
			}
			text, isErr := r.call("get_account", nil, 30*time.Second)
			if !isErr || !strings.Contains(text, "Authorization: Bearer") || strings.Contains(text, "set GS_API_KEY") {
				t.Errorf("a call without a key: %s", text)
			}
		})
	}

	for _, flow := range flows {
		t.Run(flow, func(t *testing.T) {
			t.Parallel()
			r := newLabRun(t, mode, flow, key, apiURL, outDir)
			r.connect()
			names := r.tools()
			if mode == "stdio" && (!slices.Contains(names, "upload_windows_build") || slices.Contains(names, "begin_windows_upload")) {
				t.Errorf("stdio tool list: %v", names)
			}
			r.path(flow, stamp)
		})
	}
	t.Cleanup(func() {
		// The key must never appear in what was recorded.
		entries, _ := os.ReadDir(outDir)
		for _, e := range entries {
			data, _ := os.ReadFile(filepath.Join(outDir, e.Name()))
			if bytes.Contains(data, []byte(key)) {
				t.Errorf("%s contains the API key", e.Name())
			}
		}
	})
}

func newLabRun(t *testing.T, mode, flow, key, apiURL, outDir string) *labRun {
	f, err := os.OpenFile(filepath.Join(outDir, mode+"-"+flow+".jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return &labRun{t: t, mode: mode, flow: flow, out: f, key: key, apiURL: apiURL}
}

func (r *labRun) note(v map[string]any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v["at"] = time.Now().UTC().Format(time.RFC3339Nano)
	v["mode"], v["flow"] = r.mode, r.flow
	data, _ := json.Marshal(v)
	_, _ = r.out.Write(append(data, '\n'))
}

// connect starts the MCP session: "survivalservers mcp" as a subprocess over stdio, or the hosted server over HTTP.
func (r *labRun) connect() {
	t := r.t
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var transport mcp.Transport
	switch r.mode {
	case "stdio":
		bin := os.Getenv("GS_LAB_CLI")
		if bin == "" {
			t.Fatal("GS_LAB_CLI: the survivalservers program, started as survivalservers mcp")
		}
		cmd := exec.Command(bin, "mcp")
		cmd.Env = append(os.Environ(), "GS_API_URL="+r.apiURL, "GS_API_KEY="+r.key, "GS_MAX_TTL=30m")
		cmd.Stderr = os.Stderr
		transport = &mcp.CommandTransport{Command: cmd}
	case "hosted":
		endpoint := os.Getenv("GS_LAB_MCP_URL")
		if endpoint == "" {
			t.Fatal("GS_LAB_MCP_URL: the hosted server's /mcp address")
		}
		hc := &http.Client{Transport: bearerTransport{key: r.key, next: http.DefaultTransport}, Timeout: 16 * time.Minute}
		transport = &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: hc, DisableStandaloneSSE: true}
	default:
		t.Fatalf("GS_LAB_MODE %q: stdio or hosted", r.mode)
	}
	c := mcp.NewClient(&mcp.Implementation{Name: "survivalservers-mcp-labcheck", Version: "0"}, nil)
	cs, err := c.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("connect (%s): %v", r.mode, err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	r.cs = cs
	init := cs.InitializeResult()
	r.note(map[string]any{"step": "initialize", "server": init.ServerInfo, "protocol": init.ProtocolVersion, "instructions": init.Instructions})
	if init.ServerInfo == nil || init.ServerInfo.Name != "survival-servers" {
		t.Errorf("server info: %+v", init.ServerInfo)
	}
	if r.mode == "hosted" && !strings.Contains(init.Instructions, "hosted server") {
		t.Errorf("hosted instructions do not say it is the hosted server")
	}
}

func (r *labRun) tools() []string {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := r.cs.ListTools(ctx, nil)
	if err != nil {
		r.t.Fatalf("tools/list: %v", err)
	}
	var names []string
	for _, tl := range res.Tools {
		names = append(names, tl.Name)
	}
	r.note(map[string]any{"step": "tools/list", "tools": names})
	return names
}

// call calls a tool and records the call and its answer.
func (r *labRun) call(tool string, args map[string]any, limit time.Duration) (string, bool) {
	r.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	start := time.Now()
	res, err := r.cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	ms := time.Since(start).Milliseconds()
	if err != nil {
		r.note(map[string]any{"step": "call", "tool": tool, "args": args, "ms": ms, "transport_error": err.Error()})
		r.t.Fatalf("%s: %v", tool, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	r.note(map[string]any{"step": "call", "tool": tool, "args": args, "ms": ms, "is_error": res.IsError, "text": b.String()})
	return b.String(), res.IsError
}

func (r *labRun) ok(tool string, args map[string]any, limit time.Duration, wants ...string) string {
	r.t.Helper()
	text, isErr := r.call(tool, args, limit)
	if isErr {
		r.t.Fatalf("%s: %s", tool, text)
	}
	for _, w := range wants {
		if !strings.Contains(text, w) {
			r.t.Fatalf("%s: no %q in %s", tool, w, text)
		}
	}
	return text
}

var (
	buildID = regexp.MustCompile(`Build (bld_[A-Za-z0-9]+)`)
	gsID    = regexp.MustCompile(`Game server (gs_[A-Za-z0-9]+)`)
	addr    = regexp.MustCompile(`Players connect to (\d+\.\d+\.\d+\.\d+):(\d+) \((udp|tcp)\)`)
)

func match(t *testing.T, re *regexp.Regexp, text string) []string {
	t.Helper()
	m := re.FindStringSubmatch(text)
	if m == nil {
		t.Fatalf("no %s in %s", re, text)
	}
	return m
}

// path is the core path for one system: app, build, version, game server, and back.
func (r *labRun) path(flow, stamp string) {
	t := r.t
	region := envOr("GS_LAB_REGION", "europe-west")
	app := fmt.Sprintf("mcp-%s-%s-%s", r.mode, flow[:3], stamp)
	minute := time.Minute

	acct := r.ok("get_account", nil, minute, "Studio ")
	if strings.Contains(acct, "(suspended)") || strings.Contains(acct, "(closed)") {
		t.Fatalf("studio: %s", firstLine(acct))
	}
	r.ok("list_regions", map[string]any{"os": flow}, minute, `"id": "`+region+`"`)

	var build, tier string
	switch flow {
	case "windows":
		tier = envOr("GS_LAB_WIN_TIER", "standard")
		r.ok("create_app", map[string]any{"name": "MCP lab " + r.mode + " Windows " + stamp, "slug": app, "os": "windows", "tier": tier, "regions": []any{region}},
			minute, "App "+app+" made (windows, "+tier+")")
		zipPath := os.Getenv("GS_LAB_ZIP")
		if zipPath == "" {
			t.Fatal("GS_LAB_ZIP: the Windows sample zip")
		}
		if r.mode == "stdio" {
			text := r.ok("upload_windows_build", map[string]any{"app": app, "zip_path": zipPath}, 10*minute, "uploaded")
			build = match(t, buildID, text)[1]
		} else {
			build = r.hostedUpload(app, zipPath)
		}
		r.ok("wait_for_build", map[string]any{"build": build, "wait_seconds": 600}, 11*minute, "Build "+build+" is ready.", "PlainServer.exe")
		text := r.ok("create_version", map[string]any{"app": app, "name": "1.0.0", "build": build, "tier": tier, "cpu": 1, "memory_gb": 1,
			"executable": "PlainServer.exe", "ports": []any{map[string]any{"name": "game", "protocol": "udp"}},
			"ready_check": map[string]any{"type": "local_api"}, "stop_method": "ctrl_break", "stop_shutdown_time": "10s", "restart": "never",
			"regions": []any{region}}, minute, "Version 1.0.0 of "+app+" made; copying to 1 regions.")
		_ = text
	case "linux":
		tier = envOr("GS_LAB_LINUX_TIER", "high-frequency")
		image := os.Getenv("GS_LAB_IMAGE")
		if image == "" {
			t.Fatal("GS_LAB_IMAGE: a Linux image by digest the cell can pull")
		}
		r.ok("create_app", map[string]any{"name": "MCP lab " + r.mode + " Linux " + stamp, "slug": app, "os": "linux", "tier": tier, "continents": []any{"europe"}, "regions": []any{region}},
			minute, "App "+app+" made (linux, "+tier+")")
		text := r.ok("create_linux_build", map[string]any{"app": app, "image": image}, minute, "from "+image)
		build = match(t, buildID, text)[1]
		r.ok("wait_for_build", map[string]any{"build": build, "wait_seconds": 600}, 11*minute, "Build "+build+" is ready.")
		r.ok("create_version", map[string]any{"app": app, "name": "1.0.0", "build": build, "tier": tier, "cpu": 1, "memory_gb": 1,
			"ports":       []any{map[string]any{"name": "game", "protocol": "udp", "container_port": 7777}},
			"ready_check": map[string]any{"type": "local_api"}, "stop_method": "sigterm", "stop_shutdown_time": "10s", "regions": []any{region}},
			minute, "Version 1.0.0 of "+app+" made; copying to 1 regions.")
	}
	r.ok("wait_for_version", map[string]any{"app": app, "version": "1.0.0", "wait_seconds": 900}, 16*minute, "Version 1.0.0: ready in 1 of 1 regions.")
	r.ok("list_versions", map[string]any{"app": app}, minute, "1 versions of "+app+".", `"name": "1.0.0"`, `"build": "`+build+`"`)
	r.ok("list_regions", map[string]any{"os": flow, "app": app, "version": "1.0.0"}, minute, `"version_ready": true`)

	// A game server: started, found, read while it runs, stopped, read after it ended.
	text := r.ok("start_game_server", map[string]any{"app": app, "version": "1.0.0", "regions": []any{region}, "ttl": "15m",
		"labels": map[string]any{"lab_run": stamp}, "wait_seconds": 300}, 6*minute)
	id := match(t, gsID, text)[1]
	t.Cleanup(func() { r.stopDirect(id) })
	if !strings.Contains(text, "is ready in "+region+".") {
		t.Fatalf("start_game_server did not end ready: %s", firstLine(text))
	}
	a := match(t, addr, text)
	r.note(map[string]any{"step": "check", "what": "address", "ip": a[1], "port": a[2], "protocol": a[3]})
	if !strings.Contains(text, `"started_by": "mcp"`) || !strings.Contains(text, `"lab_run": "`+stamp+`"`) {
		t.Errorf("labels missing from the started game server: %s", text)
	}
	if !strings.Contains(text, "It stops on its own at ") {
		t.Errorf("no ttl in the answer: %s", firstLine(text))
	}
	r.ok("wait_for_game_server", map[string]any{"id": id, "wait_seconds": 0}, minute, "Game server "+id+" is ready in "+region)
	r.ok("list_game_servers", map[string]any{"app": app, "started_here": true}, minute, "1 game servers (starting, running).", `"id": "`+id+`"`, `"status": "running"`, a[1]+":"+a[2])
	r.ok("list_game_servers", map[string]any{"labels": []any{"lab_run:" + stamp}, "app": app}, minute, `"id": "`+id+`"`)
	diag := r.ok("diagnose_game_server", map[string]any{"id": id}, 2*minute, "Timeline. ", "Last 80 console lines. ", "Server ready for players")
	for _, e := range []string{"created", "launched", "ready"} {
		if !timeline(diag, e) {
			t.Errorf("the timeline of a running game server has no %s: %s", e, diag)
		}
	}
	if strings.Contains(diag, "not readable") {
		t.Errorf("diagnose could not read part of a running game server: %s", diag)
	}
	r.ok("read_game_server_log", map[string]any{"id": id, "tail": 50}, minute, "Server ready for players", "players connect to "+a[1]+":"+a[2])
	r.ok("list_game_server_files", map[string]any{"id": id}, minute, "(live)")

	r.ok("stop_game_server", map[string]any{"id": id, "shutdown_time": "10s", "reason": "MCP lab check " + stamp}, minute, "Game server "+id+" is ")
	end := r.ok("wait_for_game_server", map[string]any{"id": id, "wait_seconds": 180}, 4*minute)
	if !strings.Contains(end, "Game server "+id+" has stopped") {
		// stopping is not settled for GameServerSettled; one more wait.
		end = r.ok("wait_for_game_server", map[string]any{"id": id, "wait_seconds": 180}, 4*minute, "Game server "+id+" has stopped")
	}
	if strings.Contains(firstLine(end), "crashed") || !strings.Contains(end, `"crashed": false`) {
		t.Errorf("a stopped game server reads as crashed: %s", firstLine(end))
	}
	// Straight after the exit, as an agent reading why a server ended would: the logs and files the machine keeps
	// arrive a few seconds after the exit (the timeline's artifacts_collected).
	diag = r.ok("diagnose_game_server", map[string]any{"id": id}, 2*minute, "Timeline. ", "has stopped")
	for _, e := range []string{"stop_requested", "exited"} {
		if !timeline(diag, e) {
			t.Errorf("the timeline after the stop has no %s: %s", e, diag)
		}
	}
	if !strings.Contains(diag, "Last 80 console lines. ") || !strings.Contains(diag, "Server ready for players") {
		t.Errorf("diagnose straight after the stop does not show the console: %s", diag)
	}
	r.ok("read_game_server_log", map[string]any{"id": id, "tail": 50}, minute, "Server ready for players")
	files := r.ok("list_game_server_files", map[string]any{"id": id}, minute, "(kept after exit)")
	if strings.Contains(files, "match-result.json") {
		r.ok("read_game_server_file", map[string]any{"id": id, "path": "match-result.json"}, minute, "match-result.json")
	}
	r.ok("list_game_servers", map[string]any{"app": app, "started_here": true, "status": []any{"stopped"}}, minute, `"id": "`+id+`"`, `"status": "stopped"`)
	r.ok("list_game_servers", map[string]any{"app": app, "started_here": true}, minute, "0 game servers (starting, running).")
}

// timeline reports whether diagnose_game_server's timeline has an entry of this type.
func timeline(diag, typ string) bool {
	return regexp.MustCompile(`(?m)^- \S+ ` + typ + `( |$)`).MatchString(diag)
}

// hostedUpload is the hosted server's zip upload: begin, the script it returns run here, complete.
func (r *labRun) hostedUpload(app, zipPath string) string {
	t := r.t
	data, err := os.ReadFile(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	text := r.ok("begin_windows_upload", map[string]any{"app": app, "filename": filepath.Base(zipPath), "bytes": len(data),
		"sha256": hex.EncodeToString(sum[:]), "zip_path": zipPath}, time.Minute, "is waiting for its upload", "```bash\n")
	build := match(t, buildID, text)[1]
	script := text[strings.Index(text, "```bash\n")+8 : strings.LastIndex(text, "```")]
	cmd := exec.Command("bash", "-c", script)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	start := time.Now()
	out, err := cmd.Output()
	r.note(map[string]any{"step": "upload_script", "ms": time.Since(start).Milliseconds(), "stdout": string(out), "stderr": stderr.String(), "error": fmt.Sprint(err)})
	if err != nil {
		t.Fatalf("upload script: %v: %s", err, stderr.String())
	}
	var parts []any
	if err := json.Unmarshal(out, &parts); err != nil || len(parts) == 0 {
		t.Fatalf("upload script printed %q: %v", out, err)
	}
	r.ok("complete_windows_upload", map[string]any{"build": build, "parts": parts}, time.Minute, "Build "+build+" uploaded")
	return build
}

// stopDirect stops a game server the run started straight through the API, when the run failed before its own stop.
func (r *labRun) stopDirect(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, r.apiURL+"/v0/game-servers/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+r.key)
	resp, err := http.DefaultClient.Do(req)
	if err == nil {
		var gs map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&gs)
		resp.Body.Close()
		if st, _ := gs["status"].(string); st == "stopped" || st == "failed" {
			return
		}
	}
	req, _ = http.NewRequestWithContext(ctx, http.MethodPost, r.apiURL+"/v0/game-servers/"+id+"/stop", strings.NewReader(`{"reason":"MCP lab check cleanup"}`))
	req.Header.Set("Authorization", "Bearer "+r.key)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", fmt.Sprintf("lab-cleanup-%s-%d", id, time.Now().UnixNano()))
	resp, err = http.DefaultClient.Do(req)
	status := 0
	if err == nil {
		status = resp.StatusCode
		resp.Body.Close()
	}
	r.note(map[string]any{"step": "cleanup_stop", "id": id, "status": status, "error": fmt.Sprint(err)})
}

type bearerTransport struct {
	key  string
	next http.RoundTripper
}

func (b bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if b.key != "" {
		req = req.Clone(req.Context())
		req.Header.Set("Authorization", "Bearer "+b.key)
	}
	return b.next.RoundTrip(req)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
