package server

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The Claude Code plugin (oss/mcp/plugin): Claude Code starts this program's mcp command with the key the developer
// typed into the plugin's settings (kept in the computer's own secure store, never in a file), and the plugin carries
// the same rules file as everything else (oss/client/skill), word for word.
func TestTheClaudeCodePluginRunsThisProgramWithOurRules(t *testing.T) {
	var p struct {
		Name       string `json:"name"`
		License    string `json:"license"`
		UserConfig map[string]struct {
			Type      string `json:"type"`
			Sensitive bool   `json:"sensitive"`
		} `json:"userConfig"`
		MCPServers map[string]struct {
			Command string            `json:"command"`
			Args    []string          `json:"args"`
			Env     map[string]string `json:"env"`
		} `json:"mcpServers"`
	}
	raw, err := os.ReadFile(filepath.Join("..", "plugin", "survival-servers", ".claude-plugin", "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	srv, ok := p.MCPServers["survival-servers"]
	if p.Name != "survival-servers" || p.License != "Apache-2.0" || !ok || srv.Command != "survivalservers" ||
		len(srv.Args) != 1 || srv.Args[0] != "mcp" || srv.Env["GS_API_KEY"] != "${user_config.api_key}" {
		t.Fatalf("plugin: %s", raw)
	}
	if k := p.UserConfig["api_key"]; k.Type != "string" || !k.Sensitive {
		t.Fatalf("the key is not kept as a secret: %+v", k)
	}
	mine, err := os.ReadFile(filepath.Join("..", "plugin", "survival-servers", "skills", "survival-servers", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	// The client's rules file: beside this module in oss/ and in studio-packages (../../client), at the top of
	// studio-go, where the client is the repository's root module (../../skill).
	var ours []byte
	for _, dir := range [][]string{{"..", "..", "client", "skill"}, {"..", "..", "skill"}} {
		if ours, err = os.ReadFile(filepath.Join(append(dir, "survival-servers", "SKILL.md")...)); err == nil {
			break
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(mine, ours) {
		t.Fatal("the plugin's SKILL.md differs from oss/client/skill/survival-servers/SKILL.md: copy it again")
	}
	var m struct {
		Plugins []struct {
			Name   string `json:"name"`
			Source string `json:"source"`
		} `json:"plugins"`
	}
	raw, err = os.ReadFile(filepath.Join("..", "plugin", ".claude-plugin", "marketplace.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &m); err != nil || len(m.Plugins) != 1 || m.Plugins[0].Name != p.Name {
		t.Fatalf("marketplace: %s %v", raw, err)
	}
	if _, err := os.Stat(filepath.Join("..", "plugin", m.Plugins[0].Source, ".claude-plugin", "plugin.json")); err != nil {
		t.Fatalf("the marketplace's source: %v", err)
	}
}

// One number for one release: the MCP Registry entry (server.json) and the Claude Code plugin carry the same version
// as this server, so a release moves all three in one change.
func TestServerJSONAndThePluginCarryThisVersion(t *testing.T) {
	for _, p := range [][]string{{"..", "server.json"}, {"..", "plugin", "survival-servers", ".claude-plugin", "plugin.json"}} {
		raw, err := os.ReadFile(filepath.Join(p...))
		if err != nil {
			t.Fatal(err)
		}
		var v struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatal(err)
		}
		if v.Version != Version {
			t.Errorf("%s says version %q, the server is %q: move them together", filepath.Join(p...), v.Version, Version)
		}
	}
}
