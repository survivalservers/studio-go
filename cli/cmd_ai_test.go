package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	api "github.com/survivalservers/studio-go"
)

// aiRun runs survivalservers ai with a fake home folder. Programs named in paths are "on the PATH"; running one is recorded.
func aiRun(t *testing.T, home string, stdin string, paths []string, runs *[][]string, args ...string) result {
	t.Helper()
	return aiRunWith(t, nil, home, stdin, paths, runs, args...)
}

// aiRunWith is aiRun with more environment variables. GS_API_KEY is always set, so a key that leaks into a file shows.
func aiRunWith(t *testing.T, extra map[string]string, home string, stdin string, paths []string, runs *[][]string, args ...string) result {
	t.Helper()
	vars := map[string]string{"HOME": home, "GS_API_KEY": "gsk_live_EXAMPLE_not_a_real_key"}
	for k, v := range extra {
		vars[k] = v
	}
	var out, errb bytes.Buffer
	code := Run(context.Background(), append([]string{"ai"}, args...), Env{
		Stdout: &out, Stderr: &errb, Stdin: strings.NewReader(stdin), Getenv: func(k string) string { return vars[k] },
		LookPath: func(name string) (string, error) {
			for _, p := range paths {
				if p == name {
					return "/usr/bin/" + name, nil
				}
			}
			return "", errors.New("not found")
		},
		Run: func(_ context.Context, name string, args ...string) error {
			*runs = append(*runs, append([]string{name}, args...))
			return nil
		},
	})
	return result{code, out.String(), errb.String()}
}

func mkdirs(t *testing.T, base string, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(base, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func readText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func writeText(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// walkFor fails when any file under dir holds the text.
func walkFor(t *testing.T, dir, text string) {
	t.Helper()
	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.Contains(readText(t, path), text) {
			t.Errorf("%s holds %q", path, text)
		}
		return nil
	})
}

func vscodeUser(home string) string {
	if runtime.GOOS == "darwin" {
		return filepath.Join("Library", "Application Support", "Code", "User")
	}
	return filepath.Join(".config", "Code", "User")
}

func TestAISetupOnThisComputer(t *testing.T) {
	home := t.TempDir()
	mkdirs(t, home, ".claude", ".codex", ".cursor", vscodeUser(home))
	var runs [][]string
	r := aiRun(t, home, "", []string{"claude"}, &runs, "setup", "--yes")
	// The hosted address is a stand-in while the hosted server is held: each tool gets the local server instead.
	if !standInAddress(defaultMCPURL) {
		t.Skip("the hosted address is real: TestAISetupHostedOnceTheAddressIsReal covers this")
	}
	r.must(t, 0, "This adds no key", "reads GS_API_KEY from your environment", keyNote)
	for _, gone := range []string{"is a placeholder", "sign in", "Needs login"} {
		if strings.Contains(r.stdout+r.stderr, gone) {
			t.Errorf("the local setup says %q: %s", gone, r.stdout)
		}
	}
	if want := "claude mcp add --transport stdio --scope user survival-servers --env GS_API_URL=" + localAPIURL + " -- survivalservers mcp"; len(runs) != 1 || strings.Join(runs[0], " ") != want {
		t.Errorf("Claude Code's own command: %v", runs)
	}
	if len(runs) == 1 {
		name, env, command := claudeAddReads(runs[0][1:])
		if name != api.SkillName || strings.Join(env, " ") != "GS_API_URL="+localAPIURL || strings.Join(command, " ") != "survivalservers mcp" {
			t.Errorf("Claude Code reads %v as name %q, env %v, command %v", runs[0], name, env, command)
		}
	}
	var cursor struct {
		MCPServers map[string]localServer `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(readText(t, filepath.Join(home, ".cursor", "mcp.json"))), &cursor); err != nil {
		t.Fatal(err)
	}
	if got := cursor.MCPServers[api.SkillName]; got.Type != "" || got.Command != "survivalservers" || strings.Join(got.Args, " ") != "mcp" || len(got.Env) != 1 || got.Env["GS_API_URL"] != localAPIURL {
		t.Errorf("Cursor's entry: %+v", got)
	}
	var vs struct {
		Servers map[string]localServer `json:"servers"`
	}
	if err := json.Unmarshal([]byte(readText(t, filepath.Join(home, vscodeUser(home), "mcp.json"))), &vs); err != nil {
		t.Fatal(err)
	}
	if got := vs.Servers[api.SkillName]; got.Type != "stdio" || got.Command != "survivalservers" || got.Env["GS_API_URL"] != localAPIURL {
		t.Errorf("VS Code's entry: %+v", got)
	}
	if got, want := readText(t, filepath.Join(home, ".codex", "config.toml")), "[mcp_servers.survival-servers]\ncommand = \"survivalservers\"\nargs = [\"mcp\"]\nenv = { GS_API_URL = \""+localAPIURL+"\" }\nenv_vars = [\"GS_API_KEY\"]\n"; got != want {
		t.Errorf("Codex's table: %q", got)
	}
	for _, f := range []string{".claude/skills/survival-servers/SKILL.md", ".agents/skills/survival-servers/SKILL.md"} {
		if readText(t, filepath.Join(home, f)) != api.Skill {
			t.Errorf("%s is not the rules file", f)
		}
	}
	walkFor(t, home, "gsk_")

	// A second run changes nothing, and asks Claude Code nothing once its settings list the server.
	writeText(t, filepath.Join(home, ".claude.json"), `{"mcpServers":{"survival-servers":{"type":"http"}}}`)
	before := readText(t, filepath.Join(home, ".cursor", "mcp.json"))
	runs = nil
	again := aiRun(t, home, "", []string{"claude"}, &runs, "setup", "--yes")
	again.must(t, 0, "already there")
	if strings.Contains(again.stdout, " added ") || strings.Contains(again.stdout, " updated ") || len(runs) != 0 {
		t.Errorf("a second run changed something (%v): %s", runs, again.stdout)
	}
	if readText(t, filepath.Join(home, ".cursor", "mcp.json")) != before {
		t.Error("a second run rewrote Cursor's file")
	}
}

func TestAISetupAsksFirst(t *testing.T) {
	home := t.TempDir()
	mkdirs(t, home, ".cursor")
	var runs [][]string
	for _, answer := range []string{"", "n\n", "no\n"} {
		r := aiRun(t, home, answer, nil, &runs, "setup")
		r.must(t, 0, "add survival-servers to ~/.cursor/mcp.json", "Make these changes? [y/N]", "Nothing was changed")
		if _, err := os.Stat(filepath.Join(home, ".cursor", "mcp.json")); err == nil {
			t.Fatalf("answer %q changed a file", answer)
		}
	}
	aiRun(t, home, "y\n", nil, &runs, "setup").must(t, 0, "added")
	if !strings.Contains(readText(t, filepath.Join(home, ".cursor", "mcp.json")), `"command":"survivalservers"`) {
		t.Error("yes made no change")
	}
}

func TestAISetupKeepsOtherEntriesToTheByte(t *testing.T) {
	cases := []struct{ name, before, after string }{
		{"others in place", "{\n\t\"mcpServers\": {\n\t\t\"linear\":   {\"url\": \"https://mcp.linear.app/mcp\"}\n\t},\n\t\"z\": [1,  2]\n}\n",
			"{\n\t\"mcpServers\": {\n    \"survival-servers\": {\"url\":\"U\"},\n\t\t\"linear\":   {\"url\": \"https://mcp.linear.app/mcp\"}\n\t},\n\t\"z\": [1,  2]\n}\n"},
		{"empty object", `{"mcpServers": {}}`, "{\"mcpServers\": {\n    \"survival-servers\": {\"url\":\"U\"}\n  }}"},
		{"no such key", `{"theme": "dark"}`, "{\n  \"mcpServers\": {\n    \"survival-servers\": {\"url\":\"U\"}\n  },\"theme\": \"dark\"}"},
		{"empty file object", `{}`, "{\n  \"mcpServers\": {\n    \"survival-servers\": {\"url\":\"U\"}\n  }\n}"},
		{"blank file", "  \n", "{\n  \"mcpServers\": {\n    \"survival-servers\": {\"url\":\"U\"}\n  }\n}\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, changed, err := addJSONEntry([]byte(c.before), "mcpServers", api.SkillName, map[string]string{"url": "U"})
			if err != nil || !changed {
				t.Fatalf("changed %v, %v", changed, err)
			}
			if string(got) != c.after {
				t.Errorf("got %q\nwant %q", got, c.after)
			}
			if !json.Valid(got) {
				t.Errorf("not JSON: %s", got)
			}
		})
	}
	if _, changed, err := addJSONEntry([]byte(`{"mcpServers":{"survival-servers":{"url":"narrow"}}}`), "mcpServers", api.SkillName, map[string]string{"url": "U"}); err != nil || changed {
		t.Errorf("an entry already there stays: %v %v", changed, err)
	}
	if _, _, err := addJSONEntry([]byte(`{"mcpServers":[]}`), "mcpServers", api.SkillName, nil); err == nil {
		t.Error("a list where the object should be is refused")
	}
}

func TestAISetupLeavesCommentedJSONAlone(t *testing.T) {
	home := t.TempDir()
	file := filepath.Join(home, vscodeUser(home), "mcp.json")
	const text = "{\n  // my servers\n  \"servers\": {}\n}\n"
	writeText(t, file, text)
	var runs [][]string
	aiRun(t, home, "", nil, &runs, "setup", "--yes").must(t, 0, "can't be read as plain JSON", `"survival-servers": {"type":"stdio","command":"survivalservers","args":["mcp"],"env":{"GS_API_URL":"`+localAPIURL+`"}}`)
	if readText(t, file) != text {
		t.Error("the file changed")
	}
}

func TestAISetupKeepsLinksAndModes(t *testing.T) {
	home := t.TempDir()
	real := filepath.Join(t.TempDir(), "dotfiles-mcp.json")
	writeText(t, real, `{"mcpServers":{}}`)
	mkdirs(t, home, ".cursor")
	link := filepath.Join(home, ".cursor", "mcp.json")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	var runs [][]string
	aiRun(t, home, "", nil, &runs, "setup", "--yes").must(t, 0, "added")
	if st, err := os.Lstat(link); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the link is gone: %v", err)
	}
	if !strings.Contains(readText(t, real), `"command":"survivalservers"`) {
		t.Error("the file behind the link did not change")
	}
	if st, _ := os.Stat(real); st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode().Perm())
	}
}

// claudeAddReads reads "mcp add ..." as Claude Code does (2.1.323): -e/--env takes every value up to the next
// option or "--", so an argument after its value that is not an option is read as one more value, not as the name.
func claudeAddReads(args []string) (name string, env, command []string) {
	var plain []string
	for i := 2; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--":
			command = args[i+1:]
			i = len(args)
		case a == "-e" || a == "--env":
			for i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				env = append(env, args[i])
			}
		case a == "-t" || a == "--transport" || a == "-s" || a == "--scope" || a == "-H" || a == "--header":
			i++
		default:
			plain = append(plain, a)
		}
	}
	if len(plain) > 0 {
		name = plain[0]
	}
	return name, env, command
}

func TestClaudeAddReadsTheOldOrderWrong(t *testing.T) {
	// The order setup used in 0.1.0: Claude Code took the name as a second --env value and refused the command.
	name, env, _ := claudeAddReads([]string{"mcp", "add", "--transport", "stdio", "--scope", "user", "--env", "GS_API_URL=x", api.SkillName, "--", "survivalservers", "mcp"})
	if name != "" || len(env) != 2 {
		t.Errorf("the model of Claude Code's parser does not reproduce the 0.1.0 refusal: name %q, env %v", name, env)
	}
}

func TestAISetupClaudeNotOnPath(t *testing.T) {
	home := t.TempDir()
	mkdirs(t, home, ".claude")
	var runs [][]string
	aiRun(t, home, "", nil, &runs, "setup", "--yes").must(t, 0, "Claude Code is not on your PATH. Run this yourself: claude mcp add --transport stdio --scope user survival-servers --env GS_API_URL="+localAPIURL+" -- survivalservers mcp")
	if len(runs) != 0 {
		t.Errorf("ran %v", runs)
	}
}

func TestAISetupFindsNothing(t *testing.T) {
	var runs [][]string
	aiRun(t, t.TempDir(), "", nil, &runs, "setup", "--yes").must(t, 1, "found no AI tool", "survivalservers ai setup --project")
}

func TestAISetupProject(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	writeText(t, "AGENTS.md", "# My game\n\nBuild with make.\n")
	writeText(t, ".codex/config.toml", "model = \"x\"\n")
	var runs [][]string
	r := aiRun(t, t.TempDir(), "", []string{"claude"}, &runs, "setup", "--project", "--yes")
	r.must(t, 0, "Commit these files", "add survival-servers to .mcp.json", "add our rules to AGENTS.md", keyNote)
	if len(runs) != 0 {
		t.Errorf("a repository's files are written, nothing is run: %v", runs)
	}
	for file, want := range map[string]string{
		".mcp.json":          `"survival-servers": {"type":"stdio","command":"survivalservers","args":["mcp"],"env":{"GS_API_URL":"` + localAPIURL + `"}}`,
		".cursor/mcp.json":   `"survival-servers": {"command":"survivalservers","args":["mcp"],"env":{"GS_API_URL":"` + localAPIURL + `"}}`,
		".vscode/mcp.json":   `"servers": {` + "\n" + `    "survival-servers": {"type":"stdio","command":"survivalservers"`,
		".codex/config.toml": "model = \"x\"\n\n[mcp_servers.survival-servers]\ncommand = \"survivalservers\"\nargs = [\"mcp\"]\nenv = { GS_API_URL = \"" + localAPIURL + "\" }\nenv_vars = [\"GS_API_KEY\"]\n",
		"AGENTS.md":          "# My game\n\nBuild with make.\n\n" + rulesStart + "\n\n# Survival Servers Studio",
		".claude/skills/survival-servers/SKILL.md": "name: survival-servers",
	} {
		if !strings.Contains(readText(t, file), want) {
			t.Errorf("%s lacks %q: %s", file, want, readText(t, file))
		}
	}
	if strings.Contains(readText(t, "AGENTS.md"), "description:") {
		t.Error("AGENTS.md carries the skill's front matter")
	}
	before := readText(t, "AGENTS.md")
	aiRun(t, t.TempDir(), "", nil, &runs, "setup", "--project", "--yes").must(t, 0, "already there")
	if readText(t, "AGENTS.md") != before {
		t.Error("a second run changed AGENTS.md")
	}
	// Older rules between our two lines are replaced, and the rest of the file stays.
	writeText(t, "AGENTS.md", "top\n\n"+rulesStart+"\nold rules\n"+rulesEnd+"\n\nbottom\n")
	aiRun(t, t.TempDir(), "", nil, &runs, "setup", "--project", "--yes").must(t, 0, "updated")
	got := readText(t, "AGENTS.md")
	if !strings.HasPrefix(got, "top\n\n"+rulesStart) || !strings.HasSuffix(got, rulesEnd+"\n\nbottom\n") || strings.Contains(got, "old rules") {
		t.Errorf("AGENTS.md: %s", got)
	}
}

func TestAISetupJSON(t *testing.T) {
	home := t.TempDir()
	mkdirs(t, home, ".cursor")
	var runs [][]string
	r := aiRun(t, home, "", nil, &runs, "setup", "--yes", "--json")
	r.must(t, 0)
	var out struct {
		Changes []struct{ Tool, Target, Result string } `json:"changes"`
		Next    []string                                `json:"next"`
	}
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil {
		t.Fatalf("%v: %s", err, r.stdout)
	}
	if len(out.Changes) != 2 || out.Changes[0].Tool != "Cursor" || out.Changes[0].Result != "added" || len(out.Next) != 1 {
		t.Errorf("%+v", out)
	}
}

// The hosted way comes back by itself once its address is real (or GS_MCP_URL names a real one): the browser sign-in,
// no command, no environment.
func TestAISetupHostedOnceTheAddressIsReal(t *testing.T) {
	home := t.TempDir()
	mkdirs(t, home, ".claude", ".codex", ".cursor", vscodeUser(home))
	const hosted = "https://ai.survivalservers.com/mcp"
	var runs [][]string
	r := aiRunWith(t, map[string]string{"GS_MCP_URL": hosted}, home, "", []string{"claude"}, &runs, "setup", "--yes")
	r.must(t, 0, "Each tool signs in with your browser", "Claude Code: run /mcp", "Codex: run codex mcp login survival-servers", "Cursor: open its MCP settings", "VS Code: run MCP: List Servers")
	if strings.Contains(r.stdout, keyNote) {
		t.Error("the hosted setup speaks of GS_API_KEY")
	}
	if len(runs) != 1 || strings.Join(runs[0], " ") != "claude mcp add --transport http --scope user survival-servers "+hosted {
		t.Errorf("Claude Code's own command: %v", runs)
	}
	if got := readText(t, filepath.Join(home, ".cursor", "mcp.json")); !strings.Contains(got, `"survival-servers": {"url":"`+hosted+`"}`) {
		t.Errorf("Cursor: %s", got)
	}
	if got := readText(t, filepath.Join(home, vscodeUser(home), "mcp.json")); !strings.Contains(got, `{"type":"http","url":"`+hosted+`"}`) {
		t.Errorf("VS Code: %s", got)
	}
	if got := readText(t, filepath.Join(home, ".codex", "config.toml")); got != "[mcp_servers.survival-servers]\nurl = \""+hosted+"\"\n" {
		t.Errorf("Codex: %q", got)
	}
	for _, a := range []string{"https://mcp.example.com/mcp", "https://mcp.studio.test/mcp", "https://x.invalid/mcp", "not an address"} {
		if !standInAddress(a) {
			t.Errorf("%s is a stand-in", a)
		}
	}
}

// codexKeyLine is the one settings line that may name GS_API_KEY: Codex hands a server it starts only a short list of
// its own environment, so its table lets GS_API_KEY through by name. It names the variable and never holds a value.
const codexKeyLine = `env_vars = ["GS_API_KEY"]`

// settingsKeyField is the first key field in a settings file's text, with Codex's one env_vars line (exactly as
// written, in Codex's config.toml only) left out; "" when there is none.
func settingsKeyField(path, text string) string {
	keyField := regexp.MustCompile(`(?i)GS_API_KEY|api[_-]?key|authorization|bearer|token|secret`)
	if filepath.Base(path) == "config.toml" && filepath.Base(filepath.Dir(path)) == ".codex" {
		var kept []string
		for _, l := range strings.Split(text, "\n") {
			if l != codexKeyLine {
				kept = append(kept, l)
			}
		}
		text = strings.Join(kept, "\n")
	}
	return keyField.FindString(text)
}

func TestSettingsKeyFieldLetsOnlyCodexsEnvVarsLineThrough(t *testing.T) {
	table := "[mcp_servers.survival-servers]\ncommand = \"survivalservers\"\nargs = [\"mcp\"]\n"
	codex := filepath.Join("home", ".codex", "config.toml")
	if m := settingsKeyField(codex, table+codexKeyLine+"\n"); m != "" {
		t.Errorf("Codex's env_vars line was taken for a key field: %q", m)
	}
	for why, c := range map[string][2]string{
		"a value":                  {codex, table + "env = { GS_API_KEY = \"gsk_live_EXAMPLE_not_a_real_key\" }\n"},
		"a value beside the line":  {codex, table + codexKeyLine + "\nenv = { GS_API_KEY = \"x\" }\n"},
		"a key field":              {codex, table + codexKeyLine + "\napi_key = \"x\"\n"},
		"a bearer token field":     {codex, table + "bearer_token_env_var = \"X\"\n"},
		"another name in the list": {codex, table + "env_vars = [\"GS_API_KEY\", \"GS_SECRET\"]\n"},
		"the line with more on it": {codex, table + codexKeyLine + " # more\n"},
		"the line in another file": {filepath.Join("home", ".cursor", "mcp.json"), codexKeyLine + "\n"},
		"the line in another toml": {filepath.Join("home", "other", "config.toml"), codexKeyLine + "\n"},
	} {
		if settingsKeyField(c[0], c[1]) == "" {
			t.Errorf("%s was let through", why)
		}
	}
}

// The key reaches the local server only through GS_API_KEY in its environment. No file setup writes, in either mode,
// ever holds a key or a key field (Codex's table names the variable in env_vars, never a value), and setup refuses an
// MCP address that carries one.
func TestAISetupNeverWritesAKey(t *testing.T) {
	// A key's shape, in any file; a key's field, in the settings files and the commands run. (The rules file tells an
	// agent to send the key in an Authorization header and names GS_API_KEY in examples: that is not a key.)
	keyShape := regexp.MustCompile(`gsk_`)
	keyField := regexp.MustCompile(`(?i)GS_API_KEY|api[_-]?key|authorization|bearer|token|secret`)
	check := func(t *testing.T, dir string, runs [][]string) {
		t.Helper()
		filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			text := readText(t, path)
			if m := keyShape.FindString(text); m != "" {
				t.Errorf("%s holds %q", path, m)
			}
			if ext := filepath.Ext(path); ext == ".json" || ext == ".toml" {
				if m := settingsKeyField(path, text); m != "" {
					t.Errorf("the settings file %s holds %q", path, m)
				}
			}
			return nil
		})
		for _, r := range runs {
			if m := keyField.FindString(strings.Join(r, " ")); m != "" || keyShape.MatchString(strings.Join(r, " ")) {
				t.Errorf("ran %v, which holds a key or a key field", r)
			}
		}
	}
	for _, extra := range []map[string]string{nil, {"GS_MCP_URL": "https://ai.survivalservers.com/mcp"}} {
		home := t.TempDir()
		mkdirs(t, home, ".claude", ".codex", ".cursor", vscodeUser(home))
		var runs [][]string
		aiRunWith(t, extra, home, "", []string{"claude", "codex"}, &runs, "setup", "--yes").must(t, 0)
		check(t, home, runs)

		project := t.TempDir()
		t.Chdir(project)
		aiRunWith(t, extra, t.TempDir(), "", nil, &runs, "setup", "--project", "--yes").must(t, 0)
		check(t, project, nil)
	}

	home := t.TempDir()
	mkdirs(t, home, ".cursor")
	var runs [][]string
	r := aiRunWith(t, map[string]string{"GS_MCP_URL": "https://ai.survivalservers.com/mcp?key=gsk_live_EXAMPLE"}, home, "", nil, &runs, "setup", "--yes")
	r.must(t, 1, "GS_MCP_URL holds an API key")
	if _, err := os.Stat(filepath.Join(home, ".cursor", "mcp.json")); err == nil {
		t.Error("a refused setup wrote a file")
	}
}
