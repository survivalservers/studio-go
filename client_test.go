package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestCheckVersionOnTheSamplesSettings(t *testing.T) {
	// The samples' own version files, as a person would pass them to the command-line tool.
	for _, tc := range []struct{ file, os, exe string }{
		{"../samples/plain/version-windows.json", "windows", "PlainServer.exe"},
		{"../samples/plain/version-linux.json", "linux", ""},
		{"../samples/godot/version-linux.json", "linux", ""},
	} {
		data, err := os.ReadFile(tc.file)
		if os.IsNotExist(err) {
			t.Skip("the samples are not next to this module")
		} else if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatal(err)
		}
		body["build"] = "bld_1"
		app := map[string]any{"id": "app_1", "os": tc.os, "tier": "standard"}
		build := map[string]any{"id": "bld_1", "app": "app_1", "status": "ready", "executables": []any{tc.exe}}
		if tc.exe == "" {
			delete(build, "executables")
		}
		if p := CheckVersion(body, app, build); len(p) > 0 {
			t.Errorf("%s: %v", tc.file, p)
		}
	}
}

func TestCheckVersionSaysWhatToChange(t *testing.T) {
	app := map[string]any{"id": "app_1", "slug": "brawl", "os": "windows", "tier": "high-frequency"}
	build := map[string]any{"id": "bld_1", "app": "app_2", "status": "processing", "executables": []any{"Server.exe"}}
	body := map[string]any{"build": "bld_1", "size": map[string]any{"cpu": 1.5, "memory_gb": 9.0},
		"launch": map[string]any{"executable": "Game.exe", "args": []any{"-q={{port.query}}", "-p={{port.game.internal}}"},
			"env":         map[string]any{"GS_TOKEN": "x"},
			"ports":       []any{map[string]any{"name": "game", "protocol": "udp", "container_port": 7777.0}, map[string]any{"name": "game", "protocol": "quic"}},
			"ready_check": map[string]any{"type": "port", "port": "query"}, "stop": map[string]any{"method": "sigterm"}}}
	got := strings.Join(CheckVersion(body, app, build), "\n")
	for _, want := range []string{"belongs to another app", "still processing", "whole vCPUs", "0.5 to 4 GB per vCPU", `port "game" is named twice`,
		"protocol must be udp or tcp", "container_port is for Linux images", `names port "query"`, ".internal ports are for Linux",
		"GS_TOKEN", `"Game.exe" is not in the zip (programs in this build: Server.exe)`, "a port ready check needs port", `"sigterm" is for Linux`} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q in:\n%s", want, got)
		}
	}
}

// A passthrough port is a Linux public port with no container_port: the game listens on its public number.
func TestCheckVersionPassthroughPorts(t *testing.T) {
	body := func(ports ...any) map[string]any {
		return map[string]any{"build": "bld_1", "size": map[string]any{"cpu": 1.0, "memory_gb": 1.0},
			"launch": map[string]any{"args": []any{"+server.port", "{{port.game}}"}, "ports": ports}}
	}
	linux := map[string]any{"id": "app_1", "os": "linux", "tier": "standard"}
	windows := map[string]any{"id": "app_1", "os": "windows", "tier": "standard"}
	build := map[string]any{"id": "bld_1", "app": "app_1", "status": "ready"}
	same := map[string]any{"name": "game", "protocol": "udp", "port_policy": "passthrough"}
	if p := CheckVersion(body(same, map[string]any{"name": "rcon", "protocol": "tcp", "container_port": 28016.0, "port_policy": "none"}), linux, build); len(p) > 0 {
		t.Errorf("a Linux passthrough port: %v", p)
	}
	withPort := map[string]any{"name": "game", "protocol": "udp", "port_policy": "passthrough", "container_port": 28015.0}
	if got := strings.Join(CheckVersion(body(withPort), linux, build), "\n"); !strings.Contains(got, "a passthrough port has no container_port") {
		t.Errorf("passthrough with a container port: %s", got)
	}
	windowsBody := body(same)
	windowsBody["launch"].(map[string]any)["executable"] = "Server.exe"
	if got := strings.Join(CheckVersion(windowsBody, windows, build), "\n"); !strings.Contains(got, "passthrough is for Linux images") {
		t.Errorf("passthrough on Windows: %s", got)
	}
	if got := strings.Join(CheckVersion(body(map[string]any{"name": "game", "protocol": "udp", "port_policy": "static"}), linux, build), "\n"); !strings.Contains(got, "or passthrough") {
		t.Errorf("an unknown policy: %s", got)
	}
}

func TestWaitBacksOffAndStops(t *testing.T) {
	n := 0
	var states []string
	obj, ok, err := Wait(context.Background(), time.Second, 2*time.Millisecond, func(context.Context) (map[string]any, error) {
		n++
		if n < 4 {
			return map[string]any{"status": "processing"}, nil
		}
		return map[string]any{"status": "ready"}, nil
	}, BuildSettled, func(_ time.Duration, s string) { states = append(states, s) })
	if err != nil || !ok || obj["status"] != "ready" || len(states) != 3 {
		t.Errorf("%v %v %v %v", obj, ok, err, states)
	}
	_, ok, err = Wait(context.Background(), 30*time.Millisecond, 5*time.Millisecond, func(context.Context) (map[string]any, error) {
		return map[string]any{"status": "starting"}, nil
	}, GameServerSettled, nil)
	if ok || err != nil {
		t.Error("a wait that runs out returns the last state, not settled")
	}
	boom := errors.New("boom")
	if _, _, err := Wait(context.Background(), time.Second, time.Millisecond, func(context.Context) (map[string]any, error) { return nil, boom }, BuildSettled, nil); !errors.Is(err, boom) {
		t.Error("a read error ends the wait")
	}
	if ok, s := VersionSettled(map[string]any{"regions": []any{map[string]any{"region": "uk", "status": "failed"}, map[string]any{"region": "us-east", "status": "failed"}}}); !ok || s != "0 of 2 regions ready" {
		t.Errorf("failed everywhere is settled: %v %s", ok, s)
	}
	// A version the machines removed is not waited for: nothing is on its way.
	removed := map[string]any{"regions": []any{map[string]any{"region": "uk", "status": "removed"}, map[string]any{"region": "us-east", "status": "failed"}}}
	if ok, _ := VersionSettled(removed); !ok || VersionRemoved(removed) != 1 {
		t.Errorf("removed or failed everywhere is settled: %v, %d removed", ok, VersionRemoved(removed))
	}
	if ok, _ := VersionSettled(map[string]any{"regions": []any{map[string]any{"region": "uk", "status": "removed"}, map[string]any{"region": "us-east", "status": "copying"}}}); ok {
		t.Error("a region still copying is waited for")
	}
	v := map[string]any{"regions": []any{map[string]any{"region": "uk", "status": "ready"}, map[string]any{"region": "us-east", "status": "staging"}}}
	if ready, failed, all := VersionRegions(v); ready != 1 || failed != 0 || all != 2 {
		t.Errorf("regions counted: %d ready, %d failed, of %d", ready, failed, all)
	}
	for st, want := range map[string]bool{"running": true, "stopped": true, "paused": true, "failed": true, "starting": false, "resuming": false, "stopping": false, "moving": false} {
		if ok, s := PersistentServerSettled(map[string]any{"status": st}); ok != want || s != "persistent server "+st {
			t.Errorf("persistent server %s: settled %v (%s)", st, ok, s)
		}
	}
}

func TestDownloadLinksStreamsAndProblems(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v0/json":
			_ = json.NewEncoder(w).Encode(map[string]any{"url": "https://files.example.com/a", "expires_at": "x"})
		case "/v0/redirect":
			w.Header().Set("Location", "https://files.example.com/b")
			w.WriteHeader(303)
		case "/v0/stream":
			_, _ = w.Write([]byte("hello\n"))
		case "/v0/full":
			w.WriteHeader(503)
			_, _ = w.Write([]byte(`{"status":503,"code":"capacity_unavailable","regions_tried":[{"region":"uk","reason":"no_capacity"}]}`))
		default:
			w.Header().Set("Retry-After", "2")
			w.WriteHeader(429)
			_, _ = w.Write([]byte(`{"status":429,"code":"rate_limited","detail":"Slow down.","request_id":"req_1"}`))
		}
	}))
	defer srv.Close()
	c, err := New(srv.URL+"/v0/", "gsk_live_EXAMPLE_not_a_real_key", nil, "test")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if l, err := c.DownloadLink(ctx, "/v0/json", nil); err != nil || l != "https://files.example.com/a" {
		t.Errorf("JSON link: %s %v", l, err)
	}
	if l, err := c.DownloadLink(ctx, "/v0/redirect", nil); err != nil || l != "https://files.example.com/b" {
		t.Errorf("redirect link: %s %v", l, err)
	}
	body, err := c.Stream(ctx, Call{Method: "GET", Path: "/v0/stream"})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(body)
	body.Close()
	if string(data) != "hello\n" {
		t.Errorf("stream %q", data)
	}
	c.SetRetry(1, 0) // trying again is TestTriesAgainOn429And503Unavailable's
	_, err = c.Do(ctx, Call{Method: "POST", Path: "/v0/other"})
	var p *Problem
	if !errors.As(err, &p) || p.RetryAfter != "2" || !strings.Contains(err.Error(), "Retry after 2 seconds") || !strings.Contains(err.Error(), "req_1") {
		t.Errorf("problem: %v", err)
	}
	_, err = c.Do(ctx, Call{Method: "POST", Path: "/v0/full"})
	if !errors.As(err, &p) || len(p.RegionsTried) != 1 || !strings.Contains(err.Error(), `Regions tried: {"region":"uk","reason":"no_capacity"}`) ||
		!strings.Contains(err.Error(), "Next: No capacity in any region tried") {
		t.Errorf("capacity problem: %v", err)
	}
	if _, err := New("ftp://x", "k", nil, ""); err == nil {
		t.Error("only http(s) addresses")
	}
	if _, err := (&Client{}).Do(ctx, Call{}); !errors.Is(err, ErrNoKey) {
		t.Error("no key, no call")
	}
}

func TestUploadZipRepeatedFindsTheFirstBuild(t *testing.T) {
	zip := filepath.Join(t.TempDir(), "s.zip")
	os.WriteFile(zip, []byte("PK zip bytes"), 0o600)
	var completes int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/builds"):
			if r.Header.Get("Idempotency-Key") != "ci-1" {
				t.Errorf("key %q", r.Header.Get("Idempotency-Key"))
			}
			w.Header().Set("Idempotent-Replayed", "true")
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "bld_1", "status": "awaiting_upload"})
		case r.Method == "GET" && r.URL.Path == "/v0/builds/bld_1":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "bld_1", "status": "ready"})
		default:
			completes++
		}
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "gsk_live_EXAMPLE_not_a_real_key", nil, "")
	b, err := c.UploadZip(context.Background(), "brawl", zip, UploadOptions{IdempotencyKey: "ci-1"})
	if err != nil || b["status"] != "ready" || completes != 0 {
		t.Errorf("a re-run of a finished upload returns the build and uploads nothing: %v %v %d", b, err, completes)
	}
}

// A test always names the address it calls: with no address, New refuses inside a test binary, and an
// address the test names is used as given.
func TestNoDefaultAddressInTests(t *testing.T) {
	if _, err := New("", "gsk_live_EXAMPLE_not_a_real_key", nil, ""); !errors.Is(err, ErrNoAddressInTests) {
		t.Fatalf("New with no address in a test: %v, want ErrNoAddressInTests", err)
	}
	c, err := New("http://127.0.0.1:1/v0", "gsk_live_EXAMPLE_not_a_real_key", nil, "")
	if err != nil || c.Base() != "http://127.0.0.1:1" {
		t.Fatalf("New with an address: %v, base %q", err, c.Base())
	}
}

// A call is tried again, up to 3 times in all, on 429 (any call: the API did nothing) and on 503 unavailable (a
// read, or a write with its Idempotency-Key, the same key on every try), waiting Retry-After up to the cap. Never on
// 503 capacity_unavailable, and never past the cap: those answers come back at once.
func TestTriesAgainOn429And503Unavailable(t *testing.T) {
	type answer struct {
		status int
		code   string
		after  string
	}
	var script []answer
	var keys, bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if len(script) == 0 {
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		a := script[0]
		script = script[1:]
		if a.after != "" {
			w.Header().Set("Retry-After", a.after)
		}
		w.WriteHeader(a.status)
		_, _ = fmt.Fprintf(w, `{"status":%d,"code":%q}`, a.status, a.code)
	}))
	defer srv.Close()
	c, err := New(srv.URL, "gsk_live_EXAMPLE_not_a_real_key", nil, "test")
	if err != nil {
		t.Fatal(err)
	}
	var slept []time.Duration
	c.sleep = func(_ context.Context, d time.Duration) error { slept = append(slept, d); return nil }
	ctx := context.Background()
	run := func(name string, call Call, answers []answer, wantCalls int, wantOK bool, wantSlept ...time.Duration) {
		t.Helper()
		script, keys, bodies, slept = answers, nil, nil, nil
		_, err := c.Do(ctx, call)
		if (err == nil) != wantOK || len(keys) != wantCalls || fmt.Sprint(slept) != fmt.Sprint(wantSlept) {
			t.Errorf("%s: err %v, %d calls (want %d), slept %v (want %v)", name, err, len(keys), wantCalls, slept, wantSlept)
		}
		for i := range keys {
			if keys[i] != keys[0] || bodies[i] != bodies[0] {
				t.Errorf("%s: try %d sent key %q and body %q, the first sent %q and %q", name, i+1, keys[i], bodies[i], keys[0], bodies[0])
			}
		}
	}
	start := Call{Method: "POST", Path: "/v0/game-servers", Body: map[string]any{"app": "brawl"}}
	run("429 then done", start, []answer{{429, "rate_limited", "2"}}, 2, true, 2*time.Second)
	run("503 unavailable twice, no Retry-After", start, []answer{{503, "unavailable", ""}, {503, "unavailable", ""}}, 3, true, time.Second, 2*time.Second)
	run("429 three times: given up after 3 tries", start, []answer{{429, "rate_limited", "1"}, {429, "rate_limited", "1"}, {429, "rate_limited", "1"}}, 3, false, time.Second, time.Second)
	run("capacity_unavailable is the caller's call", start, []answer{{503, "capacity_unavailable", "5"}}, 1, false)
	run("a wait over the cap comes back at once", start, []answer{{429, "rate_limited", "120"}}, 1, false)
	run("other errors are not tried again", start, []answer{{500, "internal", ""}}, 1, false)
	run("a read on 503 unavailable", Call{Method: "GET", Path: "/v0/apps"}, []answer{{503, "unavailable", "0"}}, 2, true, time.Duration(0))
	if keys[0] != "" {
		t.Errorf("a read sent an Idempotency-Key %q", keys[0])
	}
	run("the caller's own key on every try", Call{Method: "PATCH", Path: "/v0/apps/brawl", Body: map[string]any{"name": "B"}, IdempotencyKey: "match-42"},
		[]answer{{503, "unavailable", "1"}}, 2, true, time.Second)
	if keys[0] != "match-42" {
		t.Errorf("the caller's key was not sent: %q", keys[0])
	}
	if !again("POST", false, &Problem{Status: 429}) || again("POST", false, &Problem{Status: 503, Code: "unavailable"}) ||
		!again("DELETE", false, &Problem{Status: 503, Code: "unavailable"}) {
		t.Error("a write without an Idempotency-Key is tried again on 429 only")
	}
	c.SetRetry(1, time.Minute)
	run("SetRetry(1) never tries again", start, []answer{{429, "rate_limited", "1"}}, 1, false)
	c.SetRetry(DefaultTries, DefaultMaxWait)
	// A caller that gives up during the wait gets the API's answer, and nothing more is sent.
	leaving, leave := context.WithCancel(ctx)
	c.sleep = func(ctx context.Context, d time.Duration) error { leave(); return sleepCtx(ctx, d) }
	script, keys = []answer{{429, "rate_limited", "1"}}, nil
	var p *Problem
	if _, err := c.Do(leaving, start); !errors.As(err, &p) || p.Status != 429 || len(keys) != 1 {
		t.Errorf("a caller that left during the wait: %v after %d calls", err, len(keys))
	}
}

// A Windows program path is matched as the API matches it: backslashes as slashes, letter case exact (the
// Windows way of writing it was once refused here though the API takes it).
func TestCheckVersionReadsTheProgramPathAsTheAPIDoes(t *testing.T) {
	app := map[string]any{"id": "app_1", "os": "windows", "tier": "standard"}
	build := map[string]any{"id": "bld_1", "app": "app_1", "status": "ready", "executables": []any{"Binaries/Win64/BreakwireServer.exe"}}
	version := func(exe string) map[string]any {
		return map[string]any{"name": "1.0.0", "build": "bld_1", "size": map[string]any{"cpu": 2, "memory_gb": 4},
			"launch": map[string]any{"executable": exe, "ports": []any{map[string]any{"name": "game", "protocol": "udp"}}}}
	}
	for _, exe := range []string{`Binaries\Win64\BreakwireServer.exe`, "Binaries/Win64/BreakwireServer.exe"} {
		for _, p := range CheckVersion(version(exe), app, build) {
			if strings.Contains(p, "executable") {
				t.Errorf("%s: %s", exe, p)
			}
		}
	}
	found := false
	for _, p := range CheckVersion(version(`binaries\win64\breakwireserver.exe`), app, build) {
		found = found || strings.Contains(p, `not in the zip: letter case counts, and the build has "Binaries/Win64/BreakwireServer.exe"`)
	}
	if !found {
		t.Errorf("a path in the wrong case should be refused here, as the API refuses it: %v", CheckVersion(version(`binaries\win64\breakwireserver.exe`), app, build))
	}
}

// The API's three refusals of a Windows program, here before sending and in the API's own
// words: a name that is not a plain path inside the build, a build that lists no program, and a script.
func TestCheckVersionRefusesWhatTheAPIRefusesForAProgram(t *testing.T) {
	app := map[string]any{"id": "app_1", "os": "windows", "tier": "standard"}
	build := func(exes ...any) map[string]any {
		return map[string]any{"id": "bld_1", "app": "app_1", "status": "ready", "executables": exes}
	}
	version := func(exe string) map[string]any {
		return map[string]any{"name": "1.0.0", "build": "bld_1", "size": map[string]any{"cpu": 2, "memory_gb": 4},
			"launch": map[string]any{"executable": exe, "ports": []any{map[string]any{"name": "game", "protocol": "udp"}}}}
	}
	says := func(exe string, b map[string]any, want string) {
		t.Helper()
		for _, p := range CheckVersion(version(exe), app, b) {
			if p == "launch.executable: "+want {
				return
			}
		}
		t.Errorf("%s: want %q, got %v", exe, want, CheckVersion(version(exe), app, b))
	}
	good := build("WindowsServer/Game.exe", "WindowsServer/start.bat")
	for _, exe := range []string{"C:/Games/Game.exe", `C:\Games\Game.exe`, "/WindowsServer/Game.exe", "../Game.exe",
		"WindowsServer/../Game.exe", "WindowsServer/./Game.exe", "WindowsServer//Game.exe", "Game.exe:stream"} {
		says(exe, good, programNotLocal)
	}
	says("WindowsServer/Game.exe", build(), programNoneInBuild)
	for _, exe := range []string{"WindowsServer/start.bat", "run.cmd", "Tools/start.ps1", "WindowsServer/Game"} {
		says(exe, good, programNotExe)
	}

	// A good program passes, written either way; a build not fetched or not ready yet is not called empty here.
	for _, exe := range []string{"WindowsServer/Game.exe", `WindowsServer\Game.exe`} {
		for _, p := range CheckVersion(version(exe), app, good) {
			if strings.Contains(p, "executable") {
				t.Errorf("%s: %s", exe, p)
			}
		}
	}
	for _, b := range []map[string]any{nil, {"id": "bld_1", "app": "app_1", "status": "processing"}} {
		for _, p := range CheckVersion(version("WindowsServer/Game.exe"), app, b) {
			if strings.Contains(p, programNoneInBuild) {
				t.Errorf("build %v: %s", b, p)
			}
		}
	}
}

// Every error code the API file lists has a next step, so a new code never reaches a caller with no advice.
// The API file is beside this module only in our own repository: elsewhere the test has nothing to compare.
func TestEveryErrorCodeHasANextStep(t *testing.T) {
	raw, err := os.ReadFile("../../api/openapi/public.yaml")
	if err != nil {
		t.Skip("the API file is not here:", err)
	}
	var codes []string
	in, list := false, false
	for _, line := range strings.Split(string(raw), "\n") {
		switch {
		case line == "    ErrorCode:":
			in = true
		case in && line == "      enum:":
			list = true
		case list && strings.HasPrefix(line, "        - "):
			codes = append(codes, strings.TrimSpace(strings.TrimPrefix(line, "        - ")))
		case list:
			in, list = false, false
		}
	}
	if len(codes) < 30 {
		t.Fatalf("read %d error codes from the API file: its ErrorCode list moved", len(codes))
	}
	for _, c := range codes {
		if NextStep[c] == "" {
			t.Errorf("no next step for the API's error code %q", c)
		}
	}
	for c, step := range NextStep {
		if !slices.Contains(codes, c) {
			t.Errorf("a next step for %q, which the API file does not list", c)
		}
		// The client is offered to studio backends: the AI tool adds its own lines (oss/mcp/server/schema.go).
		for _, ai := range []string{"chat", "the developer", "wait_for_", "list_"} {
			if strings.Contains(step, ai) {
				t.Errorf("the next step for %q talks to an AI tool (%q): %s", c, ai, step)
			}
		}
	}
	// card_required comes for no card on file and for a payment 14 days overdue (card first); the API's own sentence,
	// shown before the step, says which. So the step covers both and guesses neither: no "No card is on file", nothing
	// of free hours used up.
	if step := NextStep["card_required"]; !strings.Contains(step, "Add a card") || !strings.Contains(step, "overdue invoice") ||
		strings.Contains(step, "No card is on file") || strings.Contains(step, "used up") {
		t.Errorf("the next step for card_required: %s", step)
	}
	if strings.Contains(ErrNoKey.Error(), "chat") || !strings.HasPrefix(NewIdempotencyKey(), "idem-") {
		t.Errorf("the no-key error or the key prefix is written for the AI tool: %v, %s", ErrNoKey, NewIdempotencyKey())
	}
}

// Every call says which client is calling, and the default HTTP client waits longer than the API's longest wait of
// 60 seconds.
func TestDefaultsSayWhoIsCallingAndOutwaitTheLongestWait(t *testing.T) {
	var ua string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua = r.UserAgent()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()
	c, err := New(srv.URL, "gsk_live_EXAMPLE_not_a_real_key", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Get(context.Background(), "/v0/regions", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ua, "studio-go") || c.http.Timeout != AnswerTimeout || AnswerTimeout <= 60*time.Second {
		t.Errorf("user agent %q, timeout %v", ua, c.http.Timeout)
	}
	if c, _ := New(srv.URL, "gsk_live_EXAMPLE_not_a_real_key", nil, "survivalservers-cli/1.2"); c.userAgent != "survivalservers-cli/1.2" {
		t.Errorf("a caller's own user agent is kept: %q", c.userAgent)
	}
}
