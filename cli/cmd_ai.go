package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	api "github.com/survivalservers/studio-go"
)

// survivalservers ai setup: adds our MCP server and rules file to the AI tools on this computer, or to one repository. It never
// writes a key (each tool signs in with a browser) and it changes only our own entries. An entry that is already
// there stays as it is, so a team's narrower address survives a second run.

// defaultMCPURL is the hosted MCP server: a placeholder like the docs' (site/brand.mjs). The hosted server is held for
// later, so while this (or GS_MCP_URL) is a stand-in, setup adds the local server instead: each tool
// starts survivalservers mcp on this computer. The hosted way comes back by itself once the address is real.
const defaultMCPURL = "https://mcp.example.com/mcp"

// localAPIURL is the public API's address (site/brand.mjs apiHost), written as GS_API_URL in the local server's entry
// so the entry names the address it calls; the server uses the same address when GS_API_URL is not set.
const localAPIURL = "https://studio.survivalservers.com"

// localCommand is the program a tool starts for the local server, with the argument "mcp".
const localCommand = "survivalservers"

// localServer is a tool's entry for the local server. It names no key, by design: the local server reads GS_API_KEY
// from the environment its tool starts it in, and no file setup writes ever holds a key.
type localServer struct {
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
}

func (s *aiSetup) localEntry(typ string) localServer {
	return localServer{Type: typ, Command: localCommand, Args: []string{"mcp"}, Env: map[string]string{"GS_API_URL": localAPIURL}}
}

// keyNote is the one line setup prints about the key for the local server.
const keyNote = "Set GS_API_KEY in the environment you start your AI tools from, to an agent key from the panel's API keys page: the local server reads it there, and no file setup writes holds it."

// standInAddress says whether an MCP address is a stand-in (a reserved name, RFC 2606 and 6761) or unreadable.
func standInAddress(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return true
	}
	h := strings.ToLower(u.Hostname())
	for _, r := range []string{"example", "example.com", "example.net", "example.org", "invalid", "test"} {
		if h == r || strings.HasSuffix(h, "."+r) {
			return true
		}
	}
	return false
}

const (
	rulesStart = "<!-- " + api.SkillName + " rules: start -->"
	rulesEnd   = "<!-- " + api.SkillName + " rules: end -->"
)

// aiChange is one thing setup adds: shown first, then done.
type aiChange struct {
	Tool   string `json:"tool"`
	Target string `json:"target"`
	Result string `json:"result"`
	verb   string
	do     func(ctx context.Context) (string, error)
}

const (
	resultAdded   = "added"
	resultThere   = "already there"
	resultUpdated = "updated"
	resultByHand  = "not changed"
)

type aiSetup struct {
	c    *ctxt
	home string
	root string // the repository's folder with --project
	url  string
	// local: add the local server (survivalservers mcp) rather than the hosted one, whose address is still a stand-in.
	local bool
	hint  []string // what to do by hand, after the changes
}

func cmdAISetup(ctx context.Context, c *ctxt, args []string) error {
	project := c.fs.Bool("project", false, "set up the repository in this folder instead of this computer")
	yes := c.fs.Bool("yes", false, "make the changes without asking")
	if _, err := c.parse(args, "[--project] [--yes]", 0, 0); err != nil {
		return err
	}
	s := &aiSetup{c: c, home: firstOf(c.env.Getenv("HOME"), c.env.Getenv("USERPROFILE")), url: firstOf(c.env.Getenv("GS_MCP_URL"), defaultMCPURL)}
	if strings.Contains(s.url, "gsk_") {
		return errors.New("GS_MCP_URL holds an API key, which setup would write into your settings files: give it the MCP server's address alone, and revoke that key")
	}
	s.local = standInAddress(s.url)
	var changes []aiChange
	var next []string
	if *project {
		dir, err := os.Getwd()
		if err != nil {
			return err
		}
		s.root = dir
		changes, next = s.projectPlan()
	} else {
		if s.home == "" {
			return errors.New("HOME is not set, so your AI tools can't be found")
		}
		changes, next = s.computerPlan()
		if len(changes) == 0 {
			return failedError{"found no AI tool (Claude Code, Codex, Cursor or VS Code). To set up one repository, run survivalservers ai setup --project"}
		}
	}

	// With --json the answer on stdout stays JSON, so the plan goes to stderr.
	out := c.env.Stdout
	if c.json {
		out = c.env.Stderr
	}
	if s.local {
		fmt.Fprintln(out, "This adds no key. Each tool starts our MCP server on this computer, which reads GS_API_KEY from your environment.")
	} else {
		fmt.Fprintln(out, "This adds no key. Each tool signs in with your browser.")
	}
	fmt.Fprintln(out)
	tool := ""
	for _, ch := range changes {
		name := ch.Tool
		if name == tool {
			name = ""
		}
		tool = ch.Tool
		fmt.Fprintf(out, "  %-12s %s %s\n", name, ch.verb, ch.Target)
	}
	fmt.Fprintln(out)
	if !*yes {
		fmt.Fprint(out, "Make these changes? [y/N] ")
		line, _ := bufio.NewReader(c.env.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			fmt.Fprintln(out, "\nNothing was changed. To skip this question, add --yes.")
			return nil
		}
	}
	var failed error
	for i := range changes {
		res, err := changes[i].do(ctx)
		if err != nil {
			res = "failed: " + err.Error()
			failed = failedError{"some changes failed"}
		}
		changes[i].Result = res
		fmt.Fprintf(out, "  %-12s %-14s %s\n", changes[i].Tool, res, changes[i].Target)
	}
	if len(s.hint) > 0 || len(next) > 0 {
		fmt.Fprintln(out)
	}
	for _, line := range append(s.hint, next...) {
		fmt.Fprintln(out, line)
	}
	if c.json {
		if err := c.printJSON(map[string]any{"changes": changes, "next": append(s.hint, next...)}); err != nil {
			return err
		}
	}
	return failed
}

func (s *aiSetup) dirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}

func (s *aiSetup) onPath(name string) bool {
	look := s.c.env.LookPath
	if look == nil {
		look = exec.LookPath
	}
	_, err := look(name)
	return err == nil
}

// short is a path as the list of changes shows it: from the repository's folder, or from ~.
func (s *aiSetup) short(path string) string {
	if s.root != "" {
		if rel, err := filepath.Rel(s.root, path); err == nil {
			return filepath.ToSlash(rel)
		}
	}
	if rest, ok := strings.CutPrefix(path, s.home+string(filepath.Separator)); ok {
		return "~/" + filepath.ToSlash(rest)
	}
	return path
}

// computerPlan is one change per tool found on this computer, each by the route its docs give, then the rules: Claude
// Code reads skills from its own folder, and Codex, Cursor and VS Code all read ~/.agents/skills.
func (s *aiSetup) computerPlan() (changes []aiChange, next []string) {
	env := s.c.env.Getenv
	name := api.SkillName
	var rules []aiChange
	shared := false

	claudeDir := firstOf(env("CLAUDE_CONFIG_DIR"), filepath.Join(s.home, ".claude"))
	// The sign-in steps are the hosted server's; the local one needs only the key in the environment.
	signIn := func(line string) {
		if !s.local {
			next = append(next, line)
		}
	}
	if s.onPath("claude") || s.dirExists(claudeDir) {
		changes = append(changes, s.claudeServer(claudeDir))
		rules = append(rules, s.rulesFile(filepath.Join(claudeDir, "skills", name, "SKILL.md")))
		signIn("Claude Code: run /mcp and choose " + name + " to sign in.")
	}
	codexDir := firstOf(env("CODEX_HOME"), filepath.Join(s.home, ".codex"))
	if s.onPath("codex") || s.dirExists(codexDir) {
		changes = append(changes, s.tomlServer("Codex", filepath.Join(codexDir, "config.toml")))
		signIn("Codex: run codex mcp login " + name + " to sign in.")
		shared = true
	}
	if dir := filepath.Join(s.home, ".cursor"); s.dirExists(dir) {
		changes = append(changes, s.jsonServer("Cursor", filepath.Join(dir, "mcp.json"), "mcpServers", s.entry("", map[string]string{"url": s.url})))
		signIn("Cursor: open its MCP settings and click Needs login.")
		shared = true
	}
	if dir := s.vscodeDir(); s.dirExists(dir) {
		changes = append(changes, s.jsonServer("VS Code", filepath.Join(dir, "mcp.json"), "servers", s.entry("stdio", map[string]string{"type": "http", "url": s.url})))
		signIn("VS Code: run MCP: List Servers, choose " + name + " and start it.")
		shared = true
	}
	if shared {
		rules = append(rules, s.rulesFile(filepath.Join(s.home, ".agents", "skills", name, "SKILL.md")))
	}
	if s.local && len(changes) > 0 {
		next = append(next, keyNote)
	}
	return append(changes, rules...), next
}

// vscodeDir is VS Code's user folder, where its mcp.json sits.
func (s *aiSetup) vscodeDir() string {
	env := s.c.env.Getenv
	switch runtime.GOOS {
	case "windows":
		return filepath.Join(firstOf(env("APPDATA"), filepath.Join(s.home, "AppData", "Roaming")), "Code", "User")
	case "darwin":
		return filepath.Join(s.home, "Library", "Application Support", "Code", "User")
	}
	return filepath.Join(firstOf(env("XDG_CONFIG_HOME"), filepath.Join(s.home, ".config")), "Code", "User")
}

// projectPlan is the same for one repository, whichever tools its people use.
func (s *aiSetup) projectPlan() (changes []aiChange, next []string) {
	dir := s.root
	http := map[string]string{"type": "http", "url": s.url}
	next = []string{"Commit these files, so everyone on the project gets them."}
	if s.local {
		next = append(next, keyNote)
	}
	return []aiChange{
		s.jsonServer("Claude Code", filepath.Join(dir, ".mcp.json"), "mcpServers", s.entry("stdio", http)),
		s.tomlServer("Codex", filepath.Join(dir, ".codex", "config.toml")),
		s.jsonServer("Cursor", filepath.Join(dir, ".cursor", "mcp.json"), "mcpServers", s.entry("", map[string]string{"url": s.url})),
		s.jsonServer("VS Code", filepath.Join(dir, ".vscode", "mcp.json"), "servers", s.entry("stdio", http)),
		s.rulesFile(filepath.Join(dir, ".claude", "skills", api.SkillName, "SKILL.md")),
		s.agentsFile(filepath.Join(dir, "AGENTS.md")),
	}, next
}

// entry is a JSON tool's entry: the local server (with this type, "" for none) or the hosted one as given.
func (s *aiSetup) entry(localType string, hosted map[string]string) any {
	if s.local {
		return s.localEntry(localType)
	}
	return hosted
}

// claudeServer uses Claude Code's own command: Claude Code rewrites its settings file while it runs. An entry that is
// already there is read from that file, so a second run asks nothing of the network.
func (s *aiSetup) claudeServer(dir string) aiChange {
	name := api.SkillName
	args := []string{"mcp", "add", "--transport", "http", "--scope", "user", name, s.url}
	if s.local {
		// The name goes before --env: Claude Code's --env takes any number of values, so a name after it is read as one
		// more value and refused ("Invalid environment variable format", Claude Code 2.1.323).
		args = []string{"mcp", "add", "--transport", "stdio", "--scope", "user", name, "--env", "GS_API_URL=" + localAPIURL, "--", localCommand, "mcp"}
	}
	line := "claude " + strings.Join(args, " ")
	return aiChange{Tool: "Claude Code", verb: "run", Target: line, do: func(ctx context.Context) (string, error) {
		// ~/.claude.json, or .claude.json inside a chosen settings folder.
		file := filepath.Join(s.home, ".claude.json")
		if s.c.env.Getenv("CLAUDE_CONFIG_DIR") != "" {
			file = filepath.Join(dir, ".claude.json")
		}
		if hasServer(file, name) {
			return resultThere, nil
		}
		if !s.onPath("claude") {
			s.hint = append(s.hint, "Claude Code is not on your PATH. Run this yourself: "+line)
			return resultByHand, nil
		}
		run := s.c.env.Run
		if run == nil {
			run = func(ctx context.Context, name string, args ...string) error {
				out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
				if err != nil {
					return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
				}
				return nil
			}
		}
		if err := run(ctx, "claude", args...); err != nil {
			return "", err
		}
		return resultAdded, nil
	}}
}

// hasServer says whether a settings file lists the server at its top level.
func hasServer(file, name string) bool {
	data, err := os.ReadFile(file)
	if err != nil {
		return false
	}
	var f struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if json.Unmarshal(data, &f) != nil {
		return false
	}
	_, ok := f.MCPServers[name]
	return ok
}

// jsonServer adds our entry under key in a JSON settings file and leaves every other byte alone. A file that is not
// plain JSON (VS Code's may hold comments) is left too, with the entry to paste.
func (s *aiSetup) jsonServer(tool, file, key string, value any) aiChange {
	return aiChange{Tool: tool, verb: "add " + api.SkillName + " to", Target: s.short(file), do: func(context.Context) (string, error) {
		old, err := os.ReadFile(file)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		next, changed, err := addJSONEntry(old, key, api.SkillName, value)
		if err != nil {
			entry, _ := json.Marshal(value)
			s.hint = append(s.hint, fmt.Sprintf("%s can't be read as plain JSON. Add this under %q yourself: %q: %s", s.short(file), key, api.SkillName, entry))
			return resultByHand, nil
		}
		if !changed {
			return resultThere, nil
		}
		if err := replaceFile(file, next); err != nil {
			return "", err
		}
		return resultAdded, nil
	}}
}

// addJSONEntry returns the file with "name": value added to the object at the top-level key, keeping every other byte,
// and false when name is already there.
func addJSONEntry(file []byte, key, name string, value any) ([]byte, bool, error) {
	entry, err := json.Marshal(value)
	if err != nil {
		return nil, false, err
	}
	line := fmt.Sprintf("%q: %s", name, entry)
	if len(bytes.TrimSpace(file)) == 0 {
		return fmt.Appendf(nil, "{\n  %q: {\n    %s\n  }\n}\n", key, line), true, nil
	}
	if !json.Valid(file) {
		return nil, false, errors.New("not plain JSON")
	}
	insert := func(at int, text string) ([]byte, bool, error) {
		return append(append(append([]byte{}, file[:at]...), text...), file[at:]...), true, nil
	}
	dec := json.NewDecoder(bytes.NewReader(file))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil, false, errors.New("not a JSON object")
	}
	root := int(dec.InputOffset())
	rootEmpty := !dec.More()
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			return nil, false, err
		}
		if k != key {
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return nil, false, err
			}
			continue
		}
		if t, err := dec.Token(); err != nil || t != json.Delim('{') {
			return nil, false, fmt.Errorf("%s is not an object", key)
		}
		open := int(dec.InputOffset())
		empty := !dec.More()
		for dec.More() {
			n, err := dec.Token()
			if err != nil {
				return nil, false, err
			}
			if n == name {
				return file, false, nil
			}
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return nil, false, err
			}
		}
		if empty {
			return insert(open, "\n    "+line+"\n  ")
		}
		return insert(open, "\n    "+line+",")
	}
	text := fmt.Sprintf("\n  %q: {\n    %s\n  }", key, line)
	if rootEmpty {
		return insert(root, text+"\n")
	}
	return insert(root, text+",")
}

// tomlServer adds Codex's table at the end of its config.toml. TOML tables can come in any order, so nothing else moves.
func (s *aiSetup) tomlServer(tool, file string) aiChange {
	name := api.SkillName
	return aiChange{Tool: tool, verb: "add " + name + " to", Target: s.short(file), do: func(context.Context) (string, error) {
		old, err := os.ReadFile(file)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		for _, l := range strings.Split(string(old), "\n") {
			switch strings.TrimSpace(l) {
			case "[mcp_servers." + name + "]", `[mcp_servers."` + name + `"]`:
				return resultThere, nil
			}
		}
		text := strings.TrimRight(string(old), "\n")
		if text != "" {
			text += "\n\n"
		}
		if s.local {
			// Codex hands a server it starts only a short list of its own environment (HOME, PATH, USER and a few more),
			// so GS_API_KEY reaches the local server only when env_vars names it.
			text += fmt.Sprintf("[mcp_servers.%s]\ncommand = %q\nargs = [\"mcp\"]\nenv = { GS_API_URL = %q }\nenv_vars = [\"GS_API_KEY\"]\n", name, localCommand, localAPIURL)
		} else {
			text += fmt.Sprintf("[mcp_servers.%s]\nurl = %q\n", name, s.url)
		}
		if err := replaceFile(file, []byte(text)); err != nil {
			return "", err
		}
		return resultAdded, nil
	}}
}

// rulesFile writes the rules as a skill. The file is ours, so a newer survivalservers brings it up to date.
func (s *aiSetup) rulesFile(file string) aiChange {
	return aiChange{Tool: "Rules", verb: "write", Target: s.short(file), do: func(context.Context) (string, error) {
		return writeIfChanged(file, api.Skill)
	}}
}

// agentsFile puts the rules between our two lines in a repository's AGENTS.md, which most AI tools read.
func (s *aiSetup) agentsFile(file string) aiChange {
	return aiChange{Tool: "Rules", verb: "add our rules to", Target: s.short(file), do: func(context.Context) (string, error) {
		old, err := os.ReadFile(file)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		return writeIfChanged(file, withRules(string(old)))
	}}
}

// withRules is the AGENTS.md text with our block in it: replaced where it is, or added at the end.
func withRules(text string) string {
	block := rulesStart + "\n\n" + strings.TrimSpace(api.SkillBody()) + "\n\n" + rulesEnd + "\n"
	if i := strings.Index(text, rulesStart); i >= 0 {
		if j := strings.Index(text[i:], rulesEnd); j >= 0 {
			rest := strings.TrimPrefix(text[i+j+len(rulesEnd):], "\n")
			return text[:i] + block + rest
		}
	}
	if strings.TrimSpace(text) == "" {
		return block
	}
	return strings.TrimRight(text, "\n") + "\n\n" + block
}

func writeIfChanged(file, text string) (string, error) {
	old, err := os.ReadFile(file)
	if err == nil && string(old) == text {
		return resultThere, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if werr := replaceFile(file, []byte(text)); werr != nil {
		return "", werr
	}
	if err == nil {
		return resultUpdated, nil
	}
	return resultAdded, nil
}

// replaceFile writes a new file and renames it over the old one, so a failure leaves the old one whole. A symbolic
// link stays a link: the file it points at is what changes.
func replaceFile(file string, data []byte) error {
	if real, err := filepath.EvalSymlinks(file); err == nil {
		file = real
	}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(file); err == nil {
		mode = st.Mode().Perm()
	}
	dir := filepath.Dir(file)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".survivalservers-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), file)
}
