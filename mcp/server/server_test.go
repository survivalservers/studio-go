package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	api "github.com/survivalservers/studio-go"
)

const key = "gsk_live_EXAMPLE_not_a_real_key"

func TestToolLists(t *testing.T) {
	f := newFakeAPI(t)
	local := toolNames(t, f.session(t, Config{Key: key, Local: true}))
	hosted := toolNames(t, f.session(t, Config{Key: key}))
	readOnly := toolNames(t, f.session(t, Config{Key: key, Local: true, ReadOnly: true}))

	for _, name := range []string{"create_app", "upload_windows_build", "create_linux_build", "get_registry_login", "create_version",
		"set_branch", "start_game_server", "stop_game_server", "diagnose_game_server", "read_game_server_log",
		"list_game_server_files", "read_game_server_file", "create_persistent_server", "list_persistent_servers", "wait_for_persistent_server",
		"start_persistent_server", "stop_persistent_server", "update_persistent_server", "plan_rollback", "roll_back", "undo_bulk_action", "download_backup", "back_up_persistent_server",
		"list_backups", "set_pool", "list_pools", "remove_pool", "list_regions", "get_usage", "estimate_cost", "setup_guide", "search_docs"} {
		if local[name] == nil {
			t.Errorf("local server lacks %s", name)
		}
	}
	if hosted["upload_windows_build"] != nil || hosted["begin_windows_upload"] == nil || hosted["complete_windows_upload"] == nil {
		t.Error("the hosted server must offer the scripted upload instead of reading local files")
	}
	if local["begin_windows_upload"] != nil {
		t.Error("the local server uploads itself")
	}
	if len(local) > 52 {
		t.Errorf("%d tools: too many for an agent to choose well", len(local))
	}
	for name, tl := range local {
		if tl.Description == "" || tl.Annotations == nil {
			t.Errorf("%s has no description or hints", name)
		}
		schema, _ := json.Marshal(tl.InputSchema)
		if !strings.Contains(string(schema), `"type":"object"`) {
			t.Errorf("%s: input schema is not an object: %s", name, schema)
		}
		if ro := readOnly[name] != nil; ro != tl.Annotations.ReadOnlyHint {
			t.Errorf("%s: read-only server lists it: %v, but its read-only hint is %v", name, ro, tl.Annotations.ReadOnlyHint)
		}
	}
	// Every tool that stops, restarts, deletes or overwrites is marked destructive, as the docs say; the rest are not.
	destructive := map[string]bool{"stop_game_server": true, "stop_persistent_server": true, "roll_back": true, "undo_bulk_action": true,
		"cancel_bulk_action": true, "remove_pool": true, "run_bulk_action": true, "send_command": true, "set_game_profile": true,
		"update_persistent_server": true, "set_branch": true}
	for name := range destructive {
		if local[name] == nil {
			t.Errorf("local server lacks %s", name)
		}
	}
	for name, tl := range local {
		if d := tl.Annotations.DestructiveHint; (d != nil && *d) != destructive[name] {
			t.Errorf("%s: destructive mark %v, want %v", name, d != nil && *d, destructive[name])
		}
	}
	// Enums reach the schema.
	schema, _ := json.Marshal(local["create_version"].InputSchema)
	for _, want := range []string{`"ctrl_break"`, `"local_api"`, `"on_failure"`} {
		if !strings.Contains(string(schema), want) {
			t.Errorf("create_version schema lacks %s", want)
		}
	}
	schema, _ = json.Marshal(local["create_persistent_server"].InputSchema)
	for _, want := range []string{`"when_empty"`, `"pause"`, `"stop_after"`, `"backup"`, `"region"`} {
		if !strings.Contains(string(schema), want) {
			t.Errorf("create_persistent_server schema lacks %s", want)
		}
	}
	// The backup kinds, in their words, with Continuous's line as the panel and docs say it.
	backups := local["list_backups"].Description
	for _, want := range []string{"Continuous (continuous): Copied within seconds of each game save; the newest 3 are kept, apart from your other backups.",
		"Scheduled (scheduled)", "At stop (stop)", "By hand (manual)", "Recovered (recovered)"} {
		if !strings.Contains(backups, want) {
			t.Errorf("list_backups description lacks %q: %s", want, backups)
		}
	}
	// The old words are gone from everything an agent reads (tool names, titles, descriptions, inputs, instructions).
	old := regexp.MustCompile(`(?i)\bworlds?\b|\bcit(y|ies)\b|warm pool|snapshot|\bsleep|asleep|(^|[^v])CPU-hour|local interface|fleet`)
	var read []string
	for name, tl := range local {
		schema, _ := json.Marshal(tl.InputSchema)
		read = append(read, name, tl.Title, tl.Description, string(schema))
	}
	for name, tl := range hosted {
		read = append(read, name, tl.Description)
	}
	read = append(read, f.session(t, Config{Key: key, Local: true}).InitializeResult().Instructions)
	for _, text := range read {
		if m := old.FindString(text); m != "" {
			t.Errorf("old word %q in: %.300s", m, text)
		}
	}
}

func TestInstructionsCarryTheRules(t *testing.T) {
	f := newFakeAPI(t)
	for _, cfg := range []Config{{Key: key, Local: true}, {Key: key}, {Key: key, ReadOnly: true, Apps: []string{"brinewatch-arena"}, MaxTTL: time.Hour}} {
		text := f.session(t, cfg).InitializeResult().Instructions
		if !strings.Contains(text, api.SkillRules()) {
			t.Errorf("the instructions lack the rules file's rules: %s", text)
		}
		// Claude Code cuts a server's instructions at 4,096 characters, and at 2,048 before v2.1.296.
		if len(text) > 2048 {
			t.Errorf("the instructions are %d bytes: %s", len(text), text)
		}
	}
}

func TestKeyHeadersAndIdempotency(t *testing.T) {
	f := newFakeAPI(t)
	f.on("POST /v0/apps", func(_ *http.Request, body map[string]any) (int, any) {
		app := map[string]any{"id": "app_new", "slug": body["slug"], "name": body["name"], "os": body["os"], "tier": "standard"}
		return 201, app
	})
	cs := f.session(t, Config{Key: key, Local: true})
	text := mustOK(t, cs, "create_app", map[string]any{"name": "Brawl Arena 2!", "os": "linux", "regions": []string{"europe"}})
	if !strings.Contains(text, "brawl-arena-2") {
		t.Errorf("the slug should come from the name: %s", text)
	}
	c := f.called("POST", "/v0/apps")[0]
	if c.Auth != "Bearer "+key || !strings.HasPrefix(c.Idem, "idem-") {
		t.Errorf("auth %q, idempotency key %q", c.Auth, c.Idem)
	}
	if c.Body["slug"] != "brawl-arena-2" || c.Body["os"] != "linux" {
		t.Errorf("body %v", c.Body)
	}
	mustFail(t, cs, "create_app", map[string]any{"name": "x", "os": "linux"}, "slug")
}

func TestNoKey(t *testing.T) {
	f := newFakeAPI(t)
	cs := f.session(t, Config{Local: true})
	mustFail(t, cs, "list_apps", nil, "GS_API_KEY", "never pass it as an argument")
	if len(f.calls) != 0 {
		t.Error("no call may go out without a key")
	}
}

func TestErrorsSayWhatToDo(t *testing.T) {
	f := newFakeAPI(t)
	f.json("GET /v0/apps/brawl", 200, appBrawl)
	f.json("GET /v0/builds/bld_1", 200, map[string]any{"id": "bld_1", "app": "app_brawl", "status": "ready", "executables": []any{"Server.exe"}})
	f.json("POST /v0/apps/brawl/versions", 422, map[string]any{"status": 422, "code": "validation_failed", "detail": "Some fields are not valid.",
		"errors": []any{map[string]any{"field": "size.cpu", "message": "over the studio's limit"}}, "request_id": "req_42"})
	f.on("GET /v0/apps", func(*http.Request, map[string]any) (int, any) {
		return 403, map[string]any{"status": 403, "code": "missing_scope", "detail": "This call needs apps:read."}
	})
	cs := f.session(t, Config{Key: key, Local: true})
	mustFail(t, cs, "create_version", map[string]any{"app": "brawl", "name": "1.0.0", "build": "bld_1", "cpu": 1, "memory_gb": 1,
		"executable": "Server.exe", "ports": []any{map[string]any{"name": "game", "protocol": "udp"}}, "ready_check": map[string]any{"type": "local_api"}},
		"validation_failed", "size.cpu: over the studio's limit", "Next: Some fields", "req_42")
	mustFail(t, cs, "list_apps", nil, "missing_scope", "apps:read", "Next: The API key lacks a scope",
		"For the AI tool: ask the developer", "never ask them to paste it into the chat")
}

func TestVersionChecks(t *testing.T) {
	f := newFakeAPI(t)
	f.json("GET /v0/apps/brawl", 200, appBrawl)
	f.json("GET /v0/builds/bld_ready", 200, map[string]any{"id": "bld_ready", "app": "app_brawl", "status": "ready",
		"executables": []any{"WindowsServer/BrawlServer.exe", "WindowsServer/CrashReporter.exe"}})
	f.json("GET /v0/builds/bld_busy", 200, map[string]any{"id": "bld_busy", "app": "app_brawl", "status": "processing"})
	cs := f.session(t, Config{Key: key, Local: true})
	base := func(over map[string]any) map[string]any {
		args := map[string]any{"app": "brawl", "name": "1.0.0", "build": "bld_ready", "cpu": 2, "memory_gb": 4,
			"executable": "WindowsServer/BrawlServer.exe", "args": []any{"-port={{port.game}}"},
			"ports": []any{map[string]any{"name": "game", "protocol": "udp"}}, "ready_check": map[string]any{"type": "local_api"}}
		for k, v := range over {
			args[k] = v
		}
		return args
	}
	mustFail(t, cs, "create_version", base(map[string]any{"build": "bld_busy"}), "still processing")
	mustFail(t, cs, "create_version", base(map[string]any{"executable": "Server.exe"}), `"Server.exe" is not in the zip`, "WindowsServer/BrawlServer.exe")
	mustFail(t, cs, "create_version", base(map[string]any{"stop_method": "sigterm"}), "is for Linux", "ctrl_break")
	mustFail(t, cs, "create_version", base(map[string]any{"env": map[string]any{"GS_PORT_GAME": "1"}}), "names starting with GS_ are ours")
	mustFail(t, cs, "create_version", base(map[string]any{"cpu": 0.3}), "0.25 vCPU steps")
	mustFail(t, cs, "create_version", base(map[string]any{"memory_gb": 16}), "0.5 to 4 GB per vCPU")
	mustFail(t, cs, "create_version", base(map[string]any{"args": []any{"-query={{port.query}}"}}), `names port "query"`)
	mustFail(t, cs, "create_version", base(map[string]any{"ready_check": map[string]any{"type": "log"}}), "needs pattern")
	mustFail(t, cs, "create_version", base(map[string]any{"ports": []any{map[string]any{"name": "game", "protocol": "udp", "port_policy": "static"}}}), "port_policy must be dynamic")
	mustFail(t, cs, "create_version", base(map[string]any{"ports": []any{map[string]any{"name": "game", "protocol": "udp", "port_policy": "passthrough"}}}), "passthrough is for Linux images")
	text := mustOK(t, cs, "create_version", base(map[string]any{"dry_run": true, "stop_method": "ctrl_break", "stop_shutdown_time": "20s", "compat": []any{"playfab"},
		"ports": []any{map[string]any{"name": "game", "protocol": "udp"}, map[string]any{"name": "rcon", "protocol": "tcp", "port_policy": "none"}}}))
	if !strings.Contains(text, `"method": "ctrl_break"`) || !strings.Contains(text, `"compat": [`) || !strings.Contains(text, `"port_policy": "none"`) ||
		len(f.called("POST", "/v0/apps/brawl/versions")) != 0 {
		t.Errorf("a dry run shows the body and sends nothing: %s", text)
	}
	f.on("POST /v0/apps/brawl/versions", func(_ *http.Request, body map[string]any) (int, any) {
		return 201, map[string]any{"name": body["name"], "regions": []any{map[string]any{"region": "us-central", "status": "pending"}, map[string]any{"region": "us-east", "status": "pending"}}}
	})
	if text := mustOK(t, cs, "create_version", base(map[string]any{"regions": []any{"us-central", "us-east"}})); !strings.Contains(text, "copying to 2 regions") {
		t.Errorf("create_version: %s", text)
	}
	sent := f.called("POST", "/v0/apps/brawl/versions")[0].Body
	launch := sent["launch"].(map[string]any)
	if launch["executable"] != "WindowsServer/BrawlServer.exe" || sent["size"].(map[string]any)["cpu"] != 2.0 || fmt.Sprint(sent["regions"]) != "[us-central us-east]" {
		t.Errorf("version body %v", sent)
	}
	// Ready in one region of two: settled, and said per region.
	f.json("GET /v0/apps/brawl/versions/1.0.0", 200, map[string]any{"name": "1.0.0", "regions": []any{
		map[string]any{"region": "us-central", "status": "ready"}, map[string]any{"region": "us-east", "status": "copying"}}})
	if text := mustOK(t, cs, "wait_for_version", map[string]any{"app": "brawl", "version": "1.0.0", "wait_seconds": 5}); !strings.Contains(text, "ready in 1 of 2 regions.") ||
		!strings.Contains(text, "create_persistent_server") {
		t.Errorf("wait_for_version: %s", text)
	}
	// Removed from the machines in every region: settled at once, and it says how to get it back.
	f.json("GET /v0/apps/brawl/versions/0.9.0", 200, map[string]any{"name": "0.9.0", "regions": []any{
		map[string]any{"region": "us-central", "status": "removed"}, map[string]any{"region": "us-east", "status": "removed"}}})
	if text := mustOK(t, cs, "wait_for_version", map[string]any{"app": "brawl", "version": "0.9.0", "wait_seconds": 5}); !strings.Contains(text,
		"ready in 0 of 2 regions, removed from the machines in 2. Put it on a branch (set_branch) to have it copied again.") {
		t.Errorf("wait_for_version of a removed version: %s", text)
	}
}

func TestStartWaitsCapsRunTimeAndLabels(t *testing.T) {
	f := newFakeAPI(t)
	f.json("GET /v0/apps/brawl", 200, appBrawl)
	var reads int
	var mu sync.Mutex
	f.on("POST /v0/game-servers", func(_ *http.Request, body map[string]any) (int, any) {
		return 201, gameServer("gs_1", "app_brawl", "starting")
	})
	f.on("GET /v0/game-servers/gs_1", func(*http.Request, map[string]any) (int, any) {
		mu.Lock()
		defer mu.Unlock()
		reads++
		if reads < 3 {
			return 200, gameServer("gs_1", "app_brawl", "starting")
		}
		gs := gameServer("gs_1", "app_brawl", "running")
		gs["expires_at"] = "2026-10-19T22:00:00.000Z"
		return 200, gs
	})
	cs := f.session(t, Config{Key: key, Local: true, MaxTTL: 2 * time.Hour})
	text := mustOK(t, cs, "start_game_server", map[string]any{"app": "brawl", "version": "1.0.0", "labels": map[string]any{"match_id": "88"},
		"regions": []any{"us-central", "us-east"}, "continents": []any{"north-america"}})
	if !strings.Contains(text, "is ready in us-central. Players connect to 203.0.113.24:31042 (udp)") || !strings.Contains(text, "stops on its own") {
		t.Errorf("start result: %s", text)
	}
	body := f.called("POST", "/v0/game-servers")[0].Body
	labels := body["labels"].(map[string]any)
	if body["ttl"] != "2h" || labels["started_by"] != "mcp" || labels["match_id"] != "88" {
		t.Errorf("start body %v", body)
	}
	if p, _ := json.Marshal(body["placement"]); string(p) != `{"continents":["north-america"],"regions":["us-central","us-east"]}` {
		t.Errorf("placement %s", p)
	}
	mustFail(t, cs, "start_game_server", map[string]any{"app": "brawl", "version": "1.0.0", "ttl": "5h"}, "longer than this server allows", "GS_MAX_TTL")
	mustFail(t, cs, "start_game_server", map[string]any{"app": "brawl", "version": "1.0.0", "branch": "live"}, "not both")

	// No limit: no ttl is sent unless asked.
	cs2 := f.session(t, Config{Key: key, Local: true})
	mustOK(t, cs2, "start_game_server", map[string]any{"app": "brawl", "branch": "live", "wait_seconds": 0})
	if _, ok := f.called("POST", "/v0/game-servers")[1].Body["ttl"]; ok {
		t.Error("without GS_MAX_TTL no ttl is added")
	}

	// A retry carries the agent's key, or one made from its external_id, so the API returns the first game server.
	// With neither, every call has a new key.
	for i := 0; i < 2; i++ {
		mustOK(t, cs2, "start_game_server", map[string]any{"app": "brawl", "branch": "live", "wait_seconds": 0, "idempotency_key": "match-8841"})
		mustOK(t, cs2, "start_game_server", map[string]any{"app": "brawl", "branch": "live", "wait_seconds": 0, "external_id": "m-77"})
	}
	starts := f.called("POST", "/v0/game-servers")
	if k := starts[2].Idem; k != "match-8841" || starts[4].Idem != k {
		t.Errorf("given key: %q then %q", k, starts[4].Idem)
	}
	if k := starts[3].Idem; k != "start-m-77" || starts[5].Idem != k {
		t.Errorf("key from external_id: %q then %q", k, starts[5].Idem)
	}
	if starts[0].Idem == "" || starts[0].Idem == starts[1].Idem {
		t.Errorf("without a key each start needs its own: %q, %q", starts[0].Idem, starts[1].Idem)
	}
	if k := retryKey("", "start-", strings.Repeat("m", 300)); len(k) > 255 || !strings.HasPrefix(k, "start-") {
		t.Errorf("a long external_id makes a key of %d characters: %.40s", len(k), k)
	}
}

func TestAllowlistCoversObjectsReachedByID(t *testing.T) {
	f := newFakeAPI(t)
	f.json("GET /v0/apps/brawl", 200, appBrawl)
	f.json("GET /v0/apps/other", 200, appOther)
	f.json("GET /v0/game-servers/gs_other", 200, gameServer("gs_other", "app_other", "running"))
	f.json("GET /v0/game-servers/gs_mine", 200, gameServer("gs_mine", "app_brawl", "running"))
	f.json("POST /v0/game-servers/gs_mine/stop", 202, gameServer("gs_mine", "app_brawl", "stopping"))
	f.json("GET /v0/game-servers", 200, map[string]any{"data": []any{gameServer("gs_mine", "app_brawl", "running"), gameServer("gs_other", "app_other", "running")}, "next_cursor": nil})
	f.json("GET /v0/persistent-servers/ps_other", 200, map[string]any{"id": "ps_other", "app": "app_other", "status": "running"})
	cs := f.session(t, Config{Key: key, Local: true, Apps: []string{"brawl"}})

	mustFail(t, cs, "stop_game_server", map[string]any{"id": "gs_other"}, "belongs to an app this server may not use")
	if len(f.called("POST", "/v0/game-servers/gs_other/stop")) != 0 {
		t.Fatal("a game server of another app was stopped")
	}
	mustFail(t, cs, "read_game_server_log", map[string]any{"id": "gs_other"}, "may not use")
	mustFail(t, cs, "stop_persistent_server", map[string]any{"id": "ps_other"}, "may not use")
	if len(f.called("POST", "/v0/persistent-servers/ps_other/stop")) != 0 {
		t.Fatal("a persistent server of another app was stopped")
	}
	mustFail(t, cs, "list_versions", map[string]any{"app": "other"}, "GS_APPS allows brawl")
	mustOK(t, cs, "stop_game_server", map[string]any{"id": "gs_mine"})
	text := mustOK(t, cs, "list_game_servers", nil)
	if !strings.Contains(text, "gs_mine") || strings.Contains(text, "gs_other") {
		t.Errorf("lists show only allowed apps: %s", text)
	}
	if q, _ := url.ParseQuery(f.called("GET", "/v0/game-servers")[0].Query); q.Get("status") != "starting,running" {
		t.Errorf("running ones by default, as one comma-separated value: %v", q)
	}
}

func TestUploadWindowsBuild(t *testing.T) {
	f := newFakeAPI(t)
	f.json("GET /v0/apps/brawl", 200, appBrawl)
	zip := make([]byte, 2500)
	for i := range zip {
		zip[i] = byte(i * 7)
	}
	path := filepath.Join(t.TempDir(), "BrawlServer.zip")
	if err := os.WriteFile(path, zip, 0o600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	got := map[int][]byte{}
	var storageAuth []string
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("part"))
		data, _ := io.ReadAll(r.Body)
		if r.ContentLength != int64(len(data)) {
			http.Error(w, "streamed upload refused", 400)
			return
		}
		mu.Lock()
		got[n] = data
		storageAuth = append(storageAuth, r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("ETag", fmt.Sprintf(`"etag-%d"`, n))
	}))
	t.Cleanup(storage.Close)
	sum := sha256.Sum256(zip)
	f.on("POST /v0/apps/brawl/builds", func(_ *http.Request, body map[string]any) (int, any) {
		src := body["source"].(map[string]any)
		if src["sha256"] != hex.EncodeToString(sum[:]) || src["bytes"] != 2500.0 || src["filename"] != "BrawlServer.zip" {
			return 422, map[string]any{"status": 422, "code": "validation_failed", "detail": fmt.Sprint(src)}
		}
		parts := []any{}
		for n := 1; n <= 3; n++ {
			parts = append(parts, map[string]any{"number": n, "url": fmt.Sprintf("%s/up?part=%d", storage.URL, n)})
		}
		return 201, map[string]any{"id": "bld_up", "app": "app_brawl", "status": "awaiting_upload",
			"upload": map[string]any{"part_bytes": 1000, "parts": parts, "expires_at": "2026-10-19T23:00:00Z"}}
	})
	f.on("POST /v0/builds/bld_up/complete", func(_ *http.Request, body map[string]any) (int, any) {
		return 202, map[string]any{"id": "bld_up", "app": "app_brawl", "status": "processing", "parts": body["parts"]}
	})
	cs := f.session(t, Config{Key: key, Local: true})
	mustOK(t, cs, "upload_windows_build", map[string]any{"app": "brawl", "zip_path": path})
	if !bytes.Equal(got[1], zip[:1000]) || !bytes.Equal(got[2], zip[1000:2000]) || !bytes.Equal(got[3], zip[2000:]) {
		t.Errorf("parts not sent with their exact bytes: %d %d %d", len(got[1]), len(got[2]), len(got[3]))
	}
	for _, a := range storageAuth {
		if a != "" {
			t.Error("the API key must never go to storage")
		}
	}
	parts, _ := json.Marshal(f.called("POST", "/v0/builds/bld_up/complete")[0].Body["parts"])
	if string(parts) != `[{"etag":"\"etag-1\"","number":1},{"etag":"\"etag-2\"","number":2},{"etag":"\"etag-3\"","number":3}]` {
		t.Errorf("complete sent %s", parts)
	}
	mustFail(t, cs, "upload_windows_build", map[string]any{"app": "brawl", "zip_path": filepath.Dir(path)}, "is a folder")
}

func TestHostedUploadScript(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash")
	}
	for _, tool := range []string{"curl", "dd", "sed"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip("no " + tool)
		}
	}
	f := newFakeAPI(t)
	f.json("GET /v0/apps/brawl", 200, appBrawl)
	zip := bytes.Repeat([]byte("0123456789"), 250)
	path := filepath.Join(t.TempDir(), "My Server's.zip")
	if err := os.WriteFile(path, zip, 0o600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	got := map[int][]byte{}
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := strconv.Atoi(r.URL.Query().Get("part"))
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		got[n] = data
		mu.Unlock()
		w.Header().Set("ETag", fmt.Sprintf(`"e%d"`, n))
	}))
	t.Cleanup(storage.Close)
	f.on("POST /v0/apps/brawl/builds", func(*http.Request, map[string]any) (int, any) {
		return 201, map[string]any{"id": "bld_h", "app": "app_brawl", "status": "awaiting_upload", "upload": map[string]any{"part_bytes": 1024,
			"parts":      []any{map[string]any{"number": 1, "url": storage.URL + "/u?part=1&sig=a'b"}, map[string]any{"number": 2, "url": storage.URL + "/u?part=2"}, map[string]any{"number": 3, "url": storage.URL + "/u?part=3"}},
			"expires_at": "2026-10-19T23:00:00Z"}}
	})
	f.json("GET /v0/builds/bld_h", 200, map[string]any{"id": "bld_h", "app": "app_brawl", "status": "awaiting_upload"})
	f.on("POST /v0/builds/bld_h/complete", func(_ *http.Request, body map[string]any) (int, any) {
		return 202, map[string]any{"id": "bld_h", "status": "processing"}
	})
	cs := f.session(t, Config{Key: key})
	sum := sha256.Sum256(zip)
	text := mustOK(t, cs, "begin_windows_upload", map[string]any{"app": "brawl", "filename": filepath.Base(path), "bytes": len(zip), "sha256": hex.EncodeToString(sum[:]), "zip_path": path})
	start := strings.Index(text, "```bash\n")
	end := strings.LastIndex(text, "```")
	if start < 0 || end <= start {
		t.Fatalf("no script: %s", text)
	}
	out, err := exec.Command("bash", "-c", text[start+8:end]).Output()
	if err != nil {
		t.Fatalf("script: %v\n%s", err, text)
	}
	var parts []map[string]any
	if err := json.Unmarshal(out, &parts); err != nil || len(parts) != 3 || parts[0]["etag"] != `"e1"` {
		t.Fatalf("script output %q: %v", out, err)
	}
	if !bytes.Equal(slices.Concat(got[1], got[2], got[3]), zip) || len(got[1]) != 1024 || len(got[3]) != 2500-2048 {
		t.Error("parts not cut at part_bytes")
	}
	mustOK(t, cs, "complete_windows_upload", map[string]any{"build": "bld_h", "parts": parts})
	if n := len(f.called("POST", "/v0/builds/bld_h/complete")); n != 1 {
		t.Errorf("complete called %d times", n)
	}
}

func TestDiagnoseAndFiles(t *testing.T) {
	f := newFakeAPI(t)
	gs := gameServer("gs_x", "app_brawl", "failed")
	gs["exit"] = map[string]any{"code": -1073741819, "reason": "crashed", "crashed": true}
	f.json("GET /v0/game-servers/gs_x", 200, gs)
	f.json("GET /v0/game-servers/gs_x/events", 200, map[string]any{"data": []any{
		map[string]any{"seq": 1, "type": "launched", "at": "2026-10-19T20:00:00Z"},
		map[string]any{"seq": 2, "type": "exited", "at": "2026-10-19T20:00:09Z", "data": map[string]any{"code": -1073741819}}}, "next_cursor": nil})
	f.json("GET /v0/game-servers/gs_x/logs", 200, map[string]any{"sources": []any{map[string]any{"name": "console", "bytes": 120, "live": false}}, "expires_at": "x"})
	f.on("GET /v0/game-servers/gs_x/logs/stream", func(r *http.Request, _ map[string]any) (int, any) {
		return 200, "Loading map Arena\nFatal error: access violation\n"
	})
	f.json("GET /v0/game-servers/gs_x/artifacts", 200, map[string]any{"data": []any{map[string]any{"id": "art_1", "kind": "crash_dump", "path": "crash/minidump.dmp", "bytes": 4096, "download_url": "https://storage.example.com/x", "expires_at": "x"}}, "next_cursor": nil})
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("download links carry their own permission; the key must not be sent")
		}
		if r.URL.Path == "/bin" {
			_, _ = w.Write([]byte{0, 1, 2, 3})
			return
		}
		_, _ = w.Write([]byte("MaxPlayers=10\n"))
	}))
	t.Cleanup(files.Close)
	f.on("GET /v0/game-servers/gs_x/files/download", func(r *http.Request, _ map[string]any) (int, any) {
		if r.URL.Query().Get("path") == "crash/minidump.dmp" {
			return 303, redirect(files.URL + "/bin") // an API that only redirects
		}
		if r.Header.Get("Accept") != "application/json" {
			return 303, redirect(files.URL + "/text")
		}
		return 200, map[string]any{"url": files.URL + "/text", "expires_at": "2026-10-20T03:15:00Z"}
	})
	cs := f.session(t, Config{Key: key, Local: true})
	text := mustOK(t, cs, "diagnose_game_server", map[string]any{"id": "gs_x"})
	for _, want := range []string{"has failed (crashed, exit code -1073741819)", "exited", "Fatal error: access violation", "crash_dump crash/minidump.dmp"} {
		if !strings.Contains(text, want) {
			t.Errorf("diagnose lacks %q:\n%s", want, text)
		}
	}
	if q := f.called("GET", "/v0/game-servers/gs_x/logs/stream")[0].Query; !strings.Contains(q, "tail=80") || !strings.Contains(q, "source=console") {
		t.Errorf("log query %s", q)
	}
	if text := mustOK(t, cs, "read_game_server_file", map[string]any{"id": "gs_x", "path": "Game.ini"}); !strings.Contains(text, "MaxPlayers=10") {
		t.Errorf("text file: %s", text)
	}
	if text := mustOK(t, cs, "read_game_server_file", map[string]any{"id": "gs_x", "path": "crash/minidump.dmp"}); !strings.Contains(text, "not a text file") || !strings.Contains(text, files.URL+"/bin") {
		t.Errorf("binary file: %s", text)
	}
}

func TestPersistentServers(t *testing.T) {
	f := newFakeAPI(t)
	f.json("GET /v0/apps/brawl", 200, appBrawl)
	ps := func(status string) map[string]any {
		return map[string]any{"id": "ps_1", "app": "app_brawl", "name": "EU PvE 1", "region": "europe-central", "continent": "europe", "status": status, "desired_state": "running",
			"address":     map[string]any{"ipv4": "203.0.113.77", "ports": map[string]any{"game": map[string]any{"port": 24015, "protocol": "udp"}}},
			"last_backup": map[string]any{"id": "bkp_1", "kind": "stop", "status": "ready", "created_at": "2026-10-30T06:00:00Z"}}
	}
	f.on("POST /v0/persistent-servers", func(_ *http.Request, body map[string]any) (int, any) { return 201, ps("starting") })
	var reads int
	var mu sync.Mutex
	f.on("GET /v0/persistent-servers/ps_1", func(*http.Request, map[string]any) (int, any) {
		mu.Lock()
		defer mu.Unlock()
		reads++
		if reads < 2 {
			return 200, ps("starting")
		}
		return 200, ps("running")
	})
	f.on("POST /v0/persistent-servers/ps_1/restore", func(_ *http.Request, body map[string]any) (int, any) { return 202, ps("stopped") })
	cs := f.session(t, Config{Key: key, Local: true})
	text := mustOK(t, cs, "create_persistent_server", map[string]any{"app": "brawl", "name": "EU PvE 1", "region": "europe-central", "branch": "live",
		"save_paths": []any{"Saved/SaveGames"}, "backup": map[string]any{"every": "10m", "keep_for": "30d"},
		"when_empty": map[string]any{"after": "15m", "then": "pause", "stop_after": "2h"}})
	for _, want := range []string{"Persistent server EU PvE 1 (ps_1) in europe-central is running", "203.0.113.77:24015", "Last backup bkp_1 (At stop, "} {
		if !strings.Contains(text, want) {
			t.Errorf("create_persistent_server lacks %q: %s", want, text)
		}
	}
	body := f.called("POST", "/v0/persistent-servers")[0].Body
	we, _ := json.Marshal(body["when_empty"])
	if body["start"] != true || body["region"] != "europe-central" || body["backup"].(map[string]any)["every"] != "10m" ||
		body["labels"].(map[string]any)["started_by"] != "mcp" || string(we) != `{"after":"15m","stop_after":"2h","then":"pause"}` {
		t.Errorf("persistent server body %v", body)
	}
	// A retry of the create with the agent's key sends that key again.
	mustOK(t, cs, "create_persistent_server", map[string]any{"app": "brawl", "name": "EU PvE 2", "region": "europe-central", "branch": "live",
		"save_paths": []any{"Saved/SaveGames"}, "wait_seconds": 0, "idempotency_key": "create-eu-pve-2"})
	if k := f.called("POST", "/v0/persistent-servers")[1].Idem; k != "create-eu-pve-2" {
		t.Errorf("create key %q", k)
	}
	// Going back: nothing happens without a preview, and the run sends only the preview's id.
	mustFail(t, cs, "plan_rollback", map[string]any{"ids": []string{"ps_1"}}, "give at")
	var planMu sync.Mutex
	backupThen := "bkp_1"
	plan := func() map[string]any {
		planMu.Lock()
		defer planMu.Unlock()
		return map[string]any{"id": "pln_1", "total": 1, "players": 3, "targets": []any{map[string]any{"id": "ps_1", "app": "app_brawl", "name": "EU 1", "region": "europe-west", "players": 3,
			"restore": map[string]any{"backup": backupThen, "saved_at": "2026-10-30T11:57:00Z", "play_lost": "2h3m0s"}}}, "skipped": []any{}}
	}
	f.on("POST /v0/persistent-servers/ps_1/restore", func(*http.Request, map[string]any) (int, any) { return 200, map[string]any{"plan": plan()} })
	text = mustOK(t, cs, "plan_rollback", map[string]any{"ids": []string{"ps_1"}, "backup": "bkp_1"})
	if r := f.called("POST", "/v0/persistent-servers/ps_1/restore")[0].Body; r["backup"] != "bkp_1" || r["dry_run"] != true {
		t.Errorf("restore preview body %v", r)
	}
	if !strings.Contains(text, "| EU 1 | europe-west | 3 | 2026-10-30T11:57:00Z | 2h3m0s |") || strings.Contains(text, "pln_1") {
		t.Errorf("the preview shows the table and only our plan_id, never the API's own: %s", text)
	}
	var runs []map[string]any
	f.on("POST /v0/bulk-actions/rollbacks", func(_ *http.Request, b map[string]any) (int, any) {
		if b["plan"] != nil {
			runs = append(runs, b)
			return 202, map[string]any{"bulk_action": map[string]any{"id": "bulk_1"}, "plan": plan()}
		}
		return 200, map[string]any{"plan": plan()}
	})
	planID := planIDOf(t, mustOK(t, cs, "plan_rollback", map[string]any{"app": "brawl", "continents": []string{"europe"}, "at": "2026-10-30T12:00:00Z", "try_one_first": true}))
	// The API's own preview ID is not a plan_id: roll_back runs only what it can check again.
	mustFail(t, cs, "roll_back", map[string]any{"plan": "pln_1"}, "not a plan_id from plan_rollback")
	mustOK(t, cs, "roll_back", map[string]any{"plan": planID})
	if r := f.called("POST", "/v0/bulk-actions/rollbacks"); len(r) != 3 || r[1].Body["at"] != "2026-10-30T12:00:00Z" || r[1].Body["plan"] != nil ||
		r[2].Body["plan"] != "pln_1" || len(r[2].Body) != 1 {
		t.Errorf("rollback calls (preview, preview again, run): %v", r)
	}
	// A backup changed since the preview: refused, the new table shown, nothing run.
	planMu.Lock()
	backupThen = "bkp_9"
	planMu.Unlock()
	mustFail(t, cs, "roll_back", map[string]any{"plan": planID}, "changed since the preview", "| EU 1 |")
	if len(runs) != 1 {
		t.Fatalf("a changed preview ran: %v", runs)
	}
	mustOK(t, cs, "plan_rollback", map[string]any{"app": "brawl", "before_update": "bulk_5", "version": "then"})
	if r := f.called("POST", "/v0/bulk-actions/rollbacks"); len(r) != 5 || r[4].Body["before_update"] != "bulk_5" || r[4].Body["at"] != nil {
		t.Errorf("before-update preview calls %v", r)
	}
	// A preview that ran out is not run.
	f.on("POST /v0/persistent-servers/ps_1/restore", func(*http.Request, map[string]any) (int, any) {
		p := plan()
		p["expires_at"] = time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
		return 200, map[string]any{"plan": p}
	})
	old := planIDOf(t, mustOK(t, cs, "plan_rollback", map[string]any{"ids": []string{"ps_1"}, "backup": "bkp_1"}))
	mustFail(t, cs, "roll_back", map[string]any{"plan": old}, "ran out")
	if len(runs) != 1 {
		t.Fatalf("a preview that ran out ran: %v", runs)
	}
	f.json("POST /v0/bulk-actions/bulk_1/undo", 202, map[string]any{"bulk_action": map[string]any{"id": "bulk_2"}, "plan": plan()})
	mustOK(t, cs, "undo_bulk_action", map[string]any{"id": "bulk_1"})
	f.json("POST /v0/backups/bkp_1/export", 202, map[string]any{"status": "ready", "url": "https://storage.test/b.zip"})
	mustOK(t, cs, "download_backup", map[string]any{"backup": "bkp_1"})

	got := make(chan []byte, 1)
	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- b
	}))
	defer store.Close()
	zipFile := filepath.Join(t.TempDir(), "saves.zip")
	if err := os.WriteFile(zipFile, []byte("a zip"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.json("POST /v0/persistent-servers/ps_1/backups/uploads", 201, map[string]any{"id": "upl_1", "url": store.URL + "/up"})
	f.json("POST /v0/persistent-servers/ps_1/backups/uploads/upl_1/complete", 202, map[string]any{"status": "processing"})
	f.json("GET /v0/persistent-servers/ps_1/backups/uploads/upl_1", 200, map[string]any{"status": "done", "backup": "bkp_7"})
	if text := mustOK(t, cs, "upload_save", map[string]any{"id": "ps_1", "zip_path": zipFile, "label": "Season 2"}); !strings.Contains(text, "Backup bkp_7 holds your save") {
		t.Error(text)
	}
	if b := <-got; string(b) != "a zip" {
		t.Errorf("storage got %q", b)
	}
	mustFail(t, cs, "update_persistent_server", map[string]any{"id": "ps_1", "apply": "now"}, "nothing to change")
	f.json("PATCH /v0/persistent-servers/ps_1", 200, ps("running"))
	mustOK(t, cs, "update_persistent_server", map[string]any{"id": "ps_1", "when_empty": map[string]any{"enabled": false}})
	if we := f.called("PATCH", "/v0/persistent-servers/ps_1")[0].Body["when_empty"].(map[string]any); we["enabled"] != false || len(we) != 1 {
		t.Errorf("pausing off is sent as just enabled false: %v", we)
	}

	// Paused is settled: the wait returns at once and says the first player resumes it.
	paused := ps("paused")
	paused["id"], paused["paused_since"] = "ps_2", "2026-10-30T07:00:00Z"
	f.json("GET /v0/persistent-servers/ps_2", 200, paused)
	start := time.Now()
	text = mustOK(t, cs, "wait_for_persistent_server", map[string]any{"id": "ps_2", "wait_seconds": 60})
	if !strings.Contains(text, "is paused. Players connect to 203.0.113.77:24015 (udp). Paused since 2026-10-30T07:00:00Z; the first player resumes it.") ||
		strings.Contains(text, "on its way") || time.Since(start) > 5*time.Second {
		t.Errorf("a paused persistent server (%s): %s", time.Since(start), text)
	}

	// Backups: taken now, and listed.
	f.json("POST /v0/persistent-servers/ps_1/backups", 202, map[string]any{"id": "bkp_2", "persistent_server": "ps_1", "kind": "manual", "status": "in_progress"})
	if text := mustOK(t, cs, "back_up_persistent_server", map[string]any{"id": "ps_1"}); !strings.Contains(text, "Backup bkp_2 of persistent server ps_1 is in_progress.") {
		t.Errorf("back_up_persistent_server: %s", text)
	}
	f.json("GET /v0/persistent-servers/ps_1/backups", 200, map[string]any{"data": []any{
		map[string]any{"id": "bkp_2", "kind": "manual", "status": "ready"}, map[string]any{"id": "bkp_c", "kind": "continuous", "status": "ready"}}, "next_cursor": nil})
	if text := mustOK(t, cs, "list_backups", map[string]any{"id": "ps_1"}); !strings.Contains(text, "2 backups of persistent server ps_1.") {
		t.Errorf("list_backups: %s", text)
	}
}

func TestEstimateCost(t *testing.T) {
	f := newFakeAPI(t)
	f.json("GET /v0/prices", 200, map[string]any{"currency": "USD", "locked_until": "2028-10-09T00:00:00Z", "free_cpu_hours_per_month": 2000, "bandwidth": "included",
		"rates": map[string]any{"on_demand_cpu_hour_2gb": "0.0350", "on_demand_cpu_hour_4gb": "0.0500", "reserved_cpu_hour": "0.0250", "persistent_server_cpu_hour": "0.0250", "own_machine_cpu_month": "1.0000", "paused_cpu_hour": "0.0250"}})
	cs := f.session(t, Config{Key: key, Local: true})
	for _, c := range []struct {
		args map[string]any
		want string
	}{
		{map[string]any{"cpu": 2, "memory_gb": 4, "hours": 3, "count": 10}, "10 x 2 vCPU for 3 hours = 60 vCPU-hours at $0.0350 per vCPU-hour (on_demand_cpu_hour_2gb) = $2.10"},
		{map[string]any{"cpu": 2, "memory_gb": 6, "hours": 3, "count": 10}, "(on_demand_cpu_hour_4gb) = $3.00"},
		{map[string]any{"kind": "persistent_server", "cpu": 4, "hours": 720}, "2880 vCPU-hours at $0.0250 per vCPU-hour (persistent_server_cpu_hour) = $72.00"},
		{map[string]any{"kind": "warm", "cpu": 0.5, "memory_gb": 1, "hours": 24, "count": 3}, "36 vCPU-hours at $0.0350"},
		{map[string]any{"kind": "warm", "cpu": 0.5, "memory_gb": 1, "hours": 24, "count": 3}, "The ready server price is not final"},
		{map[string]any{"kind": "reserved", "cpu": 8, "hours": 730}, "5840 vCPU-hours at $0.0250 per vCPU-hour (reserved_cpu_hour) = $146.00"},
		{map[string]any{"kind": "reserved", "cpu": 8, "hours": 730}, "free vCPU-hours do not apply to it"},
	} {
		if text := mustOK(t, cs, "estimate_cost", c.args); !strings.Contains(text, c.want) {
			t.Errorf("%v: %s", c.args, text)
		}
	}
	// A reservation never gets free vCPU-hours, so its estimate must not promise them.
	if text := mustOK(t, cs, "estimate_cost", map[string]any{"kind": "reserved", "cpu": 8, "hours": 730}); strings.Contains(text, "are free") {
		t.Errorf("a reserved estimate promises free hours: %s", text)
	}
	mustFail(t, cs, "estimate_cost", map[string]any{"cpu": 1, "hours": 1}, "memory_gb")
}

func TestHTTPHandlerUsesEachRequestsKey(t *testing.T) {
	f := newFakeAPI(t)
	f.json("GET /v0/apps", 200, map[string]any{"data": []any{appBrawl}, "next_cursor": nil})
	h := HTTPHandler(Config{APIURL: f.srv.URL, Key: "gsk_live_EXAMPLE", Local: true}, nil)
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	rpc := func(auth string, msg string) string {
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
		return string(data)
	}
	list := rpc("", `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`)
	if !strings.Contains(list, "begin_windows_upload") || strings.Contains(list, "upload_windows_build") {
		t.Errorf("tools/list without a key: %.300s", list)
	}
	out := rpc("Bearer gsk_live_EXAMPLE_not_a_real_key", `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_apps","arguments":{}}}`)
	if !strings.Contains(out, "brawl") {
		t.Errorf("tools/call: %.400s", out)
	}
	calls := f.called("GET", "/v0/apps")
	if len(calls) != 1 || calls[0].Auth != "Bearer gsk_live_EXAMPLE_not_a_real_key" {
		t.Errorf("the caller's key must be used, never the environment's: %+v", calls)
	}
	// The hosted server reads no GS_API_KEY: a call without a key says to send the header (it once said to set
	// GS_API_KEY in the server's environment, which the hosted server ignores).
	if out := rpc("", `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"list_apps","arguments":{}}}`); !strings.Contains(out, "no API key") ||
		!strings.Contains(out, "Authorization: Bearer") || strings.Contains(out, "set GS_API_KEY") {
		t.Errorf("a call without a key: %.400s", out)
	}
	resp, err := http.Get(srv.URL + "/health")
	if err != nil || resp.StatusCode != 200 {
		t.Errorf("health: %v %v", err, resp)
	}
}

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("GS_API_KEY", " gsk_live_EXAMPLE_not_a_real_key ")
	t.Setenv("GS_APPS", "brawl, arena ,")
	t.Setenv("GS_READ_ONLY", "1")
	t.Setenv("GS_MAX_TTL", "1d")
	c, err := ConfigFromEnv()
	if err != nil || c.Key != "gsk_live_EXAMPLE_not_a_real_key" || !c.ReadOnly || len(c.Apps) != 2 || c.Apps[1] != "arena" || c.MaxTTL != 24*time.Hour {
		t.Errorf("%+v %v", c, err)
	}
	t.Setenv("GS_MAX_TTL", "0")
	if c, _ := ConfigFromEnv(); c.MaxTTL != 0 {
		t.Error("0 turns the limit off")
	}
	t.Setenv("GS_MAX_TTL", "soon")
	if _, err := ConfigFromEnv(); err == nil {
		t.Error("a bad duration is refused")
	}
}

// The API address: GS_API_URL when set, else the public API's own address; a malformed one stops the server before
// it starts, locally or hosted.
func TestAPIAddressDefaultAndRefusal(t *testing.T) {
	t.Setenv("GS_API_KEY", key)
	t.Setenv("GS_API_URL", " ")
	if cfg, err := ConfigFromEnv(); err != nil || cfg.APIURL != "https://studio.survivalservers.com" {
		t.Errorf("no GS_API_URL: address %q (%v), want the public API's own", cfg.APIURL, err)
	}
	t.Setenv("GS_API_URL", "https://staging.example.net")
	if cfg, err := ConfigFromEnv(); err != nil || cfg.APIURL != "https://staging.example.net" {
		t.Errorf("GS_API_URL did not win: %q (%v)", cfg.APIURL, err)
	}
	t.Setenv("GS_API_URL", "studio.survivalservers.com")
	for _, args := range [][]string{nil, {"-http", "127.0.0.1:0"}} {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // had it started, it would stop at once
		var stderr bytes.Buffer
		if err := Serve(ctx, args, io.Discard, &stderr); err == nil || !strings.Contains(err.Error(), "is not an http(s) address") {
			t.Errorf("%v: a malformed address: %v", args, err)
		}
	}
}

func TestDocsToolsPromptsAndResources(t *testing.T) {
	f := newFakeAPI(t)
	cs := f.session(t, Config{Local: true}) // the docs need no key
	text := mustOK(t, cs, "setup_guide", map[string]any{"engine": "godot", "os": "linux"})
	for _, want := range []string{"create_linux_build", "# Godot", "Linux images", "Source: https://"} {
		if !strings.Contains(text, want) {
			t.Errorf("setup_guide lacks %q", want)
		}
	}
	if strings.Contains(text, "Not written yet") {
		t.Error("every page of a setup guide is written")
	}
	mustFail(t, cs, "setup_guide", map[string]any{"engine": "unreal", "os": "mac"})
	if text := mustOK(t, cs, "search_docs", map[string]any{"query": "stop method ctrl_break grace"}); !strings.Contains(text, "windows-builds") {
		t.Errorf("search: %.300s", text)
	}
	ctx := context.Background()
	prompts, err := cs.ListPrompts(ctx, nil)
	if err != nil || len(prompts.Prompts) != 4 {
		t.Fatalf("prompts: %v %v", prompts, err)
	}
	p, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "setup_unity", Arguments: map[string]string{"os": "linux"}})
	if err != nil || !strings.Contains(p.Messages[0].Content.(*mcp.TextContent).Text, "# Unity") {
		t.Errorf("setup_unity prompt: %v", err)
	}
	res, err := cs.ListResources(ctx, nil)
	if err != nil || len(res.Resources) < 15 {
		t.Fatalf("resources: %v", err)
	}
	r, err := cs.ReadResource(ctx, &mcp.ReadResourceParams{URI: "docs://game-server/local-api"})
	if err != nil || !strings.Contains(r.Contents[0].Text, "POST /v0/self/ready") {
		t.Errorf("resource: %v", err)
	}
}

// A game server's machine sends the last run's logs and files a few seconds after the exit (artifacts_collected on
// the timeline); until then the API has no console log for it. Read straight after a stop or a crash, as an agent
// does, the tools wait for them instead of answering "not found" (diagnose_game_server 3 seconds after
// a stop once said "Console: not readable ... No such object for this studio").
func TestReadsRightAfterTheExitWaitForTheMachine(t *testing.T) {
	f := newFakeAPI(t)
	var mu sync.Mutex
	reads := map[string]int{}
	collectedAfter := map[string]int{"gs_new": 3, "gs_old": -1, "gs_lost": -1}
	exited := map[string]string{
		"gs_new":  time.Now().Add(-3 * time.Second).UTC().Format(time.RFC3339Nano),
		"gs_old":  time.Now().Add(-10 * time.Minute).UTC().Format(time.RFC3339Nano),
		"gs_lost": time.Now().Add(-5 * time.Second).UTC().Format(time.RFC3339Nano),
	}
	collected := func(id string) bool {
		mu.Lock()
		defer mu.Unlock()
		return collectedAfter[id] >= 0 && reads[id] >= collectedAfter[id]
	}
	for id := range exited {
		gs := gameServer(id, "app_brawl", "stopped")
		gs["exit"] = map[string]any{"code": 0, "reason": "api", "crashed": false}
		f.json("GET /v0/game-servers/"+id, 200, gs)
		f.on("GET /v0/game-servers/"+id+"/events", func(*http.Request, map[string]any) (int, any) {
			mu.Lock()
			reads[id]++
			mu.Unlock()
			ev := []any{
				map[string]any{"seq": 1, "type": "launched", "at": "2026-10-28T16:11:01Z"},
				map[string]any{"seq": 2, "type": "exited", "at": exited[id], "data": map[string]any{"code": 0}},
			}
			if collected(id) {
				ev = append(ev, map[string]any{"seq": 3, "type": "artifacts_collected", "at": exited[id], "data": map[string]any{"count": 2}})
			}
			return 200, map[string]any{"data": ev, "next_cursor": nil}
		})
		f.on("GET /v0/game-servers/"+id+"/logs", func(*http.Request, map[string]any) (int, any) {
			if !collected(id) {
				return 200, map[string]any{"sources": []any{}, "expires_at": "x"}
			}
			return 200, map[string]any{"sources": []any{map[string]any{"name": "console", "bytes": 40, "live": false}}, "expires_at": "x"}
		})
		f.on("GET /v0/game-servers/"+id+"/logs/stream", func(*http.Request, map[string]any) (int, any) {
			if !collected(id) {
				return 404, map[string]any{"status": 404, "code": "not_found", "detail": "No log source console; see the sources list.", "request_id": "req_fake"}
			}
			return 200, "Server ready for players\nmatch over: stop_signal after 5s\n"
		})
		f.json("GET /v0/game-servers/"+id+"/artifacts", 200, map[string]any{"data": []any{}, "next_cursor": nil})
		f.json("GET /v0/game-servers/"+id+"/files", 200, map[string]any{"path": "", "live": false, "data": []any{}, "next_cursor": nil})
	}
	cs := f.session(t, Config{Key: key, Local: true, KeptWait: 300 * time.Millisecond})

	// Exited 3 seconds ago, sent on the third read of the timeline: waited for.
	text := mustOK(t, cs, "diagnose_game_server", map[string]any{"id": "gs_new"})
	for _, want := range []string{"has stopped (api, exit code 0)", "Last 80 console lines. ", "match over: stop_signal", "artifacts_collected"} {
		if !strings.Contains(text, want) {
			t.Errorf("diagnose straight after the exit lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "not readable") || strings.Contains(text, "has not sent") {
		t.Errorf("diagnose straight after the exit:\n%s", text)
	}
	if text := mustOK(t, cs, "read_game_server_log", map[string]any{"id": "gs_new"}); !strings.Contains(text, "match over") {
		t.Errorf("log straight after the exit: %s", text)
	}

	// Exited 10 minutes ago and still nothing sent (its machine is away): said at once, not waited for.
	start := time.Now()
	text = mustOK(t, cs, "diagnose_game_server", map[string]any{"id": "gs_old"})
	if !strings.Contains(text, "The machine has not sent the logs and files of gs_old's last run yet") || !strings.Contains(text, "Next: call diagnose_game_server again") {
		t.Errorf("diagnose of a run never sent:\n%s", text)
	}
	if text := mustOK(t, cs, "read_game_server_log", map[string]any{"id": "gs_old"}); !strings.Contains(text, "has not sent") || strings.Contains(text, "No such object") {
		t.Errorf("log of a run never sent: %s", text)
	}
	if time.Since(start) > 250*time.Millisecond {
		t.Errorf("an old exit was waited for (%s)", time.Since(start))
	}

	// Exited 5 seconds ago and not sent within the wait: the same answer after KeptWait.
	if text := mustOK(t, cs, "read_game_server_log", map[string]any{"id": "gs_lost"}); !strings.Contains(text, "has not sent the logs and files of gs_lost") {
		t.Errorf("log of a run not sent in time: %s", text)
	}
	if text := mustOK(t, cs, "list_game_server_files", map[string]any{"id": "gs_lost"}); !strings.Contains(text, "has not sent") {
		t.Errorf("files of a run not sent in time: %s", text)
	}
}

func TestCommandsAndBulkActions(t *testing.T) {
	f := newFakeAPI(t)
	var mu sync.Mutex
	targets := []any{map[string]any{"id": "ps_1"}, map[string]any{"id": "ps_2"}}
	f.on("POST /v0/bulk-actions/commands", func(_ *http.Request, b map[string]any) (int, any) {
		mu.Lock()
		defer mu.Unlock()
		if b["dry_run"] == true {
			return 200, map[string]any{"plan": map[string]any{"total": len(targets), "players": 7, "targets": targets, "batches": []any{map[string]any{"batch": 1}},
				"skipped": []any{map[string]any{"name": "EU 3", "detail": "Its version has no game command announce."}}}}
		}
		return 202, map[string]any{"bulk_action": map[string]any{"id": "bulk_1", "status": "running"}}
	})
	f.json("GET /v0/bulk-actions/bulk_1", 200, map[string]any{"id": "bulk_1", "type": "command", "status": "succeeded", "counts": map[string]any{"total": 2}})
	f.json("GET /v0/bulk-actions/bulk_1/replies", 200, map[string]any{"data": []any{map[string]any{"reply": "Saved", "count": 2}}, "next_cursor": nil})
	f.json("GET /v0/persistent-servers/ps_1", 200, map[string]any{"id": "ps_1", "app": "app_1"})
	f.json("POST /v0/persistent-servers/ps_1/command", 200, map[string]any{"delivered": true, "output": "Saved"})
	f.json("GET /v0/apps/brawl/versions/1.4.2/profile", 200, map[string]any{"profile": map[string]any{"commands": []any{map[string]any{"id": "save"}}}, "revision": 2})
	f.on("PUT /v0/apps/brawl/versions/1.4.2/profile", func(_ *http.Request, b map[string]any) (int, any) {
		return 200, map[string]any{"profile": b, "revision": 3}
	})
	cs := f.session(t, Config{Key: key})
	sends := func() int {
		n := 0
		for _, c := range f.called("POST", "/v0/bulk-actions/commands") {
			if c.Body["dry_run"] != true {
				n++
			}
		}
		return n
	}

	if got := mustOK(t, cs, "get_game_profile", map[string]any{"app": "brawl", "version": "1.4.2"}); !strings.Contains(got, `"save"`) {
		t.Fatalf("profile: %s", got)
	}
	if got := mustOK(t, cs, "set_game_profile", map[string]any{"app": "brawl", "version": "1.4.2", "profile": map[string]any{"commands": []any{}}}); !strings.Contains(got, "revision 3") {
		t.Fatalf("set profile: %s", got)
	}
	if got := mustOK(t, cs, "send_command", map[string]any{"id": "ps_1", "command_id": "save"}); !strings.Contains(got, "Saved") {
		t.Fatalf("send_command: %s", got)
	}
	got := mustOK(t, cs, "plan_bulk_action", map[string]any{"action": "command", "selector": map[string]any{"app": "brawl"}, "command_id": "announce", "args": map[string]any{"text": "hi"}})
	if !strings.Contains(got, "Reaches 2 servers") || !strings.Contains(got, "Skips EU 3") {
		t.Fatalf("plan: %s", got)
	}
	_, rest, _ := strings.Cut(got, "plan_id: ")
	planID, _, _ := strings.Cut(rest, "\n")
	if sends() != 0 {
		t.Fatal("planning sent it")
	}
	if got := mustOK(t, cs, "run_bulk_action", map[string]any{"plan_id": planID}); !strings.Contains(got, "bulk_1") {
		t.Fatalf("run: %s", got)
	}
	if sends() != 1 {
		t.Fatalf("sends %d", sends())
	}
	if got := mustOK(t, cs, "get_bulk_action", map[string]any{"id": "bulk_1"}); !strings.Contains(got, `"Saved"`) {
		t.Fatalf("get: %s", got)
	}

	// The servers changed since the plan: nothing runs.
	mu.Lock()
	targets = append(targets, map[string]any{"id": "ps_3"})
	mu.Unlock()
	mustFail(t, cs, "run_bulk_action", map[string]any{"plan_id": planID}, "changed since the plan")
	if sends() != 1 {
		t.Fatal("a changed plan ran")
	}
	mustFail(t, cs, "run_bulk_action", map[string]any{"plan_id": "nonsense"}, "not a plan_id")
	mustFail(t, cs, "plan_bulk_action", map[string]any{"action": "wipe", "selector": map[string]any{"app": "brawl"}})

	f.json("GET /v0/persistent-servers/ps_1/config", 200, map[string]any{"settings": []any{map[string]any{"id": "xp_rate", "value": 2, "source": "server", "pending": false}}})
	f.on("PATCH /v0/persistent-servers/ps_1/config", func(_ *http.Request, b map[string]any) (int, any) {
		return 200, map[string]any{"settings": []any{}, "results": []any{map[string]any{"id": "xp_rate", "delivered": true}}}
	})
	if got := mustOK(t, cs, "get_server_settings", map[string]any{"id": "ps_1"}); !strings.Contains(got, "xp_rate") {
		t.Fatalf("settings: %s", got)
	}
	if got := mustOK(t, cs, "set_server_settings", map[string]any{"id": "ps_1", "values": map[string]any{"xp_rate": 3}, "live": true}); !strings.Contains(got, `"delivered"`) {
		t.Fatalf("set settings: %s", got)
	}
	if b := f.called("PATCH", "/v0/persistent-servers/ps_1/config")[0].Body; obj(b["values"])["xp_rate"] != 3.0 || b["live"] != true {
		t.Fatalf("settings body: %v", b)
	}

	f.json("GET /v0/persistent-servers/ps_1/players", 200, map[string]any{"source": "reported", "players": []any{map[string]any{"name": "Vex_77"}}})
	if got := mustOK(t, cs, "list_players", map[string]any{"id": "ps_1"}); !strings.Contains(got, "Vex_77") {
		t.Fatalf("players: %s", got)
	}

	// Limited to one app: a plan must name it.
	limited := f.session(t, Config{Key: key, Apps: []string{"other"}})
	mustFail(t, limited, "plan_bulk_action", map[string]any{"action": "command", "selector": map[string]any{"regions": []string{"us-east"}}, "command_id": "save"}, "selector.app")
}

// "Ask me first": a stop made again with the approved request's ID sends it as the Approval header, through this
// server's own HTTP client, so every API call made with a context carries it; without one, no header.
func TestApprovalGoesAsItsHeader(t *testing.T) {
	f := newFakeAPI(t)
	var approvals []string
	f.on("GET /v0/game-servers/gs_a1", func(*http.Request, map[string]any) (int, any) {
		return 200, map[string]any{"id": "gs_a1", "status": "running", "app": "app_1"}
	})
	f.on("POST /v0/game-servers/gs_a1/stop", func(r *http.Request, _ map[string]any) (int, any) {
		approvals = append(approvals, r.Header.Get("Approval"))
		return 200, map[string]any{"id": "gs_a1", "status": "stopping"}
	})
	cs := f.session(t, Config{Key: key, Local: true})
	mustOK(t, cs, "stop_game_server", map[string]any{"id": "gs_a1"})
	mustOK(t, cs, "stop_game_server", map[string]any{"id": "gs_a1", "approval": "apr_g01JB3C5D7E9F1G3H5J7K9M1N3"})
	if len(approvals) != 2 || approvals[0] != "" || approvals[1] != "apr_g01JB3C5D7E9F1G3H5J7K9M1N3" {
		t.Fatalf("Approval headers sent: %q", approvals)
	}
}
