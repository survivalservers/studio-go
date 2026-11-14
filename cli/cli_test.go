package cli

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAPI answers routes with canned JSON and records every request.
type fakeAPI struct {
	srv    *httptest.Server
	mu     sync.Mutex
	routes map[string]func(r *http.Request, body map[string]any) (int, any)
	calls  []call
}

type call struct {
	Method, Path, Auth, Idem string
	Query                    url.Values
	Body                     map[string]any
	ContentType              string
	Raw                      []byte
}

type redirect string

func newFake(t *testing.T) *fakeAPI {
	f := &fakeAPI{routes: map[string]func(*http.Request, map[string]any) (int, any){}}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		f.mu.Lock()
		f.calls = append(f.calls, call{r.Method, r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("Idempotency-Key"), r.URL.Query(), body,
			r.Header.Get("Content-Type"), raw})
		h := f.routes[r.Method+" "+r.URL.Path]
		f.mu.Unlock()
		if h == nil {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"status":404,"code":"not_found","detail":"no such object (fake)"}`))
			return
		}
		status, out := h(r, body)
		switch v := out.(type) {
		case string:
			w.WriteHeader(status)
			_, _ = w.Write([]byte(v))
		case redirect:
			w.Header().Set("Location", string(v))
			w.WriteHeader(status)
		default:
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(v)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) on(route string, h func(*http.Request, map[string]any) (int, any)) {
	f.mu.Lock()
	f.routes[route] = h
	f.mu.Unlock()
}

func (f *fakeAPI) json(route string, status int, v any) {
	f.on(route, func(*http.Request, map[string]any) (int, any) { return status, v })
}

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

type result struct {
	code           int
	stdout, stderr string
}

// run runs survivalservers against the fake with a key in the environment.
func (f *fakeAPI) run(t *testing.T, args ...string) result {
	t.Helper()
	return f.runEnv(t, map[string]string{"GS_API_KEY": "gsk_live_EXAMPLE_not_a_real_key", "GS_API_URL": f.srv.URL}, args...)
}

func (f *fakeAPI) runEnv(t *testing.T, vars map[string]string, args ...string) result {
	t.Helper()
	var out, errb bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	code := Run(ctx, args, Env{Stdout: &out, Stderr: &errb, Getenv: func(k string) string { return vars[k] }, Poll: 5 * time.Millisecond, Stdin: strings.NewReader("")})
	return result{code, out.String(), errb.String()}
}

func (r result) must(t *testing.T, code int, want ...string) {
	t.Helper()
	if r.code != code {
		t.Fatalf("exit %d, want %d\nstdout: %s\nstderr: %s", r.code, code, r.stdout, r.stderr)
	}
	for _, w := range want {
		if !strings.Contains(r.stdout+r.stderr, w) {
			t.Errorf("output lacks %q\nstdout: %s\nstderr: %s", w, r.stdout, r.stderr)
		}
	}
}

var appWin = map[string]any{"id": "app_w", "slug": "brawl", "name": "Brawl", "os": "windows", "tier": "standard"}

func gs(id, status string) map[string]any {
	return map[string]any{"id": id, "status": status, "region": "us-central", "continent": "north-america", "version": "1.0.0", "os": "windows", "start_mode": "cold",
		"address": map[string]any{"ipv4": "203.0.113.24", "ports": map[string]any{"game": map[string]any{"port": 31042, "protocol": "udp"}}},
		"players": map[string]any{"count": 0}}
}

// The version a copy built from source prints (go install) is the one its release carries: the newest version in
// CHANGELOG.md. A release adds its section and moves Version in the same change.
func TestVersionIsTheChangelogsNewest(t *testing.T) {
	b, err := os.ReadFile("CHANGELOG.md")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^## (\d+\.\d+\.\d+)\b`).FindSubmatch(b)
	if m == nil {
		t.Fatal(`CHANGELOG.md has no "## X.Y.Z" heading`)
	}
	if string(m[1]) != Version {
		t.Errorf("Version is %q, but CHANGELOG.md's newest version is %q: move them together", Version, m[1])
	}
}

// release.sh refuses to stamp a release version that is not CHANGELOG.md's newest, before it builds anything. A
// version with a suffix (a check build such as 0.0.0-check) is not a release and is built as asked.
func TestReleaseRefusesAnotherVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("release.sh is a bash script; release builds run on Linux")
	}
	_, had := os.Stat("dist")
	out, err := exec.Command("bash", "release.sh", "9.9.9").CombinedOutput()
	if err == nil || !strings.Contains(string(out), "CHANGELOG.md's newest version is "+Version) {
		t.Fatalf("release.sh 9.9.9: %v\n%s", err, out)
	}
	if _, err := os.Stat("dist"); had != nil && err == nil {
		t.Error("release.sh made dist/ before it refused")
	}
}

func TestCommandLine(t *testing.T) {
	f := newFake(t)
	f.run(t).must(t, 0, "survivalservers builds upload")
	f.run(t, "version").must(t, 0, Version)
	f.run(t, "nope").must(t, 2, `no command "nope"`)
	f.run(t, "apps", "explode").must(t, 2, "one of banner, color, create, get, icon, list")
	f.run(t, "apps", "create", "x").must(t, 2, "--os must be windows or linux", "usage: survivalservers apps create")
	f.run(t, "apps", "list", "--key", "gsk_live_EXAMPLE").must(t, 2, "API key was passed on the command line")
	f.runEnv(t, map[string]string{"GS_API_URL": f.srv.URL}, "apps", "list").must(t, 1, "no API key", "GS_API_KEY")
	if len(f.calls) != 0 {
		t.Error("nothing may be sent without a key or with a bad command line")
	}
	// A key file works, and flags may come before or after the arguments.
	key := filepath.Join(t.TempDir(), "key")
	os.WriteFile(key, []byte("gsk_live_EXAMPLE\n"), 0o600)
	f.json("GET /v0/apps/brawl", 200, appWin)
	f.runEnv(t, map[string]string{"GS_API_URL": f.srv.URL}, "apps", "get", "--key-file", key, "brawl").must(t, 0, "brawl (app_w)")
	if c := f.called("GET", "/v0/apps/brawl"); len(c) != 1 || c[0].Auth != "Bearer gsk_live_EXAMPLE" {
		t.Errorf("key file: %+v", c)
	}
}

func TestAppsAndJSON(t *testing.T) {
	f := newFake(t)
	f.on("POST /v0/apps", func(_ *http.Request, b map[string]any) (int, any) {
		return 201, map[string]any{"id": "app_n", "slug": b["slug"], "os": b["os"], "tier": "standard"}
	})
	f.run(t, "apps", "create", "brawl", "--os", "linux", "--continent", "europe", "--continent", "north-america", "--region", "europe-west", "--idempotency-key", "ci-42").must(t, 0, "App brawl made")
	c := f.called("POST", "/v0/apps")[0]
	dp := c.Body["default_placement"].(map[string]any)
	if c.Idem != "ci-42" || c.Body["name"] != "brawl" || len(dp["continents"].([]any)) != 2 || dp["regions"].([]any)[0] != "europe-west" || dp["cities"] != nil {
		t.Errorf("create: %+v", c)
	}
	f.json("GET /v0/apps", 200, map[string]any{"data": []any{appWin}, "next_cursor": nil})
	r := f.run(t, "apps", "list", "--json")
	var apps []map[string]any
	if r.code != 0 || json.Unmarshal([]byte(r.stdout), &apps) != nil || apps[0]["slug"] != "brawl" {
		t.Errorf("--json prints the API's JSON: %q", r.stdout)
	}
}

func TestUploadAndDeploy(t *testing.T) {
	f := newFake(t)
	zip := bytes.Repeat([]byte("PK0123456789"), 300) // 3600 bytes, 4 parts of 1000
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "BrawlServer.zip")
	os.WriteFile(zipPath, zip, 0o600)
	settings := filepath.Join(dir, "version-windows.json")
	os.WriteFile(settings, []byte(`{"name": "1.0.0", "build": "BUILD_ID", "size": {"cpu": 1, "memory_gb": 1},
		"launch": {"executable": "BrawlServer.exe", "ports": [{"name": "game", "protocol": "udp"}], "ready_check": {"type": "local_api"},
		"stop": {"method": "ctrl_break", "shutdown_time": "10s"}}, "regions": ["*"]}`), 0o600)

	var mu sync.Mutex
	got := map[int]int{}
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("part"))
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		got[n] = len(data)
		mu.Unlock()
		if r.Header.Get("Authorization") != "" {
			t.Error("the key never goes to storage")
		}
		w.Header().Set("ETag", fmt.Sprintf(`"e%d"`, n))
	}))
	t.Cleanup(storage.Close)
	f.json("GET /v0/apps/brawl", 200, appWin)
	f.on("POST /v0/apps/brawl/builds", func(_ *http.Request, b map[string]any) (int, any) {
		var parts []any
		for n := 1; n <= 4; n++ {
			parts = append(parts, map[string]any{"number": n, "url": fmt.Sprintf("%s/u?part=%d", storage.URL, n)})
		}
		return 201, map[string]any{"id": "bld_1", "app": "app_w", "status": "awaiting_upload", "upload": map[string]any{"part_bytes": 1000, "parts": parts, "expires_at": "x"}}
	})
	f.json("POST /v0/builds/bld_1/complete", 202, map[string]any{"id": "bld_1", "app": "app_w", "status": "processing"})
	var reads int
	f.on("GET /v0/builds/bld_1", func(*http.Request, map[string]any) (int, any) {
		mu.Lock()
		defer mu.Unlock()
		reads++
		st := "processing"
		if reads > 2 {
			st = "ready"
		}
		return 200, map[string]any{"id": "bld_1", "app": "app_w", "os": "windows", "status": st, "executables": []any{"BrawlServer.exe"}}
	})
	f.on("POST /v0/apps/brawl/versions", func(_ *http.Request, b map[string]any) (int, any) {
		return 201, map[string]any{"name": b["name"], "build": b["build"], "regions": []any{map[string]any{"region": "us-central", "status": "copying"}}}
	})
	f.json("GET /v0/apps/brawl/versions/1.4.3", 200, map[string]any{"name": "1.4.3", "build": "bld_1",
		"regions": []any{map[string]any{"region": "us-central", "status": "ready"}, map[string]any{"region": "europe-central", "status": "copying"}}})
	f.json("PUT /v0/apps/brawl/branches/live", 200, map[string]any{"live": "1.4.3"})

	r := f.runEnvWithStorage(t, storage.Client(), "deploy", "brawl", "--zip", zipPath, "--settings", settings, "--name", "1.4.3", "--branch", "live", "--idempotency-key", "run-7")
	r.must(t, 0, "Deployed brawl 1.4.3 (build bld_1): ready in 1 of 2 regions; branch live points at it.", "part 4 of 4")
	if got[1] != 1000 || got[4] != 600 {
		t.Errorf("parts sent with their exact lengths: %v", got)
	}
	v := f.called("POST", "/v0/apps/brawl/versions")[0]
	if v.Body["build"] != "bld_1" || v.Body["name"] != "1.4.3" || v.Body["regions"].([]any)[0] != "*" {
		t.Errorf("the version gets the new build and name, and keeps the file's regions: %v", v.Body)
	}
	keys := []string{f.called("POST", "/v0/apps/brawl/builds")[0].Idem, f.called("POST", "/v0/builds/bld_1/complete")[0].Idem, v.Idem}
	if keys[0] != "run-7" || keys[2] != "run-7-2" {
		t.Errorf("one key per write, the same on every re-run: %v", keys)
	}
	if strings.Count(r.stdout, "\n") != 1 {
		t.Errorf("deploy prints one summary line: %q", r.stdout)
	}

	// A version the checks refuse is never sent.
	before := len(f.called("POST", "/v0/apps/brawl/versions"))
	f.run(t, "versions", "create", "brawl", settings, "--build", "bld_1", "--name", "2.0.0").must(t, 0)
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte(`{"name": "x", "build": "bld_1", "size": {"cpu": 0.3, "memory_gb": 1}, "launch": {"executable": "Other.exe", "ports": [], "stop": {"method": "sigterm"}}}`), 0o600)
	f.run(t, "versions", "create", "brawl", bad).must(t, 1, "0.25 vCPU steps", `"Other.exe" is not in the zip`, "add at least one port", `stop method "sigterm" is for Linux`)
	f.run(t, "versions", "create", "brawl", settings, "--build", "bld_1", "--dry-run").must(t, 0, "pass the checks", `"executable": "BrawlServer.exe"`)
	if n := len(f.called("POST", "/v0/apps/brawl/versions")); n != before+1 {
		t.Errorf("only the good version was sent: %d", n-before)
	}

	// A version the machines removed is not waited for: the wait ends at once and says how to get it back.
	f.json("GET /v0/apps/brawl/versions/0.9.0", 200, map[string]any{"name": "0.9.0", "build": "bld_1",
		"regions": []any{map[string]any{"region": "us-central", "status": "removed"}}})
	f.run(t, "versions", "get", "brawl", "0.9.0", "--wait", "--timeout", "5s").
		must(t, 1, "us-central removed", "the machines removed it (put it on a branch to have it copied again)")
}

func (f *fakeAPI) runEnvWithStorage(t *testing.T, storage *http.Client, args ...string) result {
	t.Helper()
	var out, errb bytes.Buffer
	code := Run(context.Background(), args, Env{Stdout: &out, Stderr: &errb, Getenv: func(k string) string {
		return map[string]string{"GS_API_KEY": "gsk_live_EXAMPLE_not_a_real_key", "GS_API_URL": f.srv.URL}[k]
	}, Poll: 5 * time.Millisecond, Storage: storage})
	return result{code, out.String(), errb.String()}
}

func TestServers(t *testing.T) {
	f := newFake(t)
	var reads int
	var mu sync.Mutex
	f.json("POST /v0/game-servers", 201, gs("gs_1", "starting"))
	f.on("GET /v0/game-servers/gs_1", func(*http.Request, map[string]any) (int, any) {
		mu.Lock()
		defer mu.Unlock()
		reads++
		if reads < 3 {
			return 200, gs("gs_1", "starting")
		}
		return 200, gs("gs_1", "running")
	})
	f.run(t, "game-servers", "start", "--wait", "brawl", "--branch", "live", "--continent", "europe", "--region", "europe-west", "--region", "uk", "--env", "MODE=ranked", "--label", "match_id=88", "--ttl", "1h").
		must(t, 0, "gs_1: running in us-central", "Address: 203.0.113.24:31042")
	b := f.called("POST", "/v0/game-servers")[0].Body
	if b["branch"] != "live" || b["ttl"] != "1h" || b["env"].(map[string]any)["MODE"] != "ranked" || b["labels"].(map[string]any)["match_id"] != "88" {
		t.Errorf("start body: %v", b)
	}
	if pl := b["placement"].(map[string]any); jsonText(pl) != `{"continents":["europe"],"regions":["europe-west","uk"]}` {
		t.Errorf("placement: %v", pl)
	}
	f.run(t, "game-servers", "start", "brawl", "--env", "NOEQUALS").must(t, 2, "is not NAME=value")

	failed := gs("gs_2", "failed")
	failed["exit"] = map[string]any{"reason": "boot_timeout", "code": nil, "crashed": false}
	f.json("POST /v0/game-servers/gs_2/stop", 409, map[string]any{"status": 409, "code": "already_stopped", "detail": "It has already ended."})
	f.run(t, "game-servers", "stop", "gs_2").must(t, 1, "already_stopped", "Next: The game server has already ended")

	f.json("GET /v0/game-servers", 200, map[string]any{"data": []any{gs("gs_1", "running")}, "next_cursor": nil})
	f.run(t, "game-servers", "list", "--app", "brawl", "--region", "us-central", "--label", "queue:ranked", "--label", "mode:duo").must(t, 0, "gs_1", "REGION", "us-central", "203.0.113.24:31042")
	q := f.called("GET", "/v0/game-servers")[0].Query
	if q.Get("status") != "starting,running" || q.Get("region") != "us-central" || len(q["label"]) != 2 {
		t.Errorf("list query: %v", q)
	}

	// A wait that runs out exits 3.
	f.json("GET /v0/game-servers/gs_slow", 200, gs("gs_slow", "starting"))
	f.run(t, "game-servers", "get", "gs_slow", "--wait", "--timeout", "50ms").must(t, 3, "game server gs_slow is still starting after 50ms", "run the same command to keep waiting")
}

// A wait that runs out after a start or a create names the command that waits on, never "run the same command",
// which would start or make a second one, and prints what it made, as JSON with --json (two
// runs once made gs_1 and gs_2, and --json printed nothing).
func TestAWaitThatRunsOutAfterAWriteNamesItsGetCommand(t *testing.T) {
	f := newFake(t)
	f.json("POST /v0/game-servers", 201, gs("gs_slow", "starting"))
	f.json("GET /v0/game-servers/gs_slow", 200, gs("gs_slow", "starting"))
	r := f.run(t, "game-servers", "start", "brawl", "--branch", "live", "--wait", "--timeout", "50ms")
	r.must(t, 3, "game server gs_slow is still starting after 50ms", "to keep waiting, run survivalservers game-servers get gs_slow --wait")
	if strings.Contains(r.stderr, "same command") || !strings.Contains(r.stdout, "Game server gs_slow: starting") {
		t.Errorf("start: stdout %q stderr %q", r.stdout, r.stderr)
	}
	r = f.run(t, "game-servers", "start", "brawl", "--branch", "live", "--wait", "--timeout", "50ms", "--json")
	var printed map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &printed); err != nil || printed["id"] != "gs_slow" || r.code != 3 {
		t.Errorf("start --json: exit %d, stdout %q (%v)", r.code, r.stdout, err)
	}

	slow := map[string]any{"id": "ps_1", "name": "eu-1", "status": "starting", "region": "europe-west", "version": "1.0.0"}
	f.json("POST /v0/persistent-servers", 201, slow)
	f.json("POST /v0/persistent-servers/ps_1/restart", 202, slow)
	f.json("GET /v0/persistent-servers/ps_1", 200, slow)
	settings := filepath.Join(t.TempDir(), "ps.json")
	if err := os.WriteFile(settings, []byte(`{"app":"brawl","name":"eu-1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	f.run(t, "persistent-servers", "create", settings, "--wait", "--timeout", "50ms").
		must(t, 3, "persistent server ps_1 is still starting after 50ms", "run survivalservers persistent-servers get ps_1 --wait", "Persistent server eu-1 (ps_1)")
	f.run(t, "persistent-servers", "restart", "ps_1", "--wait", "--timeout", "50ms").
		must(t, 3, "run survivalservers persistent-servers get ps_1 --wait")
	f.run(t, "persistent-servers", "get", "ps_1", "--wait", "--timeout", "50ms").must(t, 3, "run the same command to keep waiting")

	f.json("POST /v0/apps/brawl/builds", 201, map[string]any{"id": "bld_1", "status": "processing", "os": "linux"})
	f.json("GET /v0/builds/bld_1", 200, map[string]any{"id": "bld_1", "status": "processing", "os": "linux"})
	f.run(t, "builds", "image", "brawl", "registry.example.com/brawl:1.0.0", "--wait", "--timeout", "50ms").
		must(t, 3, "build bld_1 is still processing after 50ms", "run survivalservers builds get bld_1 --wait")

	f.json("POST /v0/bulk-actions/commands", 202, map[string]any{"bulk_action": map[string]any{"id": "bulk_1", "status": "running"}})
	f.json("GET /v0/bulk-actions/bulk_1", 200, map[string]any{"id": "bulk_1", "status": "running", "kind": "command", "counts": map[string]any{"total": 2}})
	r = f.run(t, "bulk-actions", "command", "--app", "brawl", "--wait", "--timeout", "50ms", "save")
	if r.code != 3 || !strings.Contains(r.stderr, "run survivalservers bulk-actions watch bulk_1") || strings.Contains(r.stderr, "same command") {
		t.Errorf("bulk command: exit %d\nstdout %s\nstderr %s", r.code, r.stdout, r.stderr)
	}
}

func TestLogsAndFiles(t *testing.T) {
	f := newFake(t)
	f.on("GET /v0/game-servers/gs_1/logs/stream", func(r *http.Request, _ map[string]any) (int, any) {
		return 200, "line one\nline two\n"
	})
	f.run(t, "logs", "gs_1", "--tail", "50", "--follow").must(t, 0, "line one\nline two")
	// A running console with nothing sent yet has no size (null): never shown as 0 bytes.
	f.json("GET /v0/game-servers/gs_1/logs", 200, map[string]any{"expires_at": "2026-02-01T00:00:00Z", "sources": []any{
		map[string]any{"name": "console", "bytes": nil, "live": true},
		map[string]any{"name": "Saved/Logs/Brawl.log", "bytes": 2048, "live": true}}})
	listed := f.run(t, "logs", "list", "gs_1")
	listed.must(t, 0, "not sent yet", "2.0 KB")
	if strings.Contains(listed.stdout, "0 bytes") {
		t.Errorf("a console with nothing sent yet read as 0 bytes:\n%s", listed.stdout)
	}
	q := f.called("GET", "/v0/game-servers/gs_1/logs/stream")[0].Query
	if q.Get("tail") != "50" || q.Get("follow") != "true" || q.Get("source") != "console" {
		t.Errorf("log query: %v", q)
	}
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("MaxPlayers=10\n")) }))
	t.Cleanup(files.Close)
	f.on("GET /v0/game-servers/gs_1/files/download", func(r *http.Request, _ map[string]any) (int, any) {
		if r.Header.Get("Accept") == "application/json" {
			return 200, map[string]any{"url": files.URL + "/x", "expires_at": "x"}
		}
		return 303, redirect(files.URL + "/x")
	})
	out := filepath.Join(t.TempDir(), "Game.ini")
	f.runEnvWithStorage(t, files.Client(), "files", "get", "gs_1", "Saved/Config/Game.ini", "-o", out).must(t, 0, "Game.ini: 14 bytes")
	if data, _ := os.ReadFile(out); string(data) != "MaxPlayers=10\n" {
		t.Errorf("downloaded %q", data)
	}
	f.runEnvWithStorage(t, files.Client(), "files", "get", "gs_1", "Game.ini", "-o", "-").must(t, 0, "MaxPlayers=10")
}

func TestRegionsAndContinents(t *testing.T) {
	f := newFake(t)
	f.json("GET /v0/regions", 200, map[string]any{"data": []any{map[string]any{"id": "europe-west", "name": "Europe West", "continent": "europe", "status": "active",
		"offers": []any{map[string]any{"tier": "standard", "os": []any{"windows", "linux"}}}}}, "next_cursor": nil})
	f.run(t, "regions", "--continent", "europe", "--os", "linux").must(t, 0, "REGION", "CONTINENT", "europe-west", "Europe West", "standard (windows, linux)")
	if q := f.called("GET", "/v0/regions")[0].Query; q.Get("continent") != "europe" || q.Get("os") != "linux" || q.Get("region") != "" {
		t.Errorf("regions query: %v", q)
	}
	f.json("GET /v0/continents", 200, map[string]any{"data": []any{map[string]any{"id": "europe", "name": "Europe", "regions": []any{"europe-west", "europe-central", "uk"}}}, "next_cursor": nil})
	f.run(t, "continents").must(t, 0, "CONTINENT", "Europe", "europe-west, europe-central, uk")
	f.run(t, "cities").must(t, 2, `no command "cities"`)
	f.run(t, "regions", "--city", "x").must(t, 2, "no flag --city")
}

func TestPersistentServersPoolsUsageAndRegistry(t *testing.T) {
	f := newFake(t)
	ps := func(st string) map[string]any {
		return map[string]any{"id": "ps_1", "name": "eu-main", "status": st, "region": "europe-central", "continent": "europe", "version": "2.0.1",
			"address":     map[string]any{"ipv4": "203.0.113.77", "ports": map[string]any{"game": map[string]any{"port": 24015}}},
			"last_backup": map[string]any{"id": "bkp_1", "kind": "stop", "created_at": "2026-10-30T10:00:00Z"}}
	}
	f.json("POST /v0/persistent-servers", 201, ps("starting"))
	f.json("GET /v0/persistent-servers/ps_1", 200, ps("running"))
	settings := filepath.Join(t.TempDir(), "persistent-server.json")
	os.WriteFile(settings, []byte(`{"app": "brawl", "name": "eu-main", "region": "europe-central", "branch": "live", "when_empty": {"after": "10m", "then": "pause"}}`), 0o600)
	f.run(t, "persistent-servers", "create", settings, "--wait").must(t, 0, "Persistent server eu-main (ps_1): running in europe-central", "203.0.113.77:24015",
		"Last backup: bkp_1 (At stop, 2026-10-30T10:00:00Z)")
	if f.called("POST", "/v0/persistent-servers")[0].Body["when_empty"].(map[string]any)["after"] != "10m" {
		t.Error("the settings file goes as it is")
	}
	f.json("POST /v0/persistent-servers/ps_1/stop", 202, ps("stopping"))
	f.run(t, "persistent-servers", "stop", "ps_1", "--shutdown-time", "60s", "--wait").must(t, 0, "running")
	if f.called("POST", "/v0/persistent-servers/ps_1/stop")[0].Body["shutdown_time"] != "60s" {
		t.Error("stop grace")
	}
	f.run(t, "persistent-servers", "update", "ps_1", "--apply", "now").must(t, 2, "nothing to change")
	f.json("PATCH /v0/persistent-servers/ps_1", 200, ps("running"))
	f.run(t, "persistent-servers", "update", "ps_1", "--version", "2.1.0").must(t, 0)

	// A paused persistent server counts as settled, and says how it comes back.
	paused := ps("paused")
	paused["paused_since"] = "2026-10-30T11:00:00Z"
	f.json("GET /v0/persistent-servers/ps_2", 200, paused)
	f.run(t, "persistent-servers", "get", "ps_2", "--wait", "--timeout", "1s").must(t, 0, "paused", "Paused since 2026-10-30T11:00:00Z; the first player resumes it")
	f.json("GET /v0/persistent-servers/ps_3", 200, ps("resuming"))
	f.run(t, "persistent-servers", "get", "ps_3", "--wait", "--timeout", "50ms").must(t, 3, "persistent server ps_3 is still resuming")

	// Backups: take one, list them with their kinds' words, restore one.
	f.json("POST /v0/persistent-servers/ps_1/backups", 202, map[string]any{"id": "bkp_9", "persistent_server": "ps_1", "kind": "manual", "status": "in_progress"})
	f.run(t, "persistent-servers", "backup", "ps_1").must(t, 0, "Backup bkp_9 of ps_1: in_progress")
	f.json("GET /v0/persistent-servers/ps_1/backups", 200, map[string]any{"data": []any{
		map[string]any{"id": "bkp_9", "kind": "manual", "status": "ready", "bytes": 2048, "created_at": "x"},
		map[string]any{"id": "bkp_8", "kind": "continuous", "status": "ready", "bytes": 1024, "created_at": "y"}}, "next_cursor": nil})
	f.run(t, "persistent-servers", "backups", "ps_1").must(t, 0, "BACKUP", "By hand", "Continuous", "2.0 KB",
		"Continuous: copied within seconds of each game save; the newest 3 are kept, apart from your other backups.")
	if r := f.run(t, "persistent-servers", "backups", "ps_1", "--json"); r.code != 0 || strings.Contains(r.stdout, "Continuous:") {
		t.Errorf("--json prints only the API's JSON: %q", r.stdout)
	}
	f.json("POST /v0/persistent-servers/ps_1/restore", 202, map[string]any{"bulk_action": map[string]any{"id": "bulk_7"}, "plan": map[string]any{}})
	f.run(t, "persistent-servers", "restore", "ps_1", "bkp_9").must(t, 0, "Restoring persistent server ps_1", "survivalservers bulk-actions undo bulk_7")
	if b := f.called("POST", "/v0/persistent-servers/ps_1/restore")[0].Body; b["backup"] != "bkp_9" || len(b) != 1 {
		t.Errorf("restore body: %v", b)
	}
	f.run(t, "restore", "ps_1", "--at", "2026-10-30 14:00 Europe/Berlin", "--version", "then", "--no-start").must(t, 0, "Restoring persistent server ps_1")
	if b := f.called("POST", "/v0/persistent-servers/ps_1/restore")[1].Body; b["at"] != "2026-10-30T13:00:00Z" || b["version"] != "then" || b["start"] != false || b["backup"] != nil {
		t.Errorf("restore by time, body: %v", b)
	}
	f.run(t, "restore", "ps_1").must(t, 2, "give --backup or --at")
	f.run(t, "worlds", "list").must(t, 2, `no command "worlds"`)
	f.run(t, "persistent-servers", "snapshots", "ps_1").must(t, 2, "one of backup, backups, create")

	f.json("PUT /v0/apps/brawl/pools/us-central", 200, map[string]any{"region": "us-central", "status": map[string]any{"ready": 1, "target": 4}})
	f.run(t, "pools", "set", "brawl", "us-central", "--branch", "live", "--cpu", "2", "--memory-gb", "4", "--mode", "auto", "--min", "2", "--max", "8").must(t, 0, "Ready servers for brawl in us-central: target 4", "that price is not final")
	pb := f.called("PUT", "/v0/apps/brawl/pools/us-central")[0].Body
	if pb["mode"] != "auto" || pb["max"] != 8.0 || pb["size"].(map[string]any)["cpu"] != 2.0 {
		t.Errorf("pool body: %v", pb)
	}

	f.json("GET /v0/usage", 200, map[string]any{"currency": "USD", "totals": map[string]any{"cpu_hours": "12.5", "amount_usd": "0.44", "free_cpu_hours_used": "12.5", "credits_usd": "0", "projected_month_usd": "1.30"},
		"rows": []any{map[string]any{"date": "2026-10-19", "app": "brawl", "cpu_hours": "12.5", "amount_usd": "0.44"}}})
	f.run(t, "usage", "--group-by", "app").must(t, 0, "12.5 vCPU-hours, $0.44", "Projected to the end of this month: $1.30.", "brawl")
	uq := f.called("GET", "/v0/usage")[0].Query
	if uq.Get("from") == "" || uq.Get("to") == "" || uq.Get("group_by") != "app" {
		t.Errorf("usage query: %v", uq)
	}

	f.json("POST /v0/registry/token", 200, map[string]any{"registry": "registry.example.com", "repository": "registry.example.com/acme",
		"username": "std_1", "password": "s3cret-pass", "expires_at": "x"})
	var dockerArgs []string
	var dockerIn string
	var out, errb bytes.Buffer
	code := Run(context.Background(), []string{"registry", "login"}, Env{Stdout: &out, Stderr: &errb,
		Getenv: func(k string) string {
			return map[string]string{"GS_API_KEY": "gsk_live_EXAMPLE_not_a_real_key", "GS_API_URL": f.srv.URL}[k]
		},
		Docker: func(_ context.Context, stdin io.Reader, args ...string) error {
			dockerArgs = args
			b, _ := io.ReadAll(stdin)
			dockerIn = string(b)
			return nil
		}})
	if code != 0 || dockerIn != "s3cret-pass" || strings.Contains(strings.Join(dockerArgs, " "), "s3cret") || strings.Contains(out.String(), "s3cret") {
		t.Errorf("the password goes to docker on stdin only: code %d, args %v, stdout %q", code, dockerArgs, out.String())
	}
	// --json logs in too and answers without the password (an AI agent running it would put it in the chat).
	out.Reset()
	dockerIn = ""
	code = Run(context.Background(), []string{"registry", "login", "--json"}, Env{Stdout: &out, Stderr: &errb,
		Getenv: func(k string) string {
			return map[string]string{"GS_API_KEY": "gsk_live_EXAMPLE_not_a_real_key", "GS_API_URL": f.srv.URL}[k]
		},
		Docker: func(_ context.Context, stdin io.Reader, args ...string) error {
			b, _ := io.ReadAll(stdin)
			dockerIn = string(b)
			return nil
		}})
	if code != 0 || dockerIn != "s3cret-pass" || strings.Contains(out.String()+errb.String(), "s3cret") || !strings.Contains(out.String(), `"logged_in": true`) ||
		!strings.Contains(out.String(), `"repository": "registry.example.com/acme"`) {
		t.Errorf("--json: code %d, docker stdin %q, stdout %q", code, dockerIn, out.String())
	}
}

// Branding: pictures go up as their own bytes with the type their name says, colors as JSON, and --remove and
// --clear take them away.
func TestBranding(t *testing.T) {
	f := newFake(t)
	dir := t.TempDir()
	icon := filepath.Join(dir, "arena.PNG")
	os.WriteFile(icon, []byte("\x89PNG\r\n\x1a\nnot really"), 0o600)
	banner := filepath.Join(dir, "keyart.jpg")
	os.WriteFile(banner, []byte{0xff, 0xd8, 0xff, 0xe0}, 0o600)
	f.json("PUT /v0/apps/arena/icon", 200, map[string]any{"slug": "arena", "icon": map[string]any{
		"url_256": "https://api.example.com/v0/images/app_x-0123456789abcdef-256.webp?expires=1793966400&signature=s1", "expires_at": "2026-11-07T12:00:00Z"}})
	f.json("PUT /v0/apps/arena/banner", 200, map[string]any{"slug": "arena", "banner": map[string]any{"url_1600": "https://api.example.com/v0/images/app_x-0123456789abcdef-1600.jpg"}})
	f.json("DELETE /v0/apps/arena/icon", 200, map[string]any{"slug": "arena"})
	f.json("PUT /v0/studio/icon", 200, map[string]any{"slug": "fenmarch"})
	f.on("PATCH /v0/apps/arena", func(_ *http.Request, b map[string]any) (int, any) {
		return 200, map[string]any{"slug": "arena", "color": b["color"]}
	})
	f.on("PATCH /v0/studio", func(_ *http.Request, b map[string]any) (int, any) {
		out := map[string]any{"name": "Fenmarch Games", "slug": "fenmarch", "accent_color": b["accent_color"]}
		if n, ok := b["name"]; ok {
			out["name"] = n
		}
		return 200, out
	})

	f.run(t, "apps", "icon", "arena", icon).must(t, 0,
		"arena's icon is set. https://api.example.com/v0/images/app_x-0123456789abcdef-256.webp?expires=1793966400&signature=s1 (this link works until 2026-11-07 12:00 UTC)")
	c := f.called("PUT", "/v0/apps/arena/icon")[0]
	if c.ContentType != "image/png" || string(c.Raw) != "\x89PNG\r\n\x1a\nnot really" || c.Idem != "" {
		t.Errorf("icon upload: %q %q %q", c.ContentType, c.Raw, c.Idem)
	}
	f.run(t, "apps", "banner", "arena", banner).must(t, 0, "arena's banner is set.")
	if c := f.called("PUT", "/v0/apps/arena/banner")[0]; c.ContentType != "image/jpeg" || len(c.Raw) != 4 {
		t.Errorf("banner upload: %q %d bytes", c.ContentType, len(c.Raw))
	}
	f.run(t, "apps", "icon", "arena", "--remove").must(t, 0, "arena's icon is removed.")
	f.run(t, "apps", "icon", "arena", icon, "--remove").must(t, 2, "")
	f.run(t, "apps", "color", "arena", "#3a86ff").must(t, 0, "arena's color is #3a86ff.")
	f.run(t, "apps", "color", "arena", "--clear").must(t, 0, "arena's color is made from its name.")
	if cs := f.called("PATCH", "/v0/apps/arena"); len(cs) != 2 || cs[1].Body["color"] != nil || !strings.Contains(string(cs[1].Raw), `"color":null`) {
		t.Errorf("color clear sends null: %+v", cs)
	}
	f.run(t, "studio", "icon", icon).must(t, 0, "The studio's icon is set.")
	f.run(t, "studio", "rename", "Fenmarch Studios").must(t, 0, "The studio is now Fenmarch Studios. Its short name stays fenmarch, so addresses and scripts that use it keep working.")
	f.run(t, "studio", "accent", "#2e9e8f").must(t, 0, "The studio's accent color is #2e9e8f.")
	f.run(t, "studio", "accent").must(t, 2, "")
}

// A download arrives whole or not at all: every file against its SHA-256.
func TestBackupDownloadChecksEveryFile(t *testing.T) {
	f := newFake(t)
	var zipped bytes.Buffer
	zw := zip.NewWriter(&zipped)
	w, _ := zw.Create("Saved/world.sav")
	w.Write([]byte("the bay"))
	sum := sha256.Sum256([]byte("the bay"))
	s, _ := zw.Create("SHA256SUMS")
	fmt.Fprintf(s, "%s  Saved/world.sav\n", hex.EncodeToString(sum[:]))
	zw.Close()
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write(zipped.Bytes()) }))
	defer store.Close()
	f.json("POST /v0/backups/bkp_1/export", 202, map[string]any{"status": "building"})
	f.json("GET /v0/backups/bkp_1/export", 200, map[string]any{"status": "ready", "url": store.URL + "/x.zip"})
	out := filepath.Join(t.TempDir(), "b.zip")
	f.run(t, "backups", "download", "bkp_1", "-o", out).must(t, 0, "1 files, every one checked")

	var bad bytes.Buffer
	zw = zip.NewWriter(&bad)
	w, _ = zw.Create("Saved/world.sav")
	w.Write([]byte("rot"))
	s, _ = zw.Create("SHA256SUMS")
	fmt.Fprintf(s, "%s  Saved/world.sav\n", hex.EncodeToString(sum[:]))
	zw.Close()
	zipped = bad
	f.run(t, "backups", "download", "bkp_1", "-o", out).must(t, 1, "does not match its SHA-256")
}

// A save goes up, is checked, and becomes a backup; the server is left alone.
func TestBackupUpload(t *testing.T) {
	f := newFake(t)
	got := make(chan []byte, 1)
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- b
	}))
	defer store.Close()
	file := filepath.Join(t.TempDir(), "saves.zip")
	if err := os.WriteFile(file, []byte("a zip"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.json("POST /v0/persistent-servers/ps_1/backups/uploads", 201, map[string]any{"id": "upl_1", "url": store.URL + "/up"})
	f.json("POST /v0/persistent-servers/ps_1/backups/uploads/upl_1/complete", 202, map[string]any{"status": "processing"})
	f.json("GET /v0/persistent-servers/ps_1/backups/uploads/upl_1", 200, map[string]any{"status": "done", "backup": "bkp_7"})
	f.run(t, "backups", "upload", "ps_1", file, "--label", "Season 2").must(t, 0, "Backup bkp_7 holds your save", "survivalservers restore ps_1 --backup bkp_7")
	if b := <-got; string(b) != "a zip" {
		t.Fatalf("storage got %q", b)
	}
	if b := f.called("POST", "/v0/persistent-servers/ps_1/backups/uploads")[0].Body; b["label"] != "Season 2" || b["bytes"] != 5.0 {
		t.Errorf("upload body %v", b)
	}
	f.json("POST /v0/persistent-servers/ps_1/backups", 202, map[string]any{"id": "bkp_9", "status": "in_progress"})
	f.run(t, "backups", "take", "ps_1", "--label", "Before the patch", "--lock").must(t, 0, "Backup bkp_9")
	if b := f.called("POST", "/v0/persistent-servers/ps_1/backups")[0].Body; b["label"] != "Before the patch" || b["locked"] != true {
		t.Errorf("take body %v", b)
	}
	f.json("PATCH /v0/backups/bkp_9", 200, map[string]any{"id": "bkp_9", "locked": true})
	f.run(t, "backups", "lock", "bkp_9").must(t, 0, "Locked")
	f.json("DELETE /v0/backups/bkp_9", 204, nil)
	f.run(t, "backups", "delete", "bkp_9").must(t, 0, "Backup bkp_9 deleted.")
}

// A rollback shows its preview and runs exactly it, only once it was told yes.
func TestRollbackPreviewsFirst(t *testing.T) {
	f := newFake(t)
	plan := map[string]any{"id": "pln_1", "total": 2, "players": 12, "targets": []any{
		map[string]any{"name": "EU PvE 1", "region": "europe-central", "players": 12, "version": "2.1.0",
			"restore": map[string]any{"saved_at": "2026-10-30T11:57:00Z", "play_lost": "2h3m0s", "version": "2.0.1"}},
		map[string]any{"name": "EU PvE 2", "region": "europe-west", "players": 0, "version": "2.1.0",
			"restore": map[string]any{"saved_at": "2026-10-30T11:58:00Z", "play_lost": "2h2m0s", "version": "2.0.1"}}},
		"skipped": []any{map[string]any{"name": "EU New", "detail": "It has no backup from before that time."}}}
	f.on("POST /v0/bulk-actions/rollbacks", func(_ *http.Request, b map[string]any) (int, any) {
		if b["plan"] != nil {
			return 202, map[string]any{"bulk_action": map[string]any{"id": "bulk_9"}, "plan": plan}
		}
		return 200, map[string]any{"plan": plan}
	})
	f.run(t, "rollback", "--app", "brinewatch").must(t, 2, "give --before or --before-update")
	f.run(t, "rollback", "--app", "brinewatch", "--before", "14:00", "--before-update", "bulk_5").must(t, 2, "give --before or --before-update")
	// No answer at the question (an empty stdin) is a no.
	f.run(t, "rollback", "--app", "brinewatch", "--continent", "europe", "--label", "mode=pvp", "--before", "2026-10-30 14:00 Europe/Berlin", "--version", "then",
		"--try-one-first").must(t, 0, "2 persistent servers, 12 players online now", "EU PvE 1", "2h3m0s", "It has no backup from before that time.", "Roll back 2 persistent servers?", "Nothing was changed.")
	calls := f.called("POST", "/v0/bulk-actions/rollbacks")
	if len(calls) != 1 {
		t.Fatalf("%d calls without a yes", len(calls))
	}
	b := calls[0].Body
	sel := b["selector"].(map[string]any)
	if b["at"] != "2026-10-30T13:00:00Z" || b["version"] != "then" || sel["app"] != "brinewatch" || sel["labels"].(map[string]any)["mode"] != "pvp" ||
		b["rollout"].(map[string]any)["first"] == nil {
		t.Errorf("preview body %v", b)
	}
	f.run(t, "rollback", "--app", "brinewatch", "--before", "14:00", "--yes").must(t, 0, "Rolling back: bulk_9", "survivalservers bulk-actions undo bulk_9")
	calls = f.called("POST", "/v0/bulk-actions/rollbacks")
	if len(calls) != 3 || calls[2].Body["plan"] != "pln_1" || len(calls[2].Body) != 1 {
		t.Errorf("the run must send only the preview's id: %v", calls)
	}
	// Back to before a rollout's update: the rollout in place of a time.
	f.run(t, "rollback", "--app", "brinewatch", "--before-update", "bulk_5", "--version", "then").must(t, 0, "Roll back 2 persistent servers?", "Nothing was changed.")
	calls = f.called("POST", "/v0/bulk-actions/rollbacks")
	if b := calls[len(calls)-1].Body; b["before_update"] != "bulk_5" || b["at"] != nil || b["version"] != "then" {
		t.Errorf("before-update preview body %v", b)
	}
	// Undo and retry make a new bulk action, resume goes on with the same one: each line says which runs, on how many.
	persistent := map[string]any{"target": "persistent_servers"}
	f.json("POST /v0/bulk-actions/bulk_9/undo", 202, map[string]any{"bulk_action": map[string]any{"id": "bulk_10", "selector": persistent,
		"counts": map[string]any{"total": 2, "pending": 2}}, "plan": map[string]any{}})
	f.run(t, "bulk-actions", "undo", "bulk_9").must(t, 0, "Bulk action bulk_10 undoes bulk_9 on 2 persistent servers.", "Watch it: survivalservers bulk-actions watch bulk_10")
	f.json("POST /v0/bulk-actions/bulk_8/retry", 202, map[string]any{"bulk_action": map[string]any{"id": "bulk_11", "selector": map[string]any{"target": "game_servers"},
		"counts": map[string]any{"total": 1, "pending": 1}}, "plan": map[string]any{}})
	f.run(t, "bulk-actions", "retry", "bulk_8").must(t, 0, "Bulk action bulk_11 retries bulk_8 on its 1 failed game server.", "survivalservers bulk-actions watch bulk_11")
	f.json("POST /v0/bulk-actions/bulk_7/resume", 202, map[string]any{"id": "bulk_7", "selector": persistent, "counts": map[string]any{"total": 40, "pending": 28}})
	r := f.run(t, "bulk-actions", "resume", "bulk_7")
	r.must(t, 0, "Bulk action bulk_7 resumed: 28 of 40 persistent servers still to do.", "survivalservers bulk-actions watch bulk_7")
	if strings.Contains(r.stdout, "under way") {
		t.Errorf("a follow-up says what runs, not that it is under way: %s", r.stdout)
	}
	f.json("GET /v0/bulk-actions/bulk_10", 200, map[string]any{"id": "bulk_10", "status": "succeeded", "counts": map[string]any{"succeeded": 2, "total": 2, "failed": 0}})
	f.run(t, "bulk-actions", "watch", "bulk_10").must(t, 0, "succeeded: 2 of 2 done, 0 failed")
}

func TestCommandsAndBulk(t *testing.T) {
	f := newFake(t)
	f.json("POST /v0/persistent-servers/ps_1/command", 200, map[string]any{"delivered": true, "output": "Saved\n"})
	f.json("POST /v0/game-servers/gs_1/command", 200, map[string]any{"delivered": false, "error": "the machine is not connected"})
	f.on("POST /v0/bulk-actions/commands", func(_ *http.Request, b map[string]any) (int, any) {
		if b["dry_run"] == true {
			return 200, map[string]any{"plan": map[string]any{"total": 12, "players": 40, "batches": []any{map[string]any{"batch": 1}},
				"skipped": []any{map[string]any{"name": "EU 3", "reason": "no_command", "detail": "Its version has no game command announce."}}}}
		}
		return 202, map[string]any{"bulk_action": map[string]any{"id": "bulk_1", "status": "running"}}
	})
	f.json("GET /v0/bulk-actions/bulk_1", 200, map[string]any{"id": "bulk_1", "status": "succeeded", "counts": map[string]any{"total": 12, "succeeded": 12, "failed": 0, "skipped": 1}})
	f.json("GET /v0/bulk-actions/bulk_1/replies", 200, map[string]any{"data": []any{map[string]any{"reply": "Saved", "status": "succeeded", "count": 11},
		map[string]any{"reply": "", "status": "succeeded", "count": 1}}, "next_cursor": nil})
	f.json("GET /v0/apps/brawl/versions/1.4.2/profile", 200, map[string]any{"profile": map[string]any{"commands": []any{}}, "revision": 3})
	f.on("PUT /v0/apps/brawl/versions/1.4.2/profile", func(_ *http.Request, b map[string]any) (int, any) {
		return 200, map[string]any{"profile": b, "revision": 4}
	})

	f.run(t, "command", "ps_1", "announce", "text=Double XP starts now").must(t, 0, "Saved")
	sent := f.called("POST", "/v0/persistent-servers/ps_1/command")
	if b := sent[0].Body; b["command_id"] != "announce" || obj(b["args"])["text"] != "Double XP starts now" {
		t.Fatalf("command body: %v", b)
	}
	f.run(t, "command", "ps_1", "--line", "status", "--via", "rcon").must(t, 0)
	sent = f.called("POST", "/v0/persistent-servers/ps_1/command")
	if b := sent[1].Body; b["line"] != "status" || b["via"] != "rcon" {
		t.Fatalf("line body: %v", b)
	}
	f.run(t, "command", "gs_1", "save").must(t, 1, "not connected")
	f.run(t, "command", "oops", "save").must(t, 2, "not a persistent server or game server ID")
	f.run(t, "command", "ps_1", "--via", "rcon", "save").must(t, 2, "--via goes with --line")
	f.run(t, "command", "ps_1").must(t, 2, "give a game command or --line")

	f.run(t, "bulk-actions", "command", "--app", "brawl", "--label", "mode:pvp", "--region", "us-east", "--dry-run", "announce", "text=hi").
		must(t, 0, "Would reach 12", "Skips EU 3: Its version has no game command announce.")
	sent = f.called("POST", "/v0/bulk-actions/commands")
	b := sent[0].Body
	if sel := obj(b["selector"]); sel["app"] != "brawl" || obj(sel["labels"])["mode"] != "pvp" || b["dry_run"] != true {
		t.Fatalf("selector: %v", b)
	}
	f.run(t, "bulk-actions", "command", "announce").must(t, 2, "say which persistent servers or game servers")
	f.run(t, "bulk-actions", "command", "--app", "brawl", "--wait", "save").must(t, 0, "12 of 12 done", "(no answer)")
	f.run(t, "bulk-actions", "replies", "bulk_1").must(t, 0, "Saved", "COUNT")

	file := filepath.Join(t.TempDir(), "game-profile.json")
	if err := os.WriteFile(file, []byte(`{"commands":[{"id":"save","label":"Save","send":"save"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	f.run(t, "profile", "set", "brawl", "1.4.2", file).must(t, 0, "revision 4")
	f.json("GET /v0/profile-templates", 200, map[string]any{"data": []any{map[string]any{"id": "palworld", "name": "Palworld", "summary": "REST"}}, "next_cursor": nil})
	f.json("GET /v0/profile-templates/palworld", 200, map[string]any{"id": "palworld", "profile": map[string]any{"commands": []any{}}, "setup": []any{"Turn the REST API on."}})
	f.run(t, "profile", "templates").must(t, 0, "palworld", "Palworld")
	f.run(t, "profile", "set", "brawl", "1.4.2", "--from", "palworld").must(t, 0, "revision 4", "Setup: Turn the REST API on.")
	f.run(t, "profile", "set", "brawl", "1.4.2").must(t, 2, "give a file or --from")
	r := f.run(t, "profile", "get", "brawl", "1.4.2")
	r.must(t, 0, `"commands"`)
	if strings.Contains(r.stdout, "revision") {
		t.Fatalf("profile get prints a file to edit: %s", r.stdout)
	}
}

func TestSavedActions(t *testing.T) {
	f := newFake(t)
	f.json("GET /v0/saved-actions", 200, map[string]any{"data": []any{map[string]any{"id": "sav_1", "name": "Double XP weekend", "action": "command", "paused": true,
		"schedule": map[string]any{"cron": "0 18 * * 5", "time_zone": "UTC"}}}, "next_cursor": nil})
	f.on("POST /v0/saved-actions", func(_ *http.Request, b map[string]any) (int, any) {
		return 201, map[string]any{"id": "sav_1", "name": b["name"]}
	})
	f.on("POST /v0/saved-actions/sav_1/run", func(_ *http.Request, b map[string]any) (int, any) {
		if b["dry_run"] == true {
			return 200, map[string]any{"plan": map[string]any{"total": 3, "players": 1, "batches": []any{}}}
		}
		return 202, map[string]any{"bulk_action": map[string]any{"id": "bulk_9", "status": "running"}}
	})
	f.on("PATCH /v0/saved-actions/sav_1", func(_ *http.Request, b map[string]any) (int, any) {
		return 200, map[string]any{"name": "Double XP weekend"}
	})
	f.json("DELETE /v0/saved-actions/sav_1", 204, nil)

	f.run(t, "saved", "list").must(t, 0, "Double XP weekend", "paused", "0 18 * * 5 (UTC)")
	file := filepath.Join(t.TempDir(), "request.json")
	if err := os.WriteFile(file, []byte(`{"selector":{"target":"persistent_servers","app":"brawl"},"command_id":"save"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	f.run(t, "saved", "create", "Double XP weekend", "command", file, "--cron", "0 18 * * 5", "--time-zone", "UTC").must(t, 0, "sav_1")
	sent := f.called("POST", "/v0/saved-actions")[0].Body
	if obj(sent["schedule"])["cron"] != "0 18 * * 5" || obj(sent["request"])["command_id"] != "save" {
		t.Fatalf("create body: %v", sent)
	}
	f.run(t, "saved", "create", "X", "command", file, "--time-zone", "UTC").must(t, 2, "--time-zone goes with --cron")
	f.run(t, "saved", "run", "sav_1", "--dry-run").must(t, 0, "Would reach 3")
	f.run(t, "saved", "run", "sav_1").must(t, 0, "bulk_9")
	f.run(t, "saved", "pause", "sav_1").must(t, 0, "paused true")
	if b := f.called("PATCH", "/v0/saved-actions/sav_1")[0].Body; b["paused"] != true {
		t.Fatalf("pause: %v", b)
	}
	f.run(t, "saved", "delete", "sav_1").must(t, 0, "Deleted sav_1")
}

func TestServerConfig(t *testing.T) {
	f := newFake(t)
	view := map[string]any{"settings": []any{map[string]any{"id": "xp_rate", "value": 2, "source": "server", "pending": true},
		map[string]any{"id": "pvp", "value": true, "source": "default", "pending": false}},
		"results": []any{map[string]any{"id": "xp_rate", "delivered": true}, map[string]any{"id": "gravity", "delivered": false, "error": "not running"}}}
	f.json("GET /v0/persistent-servers/ps_1/config", 200, view)
	f.json("PATCH /v0/persistent-servers/ps_1/config", 200, view)
	f.run(t, "config", "get", "ps_1").must(t, 0, "xp_rate", "at the next restart", "SETTING")
	f.json("GET /v0/persistent-servers/ps_2/config", 200, map[string]any{"not_applied": "Not applied: this machine needs the newest agent",
		"settings": []any{map[string]any{"id": "xp_rate", "value": 2, "source": "server", "pending": false, "not_applied": true}}})
	f.run(t, "config", "get", "ps_2").must(t, 0, "Not applied: this machine needs the newest agent", "not applied")
	f.run(t, "config", "set", "ps_1", "xp_rate=2", "pvp=true", "name=EU 1", "--unset", "motd", "--live").must(t, 0, "xp_rate changed now", "gravity did not change in the running game: not running")
	b := f.called("PATCH", "/v0/persistent-servers/ps_1/config")[0].Body
	values := obj(b["values"])
	if values["xp_rate"] != 2.0 || values["pvp"] != true || values["name"] != "EU 1" || b["live"] != true {
		t.Fatalf("body: %v", b)
	}
	if v, has := values["motd"]; !has || v != nil {
		t.Fatalf("unset: %v", values)
	}
	f.run(t, "config", "set", "ps_1").must(t, 2, "give at least one")

	// With the game profile's types: a text setting keeps digits and true as text, and a number or a yes/no is sent
	// as one (a password of 12345 once went out as a number and was refused).
	f.json("GET /v0/persistent-servers/ps_1", 200, map[string]any{"id": "ps_1", "app": "app_w", "version": "1.0.0"})
	f.json("GET /v0/apps/app_w/versions/1.0.0/profile", 200, map[string]any{"profile": map[string]any{"config": map[string]any{"settings": []any{
		map[string]any{"id": "server_password", "type": "text"}, map[string]any{"id": "motd", "type": "text"},
		map[string]any{"id": "pvp", "type": "bool"}, map[string]any{"id": "max_players", "type": "int"}, map[string]any{"id": "mode", "type": "choice"}}}}})
	f.run(t, "config", "set", "ps_1", "server_password=12345", "motd=true", "pvp=true", "max_players=16", "mode=1").must(t, 0)
	calls := f.called("PATCH", "/v0/persistent-servers/ps_1/config")
	values = obj(calls[len(calls)-1].Body["values"])
	if values["server_password"] != "12345" || values["motd"] != "true" || values["pvp"] != true || values["max_players"] != 16.0 || values["mode"] != "1" {
		t.Errorf("typed values: %#v", values)
	}
	// Without a profile to read: quoted text is text, without its quote marks.
	f.json("PATCH /v0/persistent-servers/ps_9/config", 200, view)
	f.run(t, "config", "set", "ps_9", `server_password="12345"`, "xp_rate=2").must(t, 0)
	values = obj(f.called("PATCH", "/v0/persistent-servers/ps_9/config")[0].Body["values"])
	if values["server_password"] != "12345" || values["xp_rate"] != 2.0 {
		t.Errorf("untyped values: %#v", values)
	}
}

func TestPlayers(t *testing.T) {
	f := newFake(t)
	f.json("GET /v0/persistent-servers/ps_1/players", 200, map[string]any{"source": "reported", "players": []any{map[string]any{"id": "p_1", "name": "Vex_77", "team": "red", "ping_ms": 34}}})
	f.json("GET /v0/game-servers/gs_1/players", 200, map[string]any{"source": "reported", "players": []any{}, "error": "Not available: this machine needs the newest agent"})
	f.run(t, "players", "ps_1").must(t, 0, "Vex_77", "PLAYER")
	f.run(t, "players", "gs_1").must(t, 1, "Not available: this machine needs the newest agent")
}

// Usage names a projection only when there is one, in plain words; prices lists every rate the API gives, each with
// its unit, backup storage and the warm rate included.
func TestUsageAndPrices(t *testing.T) {
	f := newFake(t)
	f.json("GET /v0/usage", 200, map[string]any{"currency": "USD", "totals": map[string]any{"cpu_hours": "3.0", "amount_usd": "0.11", "free_cpu_hours_used": "0",
		"credits_usd": "0", "projected_month_usd": "0.11"}, "rows": []any{}})
	r := f.run(t, "usage")
	r.must(t, 0, "3.0 vCPU-hours, $0.11 (free vCPU-hours used 0, credits $0).")
	if strings.Contains(r.stdout, "Projected") || strings.Contains(r.stdout, "heading for") {
		t.Errorf("no projection when it is only the spend so far: %s", r.stdout)
	}

	rates := map[string]any{"on_demand_cpu_hour_2gb": "0.0350", "on_demand_cpu_hour_4gb": "0.0500", "reserved_cpu_hour": "0.0250",
		"persistent_server_cpu_hour": "0.0250", "paused_cpu_hour": "0.0250", "warm_cpu_hour": "0.0300", "own_machine_cpu_month": "1.0000",
		"backup_gb_month": "0.0500"}
	f.json("GET /v0/prices", 200, map[string]any{"currency": "USD", "locked_until": "2028-10-09T00:00:00Z", "free_cpu_hours_per_month": 2000,
		"paused_free_minutes": 60, "free_backup_gb_months": 100, "bandwidth": "included", "rates": rates})
	r = f.run(t, "prices")
	r.must(t, 0, "Prices in USD, locked until 2028-10-09T00:00:00Z. Free: 2000 free vCPU-hours a month, the first 60 minutes of each pause, 100 GB of backup storage a month. Bandwidth included.")
	lines := map[string]string{}
	for _, l := range strings.Split(r.stdout, "\n") {
		if f := strings.Fields(l); len(f) > 0 {
			lines[f[len(f)-1]] = l
		}
	}
	for field, want := range map[string][]string{
		"on_demand_cpu_hour_2gb": {"On demand, up to 2 GB per vCPU", "$0.0350", "vCPU-hour"},
		"on_demand_cpu_hour_4gb": {"On demand, up to 4 GB per vCPU", "$0.0500", "vCPU-hour"},
		"warm_cpu_hour":          {"Ready servers waiting in a pool", "$0.0300", "vCPU-hour"},
		"own_machine_cpu_month":  {"Your own machines", "$1.0000", "vCPU a month"},
		"backup_gb_month":        {"Backup storage past the free GB", "$0.0500", "GB a month"},
	} {
		for _, w := range want {
			if !strings.Contains(lines[field], w) {
				t.Errorf("%s row lacks %q: %q", field, w, lines[field])
			}
		}
	}
	for k := range rates {
		if lines[k] == "" {
			t.Errorf("no row for %s", k)
		}
	}
}

// survivalservers prices reads exactly the rates the API file's Prices schema has, so a rate added to the price book (or renamed)
// cannot be missing or misread here.
func TestPriceRatesMatchAPI(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "api", "openapi", "public.yaml"))
	if errors.Is(err, fs.ErrNotExist) {
		t.Skip("the API file is not here (survivalservers is checked out on its own)")
	}
	if err != nil {
		t.Fatal(err)
	}
	// The Prices schema's rates: the property names indented under "rates:" > "properties:".
	var api []string
	state := 0
	for _, line := range strings.Split(string(raw), "\n") {
		switch {
		case state == 0 && line == "    Prices:":
			state = 1
		case state == 1 && line == "        rates:":
			state = 2
		case state == 2 && line == "          properties:":
			state = 3
		case state == 3 && strings.HasPrefix(line, "            ") && !strings.HasPrefix(line, "             "):
			if name, _, ok := strings.Cut(strings.TrimSpace(line), ":"); ok {
				api = append(api, name)
			}
		case state == 3 && strings.HasPrefix(line, "              "):
		case state == 3:
			state = 4
		}
	}
	var ours []string
	for _, r := range priceRates {
		ours = append(ours, r.field)
	}
	slices.Sort(api)
	slices.Sort(ours)
	if len(api) == 0 || !slices.Equal(api, ours) {
		t.Errorf("survivalservers prices rates differ from the API file's Prices.rates\nsurvivalservers: %v\napi:   %v", ours, api)
	}
}

// Lists follow next_cursor to the end and --json prints one array of everything; a list cut at --limit says so on
// stderr (every list once stopped at its first page with no sign). An offer without its os list no longer crashes
// regions.
func TestListsReadEveryPage(t *testing.T) {
	f := newFake(t)
	f.on("GET /v0/apps/brawl/versions", func(r *http.Request, _ map[string]any) (int, any) {
		if r.URL.Query().Get("cursor") == "" {
			var rows []any
			for i := range 500 {
				rows = append(rows, map[string]any{"name": fmt.Sprintf("1.0.%d", i), "build": "bld_1"})
			}
			return 200, map[string]any{"data": rows, "next_cursor": "c2"}
		}
		return 200, map[string]any{"data": []any{map[string]any{"name": "2.0.0", "build": "bld_2"}}, "next_cursor": nil}
	})
	r := f.run(t, "versions", "list", "brawl", "--json")
	var rows []map[string]any
	if err := json.Unmarshal([]byte(r.stdout), &rows); err != nil || len(rows) != 501 || rows[500]["name"] != "2.0.0" {
		t.Fatalf("versions list --json: %d rows (%v), stderr %s", len(rows), err, r.stderr)
	}
	if calls := f.called("GET", "/v0/apps/brawl/versions"); len(calls) != 2 || calls[1].Query.Get("cursor") != "c2" || calls[0].Query.Get("limit") != "500" {
		t.Errorf("pages read: %+v", calls)
	}
	f.run(t, "versions", "list", "brawl").must(t, 0, "2.0.0")

	f.json("GET /v0/game-servers", 200, map[string]any{"data": []any{gs("gs_1", "running"), gs("gs_2", "running")}, "next_cursor": "more"})
	r = f.run(t, "game-servers", "list", "--limit", "2", "--json")
	if err := json.Unmarshal([]byte(r.stdout), &rows); err != nil || len(rows) != 2 || r.code != 0 ||
		!strings.Contains(r.stderr, "showing the first 2; more exist: raise --limit (now 2)") {
		t.Errorf("a list cut at --limit: exit %d, %d rows, stderr %q", r.code, len(rows), r.stderr)
	}
	if q := f.called("GET", "/v0/game-servers")[0].Query; q.Get("limit") != "2" {
		t.Errorf("a limit under one page asks for that many: %v", q)
	}

	f.json("GET /v0/regions", 200, map[string]any{"data": []any{map[string]any{"id": "us-central", "name": "US Central", "continent": "north-america", "status": "available",
		"offers": []any{map[string]any{"tier": "standard"}}}}, "next_cursor": nil})
	f.run(t, "regions").must(t, 0, "us-central", "standard")
}

// --help and -h work on every command and exit 0, and "help COMMAND" is the same; with --json an API error is JSON;
// a fault worth a retry has its own exit code; a key file others can read is warned about.
func TestHelpErrorsAndExitCodes(t *testing.T) {
	f := newFake(t)
	f.run(t, "game-servers", "start", "--help").must(t, 0, "usage: survivalservers game-servers start APP", "--wait")
	f.run(t, "game-servers", "start", "brawl", "-h").must(t, 0, "usage: survivalservers game-servers start")
	f.run(t, "game-servers", "--help").must(t, 0, "survivalservers game-servers ACTION: one of", "start", "--help shows")
	f.run(t, "help", "game-servers", "start").must(t, 0, "usage: survivalservers game-servers start")
	f.run(t, "help", "account").must(t, 0, "usage: survivalservers account")
	f.run(t, "help").must(t, 0, "persistent-servers backup ID | backups ID | restore ID", "4 the API was busy")
	f.run(t, "mcp", "--help").must(t, 0, "-http")
	f.run(t, "help", "nothing").must(t, 2, "no command")
	if n := len(f.called("POST", "/v0/game-servers")); n != 0 {
		t.Fatalf("asking for help started %d game servers", n)
	}

	f.json("GET /v0/studio", 401, map[string]any{"status": 401, "code": "unauthenticated", "detail": "The key is not known.", "request_id": "req_1"})
	r := f.run(t, "account", "--json")
	var problem map[string]any
	if err := json.Unmarshal([]byte(r.stderr), &problem); err != nil || r.code != 1 || problem["code"] != "unauthenticated" || problem["request_id"] != "req_1" ||
		!strings.Contains(fmt.Sprint(problem["next"]), "current key") {
		t.Errorf("an API error with --json: exit %d, stderr %q (%v)", r.code, r.stderr, err)
	}
	f.run(t, "account").must(t, 1, "unauthenticated")

	f.json("GET /v0/apps", 429, map[string]any{"status": 429, "code": "rate_limited", "detail": "Slow down."})
	f.run(t, "apps", "list").must(t, 4, "rate_limited")
	f.json("GET /v0/regions", 503, map[string]any{"status": 503, "code": "unavailable", "detail": "Briefly unavailable."})
	f.run(t, "regions").must(t, 4)
	f.json("GET /v0/continents", 422, map[string]any{"status": 422, "code": "validation_failed", "detail": "No."})
	f.run(t, "continents").must(t, 1)
	closed := httptest.NewServer(http.NotFoundHandler())
	gone := closed.URL
	closed.Close()
	f.runEnv(t, map[string]string{"GS_API_KEY": "gsk_live_EXAMPLE_not_a_real_key", "GS_API_URL": gone}, "account").must(t, 4)

	f.json("GET /v0/studio", 200, map[string]any{"name": "Kingsmoor"})
	keyFile := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(keyFile, []byte("gsk_live_EXAMPLE_not_a_real_key\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	vars := map[string]string{"GS_API_URL": f.srv.URL}
	if runtime.GOOS != "windows" {
		f.runEnv(t, vars, "account", "--key-file", keyFile).must(t, 0, "other users on this computer can read the key file")
		if err := os.Chmod(keyFile, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if r := f.runEnv(t, vars, "account", "--key-file", keyFile); r.code != 0 || strings.Contains(r.stderr, "warning") {
		t.Errorf("a key file only its owner reads: exit %d, stderr %q", r.code, r.stderr)
	}
}

// addressRecorder answers every call itself and keeps the address it was sent to: nothing leaves this computer.
type addressRecorder struct{ urls []string }

func (a *addressRecorder) RoundTrip(r *http.Request) (*http.Response, error) {
	a.urls = append(a.urls, r.URL.String())
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{"name":"Kingsmoor"}`)), Request: r}, nil
}

// The API address: --api, else GS_API_URL, else the public API's own address; a malformed one is refused and calls
// nothing.
func TestAPIAddress(t *testing.T) {
	run := func(vars map[string]string, args ...string) (result, []string) {
		t.Helper()
		rec := &addressRecorder{}
		var out, errb bytes.Buffer
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		vars["GS_API_KEY"] = "gsk_live_EXAMPLE_not_a_real_key"
		code := Run(ctx, args, Env{Stdout: &out, Stderr: &errb, Getenv: func(k string) string { return vars[k] },
			HTTP: &http.Client{Transport: rec}, Poll: 5 * time.Millisecond, Stdin: strings.NewReader("")})
		return result{code, out.String(), errb.String()}, rec.urls
	}
	r, urls := run(map[string]string{}, "account")
	r.must(t, 0, "Kingsmoor")
	if len(urls) != 1 || urls[0] != "https://studio.survivalservers.com/v0/studio" {
		t.Errorf("with no address set, calls went to %v, want the public API's own address", urls)
	}
	r, urls = run(map[string]string{"GS_API_URL": "https://staging.example.net"}, "account")
	r.must(t, 0)
	if len(urls) != 1 || !strings.HasPrefix(urls[0], "https://staging.example.net/v0/") {
		t.Errorf("GS_API_URL did not win: calls went to %v", urls)
	}
	r, urls = run(map[string]string{"GS_API_URL": "https://staging.example.net"}, "account", "--api", "https://other.example.net")
	r.must(t, 0)
	if len(urls) != 1 || !strings.HasPrefix(urls[0], "https://other.example.net/v0/") {
		t.Errorf("--api did not win over GS_API_URL: calls went to %v", urls)
	}
	r, urls = run(map[string]string{"GS_API_URL": "studio.survivalservers.com"}, "account")
	r.must(t, 1, "is not an http(s) address")
	if len(urls) != 0 {
		t.Errorf("a malformed address still called %v", urls)
	}
}

// builds upload sends a Windows build as only the chunks the studio's store lacks: the file list first, then a link
// asked for each missing chunk, then complete with {}. The same build again sends no chunk at all.
func TestBuildsUploadSendsOnlyTheChunksTheStudioLacks(t *testing.T) {
	f := newFake(t)
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "BrawlServer.zip")
	program := make([]byte, 6<<20)
	for i := range program {
		program[i] = byte(i*7919 + i/4093) // varied bytes, so the store cuts several chunks
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("WindowsServer/BrawlServer.exe")
	w.Write(program)
	zw.Close()
	os.WriteFile(zipPath, buf.Bytes(), 0o600)

	var mu sync.Mutex
	held, puts := map[string]bool{}, map[string]int{}
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		puts[r.URL.Path]++
		if name, ok := strings.CutPrefix(r.URL.Path, "/chunk/"); ok {
			if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != name {
				t.Errorf("chunk %s sent with other bytes", name)
			}
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("the key never goes to storage")
		}
	}))
	t.Cleanup(storage.Close)
	f.json("GET /v0/apps/brawl", 200, appWin)
	f.on("POST /v0/apps/brawl/builds", func(_ *http.Request, b map[string]any) (int, any) {
		if src, _ := b["source"].(map[string]any); src["chunks"] == nil {
			t.Errorf("the build was not sent as chunks: %v", b)
		}
		return 201, map[string]any{"id": "bld_c", "app": "app_w", "status": "awaiting_upload",
			"upload": map[string]any{"part_bytes": 0, "parts": []any{}, "list": map[string]any{"url": storage.URL + "/list"}}}
	})
	f.on("POST /v0/builds/bld_c/chunks", func(_ *http.Request, b map[string]any) (int, any) {
		mu.Lock()
		defer mu.Unlock()
		links := []any{}
		for _, c := range b["chunks"].([]any) {
			name := c.(map[string]any)["sha256"].(string)
			if !held[name] {
				links = append(links, map[string]any{"sha256": name, "url": storage.URL + "/chunk/" + name,
					"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
			}
		}
		return 200, map[string]any{"links": links}
	})
	f.on("POST /v0/builds/bld_c/complete", func(_ *http.Request, b map[string]any) (int, any) {
		if len(b) != 0 {
			t.Errorf("a chunk build completes with {}, got %v", b)
		}
		mu.Lock()
		defer mu.Unlock()
		for p := range puts {
			if name, ok := strings.CutPrefix(p, "/chunk/"); ok {
				held[name] = true
			}
		}
		return 202, map[string]any{"id": "bld_c", "app": "app_w", "status": "processing"}
	})

	r := f.runEnvWithStorage(t, storage.Client(), "builds", "upload", "brawl", zipPath)
	r.must(t, 0, "Build bld_c uploaded (processing).", "new chunks to send", "chunk 1 of")
	mu.Lock()
	sent := len(held)
	if puts["/list"] != 1 || sent < 2 {
		t.Errorf("the file list once and every chunk: list %d, chunks %d", puts["/list"], sent)
	}
	mu.Unlock()

	r = f.runEnvWithStorage(t, storage.Client(), "builds", "upload", "brawl", zipPath)
	r.must(t, 0, "Build bld_c uploaded (processing).")
	mu.Lock()
	defer mu.Unlock()
	for p, n := range puts {
		if strings.HasPrefix(p, "/chunk/") && n != 1 {
			t.Errorf("the same build again sent %s %d times", p, n)
		}
	}
	if puts["/list"] != 2 || len(f.called("POST", "/v0/builds/bld_c/complete")) != 2 {
		t.Errorf("the second upload sends its list and completes: list %d", puts["/list"])
	}
}
