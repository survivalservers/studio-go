package server

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestLive runs the tools against a real API (GS_LIVE_API_URL and GS_LIVE_API_KEY, a key with every scope on a
// studio made for testing): real uploads through storage links, real processing, and the API's own answers
// where it cannot do something yet. Never point it at a studio with live games: it creates apps and builds.
func TestLive(t *testing.T) {
	base, key := os.Getenv("GS_LIVE_API_URL"), os.Getenv("GS_LIVE_API_KEY")
	if base == "" || key == "" {
		t.Skip("GS_LIVE_API_URL and GS_LIVE_API_KEY are not set")
	}
	session := func(local bool) *mcpSession {
		s, err := New(Config{APIURL: base, Key: key, Local: local, MaxTTL: 10 * time.Minute, Poll: 250 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		return &mcpSession{t: t, cs: connect(t, s)}
	}
	local, hosted := session(true), session(false)
	run := fmt.Sprintf("%d", time.Now().Unix()%1000000)

	local.ok("get_account", nil)
	regions := local.ok("list_regions", map[string]any{"os": "linux"})
	region := firstID(t, regions)
	local.ok("get_prices", nil)
	local.ok("estimate_cost", map[string]any{"cpu": 2, "memory_gb": 4, "hours": 1, "count": 3})
	local.ok("get_usage", nil)

	// Linux: an image build, a version, a branch, then the API's own answer to a start.
	lin := "mcp-lin-" + run
	local.ok("create_app", map[string]any{"name": "MCP Linux " + run, "slug": lin, "os": "linux", "continents": []any{"north-america"}})
	b := local.ok("create_linux_build", map[string]any{"app": lin, "image": "registry.example.com/mcp-test/server:1.0.0"})
	build := field(t, b, "Build ", " ")
	if text := local.ok("wait_for_build", map[string]any{"build": build, "wait_seconds": 120}); !strings.Contains(text, "is ready") {
		t.Fatalf("image build: %s", text)
	}
	local.ok("create_version", map[string]any{"app": lin, "name": "1.0.0", "build": build, "cpu": 1, "memory_gb": 1,
		"ports":       []any{map[string]any{"name": "game", "protocol": "udp", "container_port": 7777}},
		"ready_check": map[string]any{"type": "local_api"}, "stop_method": "sigterm", "stop_shutdown_time": "10s", "compat": []any{"agones"}})
	local.ok("wait_for_version", map[string]any{"app": lin, "version": "1.0.0", "wait_seconds": 0})
	local.ok("list_versions", map[string]any{"app": lin})
	local.ok("set_branch", map[string]any{"app": lin, "branch": "live", "version": "1.0.0"})
	if text, isErr := local.call("start_game_server", map[string]any{"app": lin, "branch": "live", "wait_seconds": 5}); isErr {
		if !strings.Contains(text, "Next:") {
			t.Errorf("a refused start says what to do: %s", text)
		}
		t.Logf("start answered (expected without machines): %s", text)
	} else {
		t.Logf("start: %s", firstLine(text))
		local.ok("list_game_servers", map[string]any{"app": lin, "started_here": true})
	}
	local.ok("list_game_servers", map[string]any{"app": lin, "status": []any{"starting", "running", "stopped", "failed"}})
	if text, isErr := local.call("set_pool", map[string]any{"app": lin, "region": region, "branch": "live", "cpu": 1, "memory_gb": 1, "mode": "fixed", "min": 1}); isErr {
		t.Logf("pool answered: %s", text)
	} else {
		local.ok("list_pools", map[string]any{"app": lin})
		local.ok("remove_pool", map[string]any{"app": lin, "region": region})
	}
	if text, isErr := local.call("create_persistent_server", map[string]any{"app": lin, "name": "mcp-persistent-" + run, "region": region, "branch": "live", "start": false,
		"save_paths": []any{"/data/saves"}, "backup": map[string]any{"every": "10m", "keep_for": "7d"},
		"when_empty": map[string]any{"after": "10m", "then": "pause", "stop_after": "1h"}}); isErr {
		t.Logf("persistent server answered: %s", text)
	} else {
		ps := field(t, text, "(", ")")
		local.ok("wait_for_persistent_server", map[string]any{"id": ps, "wait_seconds": 0})
		local.ok("list_persistent_servers", map[string]any{"app": lin})
		local.ok("list_backups", map[string]any{"id": ps})
	}
	// Without a cell for its region the API cannot look the game server up (503); with one it is not found (404).
	if text, _ := local.call("diagnose_game_server", map[string]any{"id": "gs_a01JA8X9K2Q7M3T5V6W8Y0Z1B2"}); !strings.Contains(text, "Next:") ||
		!(strings.Contains(text, "not_found") || strings.Contains(text, "unavailable")) {
		t.Errorf("an unknown game server: %s", text)
	}

	// Windows: a real zip, uploaded in parts through storage links by both servers, processed, and run as a version.
	win := "mcp-win-" + run
	local.ok("create_app", map[string]any{"name": "MCP Windows " + run, "slug": win, "os": "windows"})
	zipPath := testZip(t)
	up := local.ok("upload_windows_build", map[string]any{"app": win, "zip_path": zipPath})
	wbuild := field(t, up, "Build ", " ")
	if text := local.ok("wait_for_build", map[string]any{"build": wbuild, "wait_seconds": 300}); !strings.Contains(text, "is ready") || !strings.Contains(text, "WindowsServer/GameServer.exe") {
		t.Fatalf("zip build: %s", text)
	}
	t.Logf("zip uploaded in parts and processed: %s", wbuild)
	local.ok("create_version", map[string]any{"app": win, "name": "1.0.0", "build": wbuild, "cpu": 1, "memory_gb": 2,
		"executable": "WindowsServer/GameServer.exe", "args": []any{"-port={{port.game}}"},
		"ports": []any{map[string]any{"name": "game", "protocol": "udp"}}, "ready_check": map[string]any{"type": "port", "port": "game"}})

	if _, err := exec.LookPath("bash"); err == nil {
		data, _ := os.ReadFile(zipPath)
		sum := sha256.Sum256(data)
		text := hosted.ok("begin_windows_upload", map[string]any{"app": win, "filename": "GameServer.zip", "bytes": len(data), "sha256": hex.EncodeToString(sum[:]), "zip_path": zipPath})
		script := text[strings.Index(text, "```bash\n")+8 : strings.LastIndex(text, "```")]
		out, err := exec.Command("bash", "-c", script).Output()
		if err != nil {
			t.Fatalf("upload script: %v", err)
		}
		var parts []any
		if err := json.Unmarshal(out, &parts); err != nil {
			t.Fatalf("script output %q: %v", out, err)
		}
		hbuild := field(t, text, "Build ", " ")
		hosted.ok("complete_windows_upload", map[string]any{"build": hbuild, "parts": parts})
		if text := hosted.ok("wait_for_build", map[string]any{"build": hbuild, "wait_seconds": 300}); !strings.Contains(text, "is ready") {
			t.Fatalf("hosted zip build: %s", text)
		}
		t.Logf("zip uploaded by the hosted server's script and processed: %s", hbuild)
	}
}

type mcpSession struct {
	t  *testing.T
	cs *mcp.ClientSession
}

func (m *mcpSession) call(tool string, args map[string]any) (string, bool) {
	m.t.Helper()
	return callTool(m.t, m.cs, tool, args)
}

func (m *mcpSession) ok(tool string, args map[string]any) string {
	m.t.Helper()
	text, isErr := m.call(tool, args)
	if isErr {
		m.t.Fatalf("%s: %s", tool, text)
	}
	return text
}

// testZip is a small Windows server folder: a program and a config file, in a subfolder as engines export them.
func testZip(t *testing.T) string {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{
		"WindowsServer/GameServer.exe":         "MZ" + strings.Repeat("\x00", 4096),
		"WindowsServer/Config/DefaultGame.ini": "[Game]\nMaxPlayers=10\n",
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "GameServer.zip")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// field is the text between before and after in a tool's summary.
func field(t *testing.T, text, before, after string) string {
	t.Helper()
	i := strings.Index(text, before)
	if i < 0 {
		t.Fatalf("no %q in %s", before, text)
	}
	rest := text[i+len(before):]
	j := strings.Index(rest, after)
	if j < 0 {
		t.Fatalf("no %q after %q in %s", after, before, text)
	}
	return rest[:j]
}

func firstID(t *testing.T, text string) string {
	t.Helper()
	i := strings.Index(text, `"id": "`)
	if i < 0 {
		t.Fatalf("no region in %s", text)
	}
	return field(t, text[i:], `"id": "`, `"`)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
