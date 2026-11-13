package server

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// TestPractice runs the tools against the practice server (GS_PRACTICE_URL), which checks every request against
// the API file and answers with its examples: it proves each tool sends what the API accepts. Not run otherwise.
func TestPractice(t *testing.T) {
	base := os.Getenv("GS_PRACTICE_URL")
	if base == "" {
		t.Skip("GS_PRACTICE_URL is not set")
	}
	// get_registry_login logs Docker in; here a stand-in takes the password instead.
	cfg := Config{Key: "gsk_live_EXAMPLE_not_a_real_key", Local: true, MaxTTL: 2 * time.Hour, Poll: 20 * time.Millisecond,
		Docker: func(context.Context, io.Reader, ...string) ([]byte, error) { return []byte("Login Succeeded\n"), nil }}
	cfg.APIURL = base
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cs := connect(t, s)
	zero := 0
	steps := []struct {
		tool string
		args map[string]any
	}{
		{"get_account", nil},
		{"list_regions", map[string]any{"os": "windows", "tier": "standard", "continent": "north-america"}},
		{"list_apps", nil},
		{"create_app", map[string]any{"name": "Brawl Server", "slug": "brawl-server", "os": "windows", "tier": "standard", "regions": []any{"us-central"}, "continents": []any{"north-america"}}},
		{"create_linux_build", map[string]any{"app": "brawl-server", "image": "registry.fenmarch.example/brinewatch-arena:1.4.2"}},
		{"wait_for_build", map[string]any{"build": "bld_a01JA80M1N3B5V7C9X1Z3L5K7J", "wait_seconds": zero}},
		{"create_version", map[string]any{"app": "brawl-server", "name": "1.4.2", "build": "bld_a01JA80M1N3B5V7C9X1Z3L5K7J", "cpu": 2, "memory_gb": 4,
			"executable": "WindowsServer/BrinewatchServer.exe", "args": []any{"-port={{port.game}}", "-log"}, "env": map[string]any{"MODE": "ranked"},
			"ports":       []any{map[string]any{"name": "game", "protocol": "udp"}, map[string]any{"name": "query", "protocol": "udp"}},
			"ready_check": map[string]any{"type": "log", "pattern": "Server ready"}, "stop_method": "ctrl_break", "stop_shutdown_time": "30s",
			"boot_timeout": "5m", "log_paths": []any{"Saved/Logs/*.log"}, "artifact_paths": []any{"Saved/Results"}, "crash_dump": "mini",
			"restart": "never", "max_duration": "2h", "regions": []any{"*"}, "compat": []any{"playfab"}}},
		{"list_versions", map[string]any{"app": "brawl-server"}},
		{"wait_for_version", map[string]any{"app": "brawl-server", "version": "1.4.2", "wait_seconds": zero}},
		{"set_branch", map[string]any{"app": "brawl-server", "branch": "live", "version": "1.4.2"}},
		{"start_game_server", map[string]any{"app": "brawl-server", "branch": "live", "regions": []any{"us-central", "us-east"}, "continents": []any{"north-america"},
			"env": map[string]any{"MATCH_MODE": "ranked"}, "labels": map[string]any{"match_id": "88231"}, "external_id": "88231", "wait_seconds": zero}},
		{"list_game_servers", map[string]any{"app": "brawl-server", "labels": []any{"queue:ranked"}, "region": "us-central"}},
		{"wait_for_game_server", map[string]any{"id": "gs_a01JA8X9K2Q7M3T5V6W8Y0Z1B2", "wait_seconds": zero}},
		{"diagnose_game_server", map[string]any{"id": "gs_a01JA8X9K2Q7M3T5V6W8Y0Z1B2"}},
		{"read_game_server_log", map[string]any{"id": "gs_a01JA8X9K2Q7M3T5V6W8Y0Z1B2", "tail": 50}},
		{"list_game_server_files", map[string]any{"id": "gs_a01JA8X9K2Q7M3T5V6W8Y0Z1B2", "path": "Saved/Logs"}},
		{"stop_game_server", map[string]any{"id": "gs_a01JA8X9K2Q7M3T5V6W8Y0Z1B2", "shutdown_time": "30s"}},
		{"create_persistent_server", map[string]any{"app": "brawl-server", "name": "EU PvE 1", "region": "europe-central", "branch": "live",
			"save_paths": []any{"Saved/SaveGames"}, "backup": map[string]any{"every": "10m", "keep_for": "30d"},
			"restart":    map[string]any{"schedule": "0 6 * * *", "timezone": "Europe/Berlin"},
			"when_empty": map[string]any{"after": "10m", "then": "pause", "stop_after": "1h"}, "wait_seconds": zero}},
		{"list_persistent_servers", map[string]any{"app": "brawl-server", "status": []any{"running", "paused"}}},
		{"wait_for_persistent_server", map[string]any{"id": "ps_e01JA8YQ3W5E7R9T1Y3U5I7O9P", "wait_seconds": zero}},
		{"back_up_persistent_server", map[string]any{"id": "ps_e01JA8YQ3W5E7R9T1Y3U5I7O9P"}},
		{"list_backups", map[string]any{"id": "ps_e01JA8YQ3W5E7R9T1Y3U5I7O9P"}},
		{"stop_persistent_server", map[string]any{"id": "ps_e01JA8YQ3W5E7R9T1Y3U5I7O9P", "shutdown_time": "60s", "wait_seconds": zero}},
		{"plan_rollback", map[string]any{"ids": []string{"ps_e01JA8YQ3W5E7R9T1Y3U5I7O9P"}, "backup": "bkp_e01JA8YT2B4N6M8Q0W2E4R6T8Y"}},
		{"start_persistent_server", map[string]any{"id": "ps_e01JA8YQ3W5E7R9T1Y3U5I7O9P", "wait_seconds": zero}},
		{"update_persistent_server", map[string]any{"id": "ps_e01JA8YQ3W5E7R9T1Y3U5I7O9P", "version": "2.0.2", "apply": "next_restart"}},
		{"update_persistent_server", map[string]any{"id": "ps_e01JA8YQ3W5E7R9T1Y3U5I7O9P", "when_empty": map[string]any{"enabled": false}}},
		{"set_pool", map[string]any{"app": "brawl-server", "region": "us-central", "branch": "live", "cpu": 2, "memory_gb": 4, "mode": "auto", "min": 2, "max": 40}},
		{"list_pools", map[string]any{"app": "brawl-server"}},
		{"remove_pool", map[string]any{"app": "brawl-server", "region": "us-central"}},
		{"get_usage", map[string]any{"from": "2026-10-01", "group_by": []any{"app", "region"}, "granularity": "day"}},
		{"get_prices", nil},
		{"estimate_cost", map[string]any{"cpu": 2, "memory_gb": 4, "hours": 1, "count": 10}},
		{"estimate_cost", map[string]any{"kind": "persistent_server", "cpu": 4, "hours": 720}},
		{"get_registry_login", nil},
	}
	for _, st := range steps {
		text, isErr := callTool(t, cs, st.tool, st.args)
		if isErr {
			t.Errorf("%s refused by the practice server:\n%s", st.tool, text)
			continue
		}
		if strings.TrimSpace(text) == "" {
			t.Errorf("%s answered nothing", st.tool)
		}
	}
}
