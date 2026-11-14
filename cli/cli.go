// Package cli is the command-line tool for the public game server API: upload builds, create versions, start and
// stop game servers, follow their logs, run persistent servers and ready servers, from a studio's own computer or its
// CI.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"runtime"
	"slices"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	api "github.com/survivalservers/studio-go"
)

// Version is this tool's version: the newest one in CHANGELOG.md (TestVersionIsTheChangelogsNewest keeps them equal).
// release.sh stamps the same number and refuses any other, so a copy installed with go install from the module's
// cli/vX.Y.Z tag and the released program X.Y.Z say the same.
var Version = "0.1.2"

// Exit codes.
const (
	exitOK      = 0
	exitFailed  = 1 // the API refused, or what we waited for failed
	exitUsage   = 2 // the command line is wrong
	exitTimeout = 3 // a wait ran out before the object settled
	exitRetry   = 4 // the API was busy or out of reach (a rate limit, a brief outage, the network): worth a retry
)

// isHelp is a request for help: -h, -help or --help.
func isHelp(a string) bool { return a == "-h" || a == "-help" || a == "--help" }

// helpAsked ends a command that was asked for its help: its usage goes to stdout and it exits 0.
type helpAsked struct{}

func (helpAsked) Error() string { return "help" }

// Env is what the tool reads from its surroundings, so tests can give their own.
type Env struct {
	Stdin          io.Reader
	Stdout, Stderr io.Writer
	Getenv         func(string) string
	HTTP           *http.Client // API calls (tests); nil for the default
	Storage        *http.Client // upload parts and downloads (tests)
	Poll           time.Duration
	// Docker runs docker with the given arguments and stdin (registry login); nil runs the real one.
	Docker func(ctx context.Context, stdin io.Reader, args ...string) error
	// LookPath and Run find and run an AI tool's own command (ai setup); nil uses the real ones.
	LookPath func(string) (string, error)
	Run      func(ctx context.Context, name string, args ...string) error
}

type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func usagef(format string, a ...any) error { return usageError{fmt.Sprintf(format, a...)} }

type timeoutError struct{ what string }

func (e timeoutError) Error() string { return e.what }

// failedError is a wait that ended in a failed state (the object is printed first).
type failedError struct{ msg string }

func (e failedError) Error() string { return e.msg }

// Run runs one command line and returns the exit code.
func Run(ctx context.Context, args []string, env Env) int {
	if env.Getenv == nil {
		env.Getenv = os.Getenv
	}
	if env.Stdin == nil {
		env.Stdin = os.Stdin
	}
	if env.Poll == 0 {
		env.Poll = 3 * time.Second
	}
	for _, a := range args {
		if strings.HasPrefix(a, "gsk_") || strings.Contains(a, "=gsk_") {
			fmt.Fprintln(env.Stderr, "survivalservers: an API key was passed on the command line, where other programs and your shell history can read it. Set GS_API_KEY (or use --key-file) instead, and revoke that key.")
			return exitUsage
		}
	}
	if len(args) > 1 && args[0] == "help" {
		// "help game-servers" or "help game-servers start": the same as asking that command for its help.
		return Run(ctx, append(append([]string{}, args[1:]...), "--help"), env)
	}
	if len(args) == 0 || args[0] == "help" || isHelp(args[0]) {
		fmt.Fprint(env.Stdout, helpText)
		return exitOK
	}
	if args[0] == "version" || args[0] == "--version" {
		fmt.Fprintln(env.Stdout, "survivalservers", Version)
		return exitOK
	}
	switch args[0] {
	case "dev", "mcp":
		run := runDev
		if args[0] == "mcp" {
			run = runMCP
		}
		code := run(ctx, args[1:], env)
		if code == exitUsage && slices.ContainsFunc(args[1:], isHelp) {
			return exitOK // its flags were printed, as asked
		}
		return code
	}
	group, rest := args[0], args[1:]
	cmds, ok := commands[group]
	if !ok {
		fmt.Fprintf(env.Stderr, "survivalservers: no command %q (survivalservers help lists them)\n", group)
		return exitUsage
	}
	if _, single := cmds[""]; !single && len(rest) > 0 && isHelp(rest[0]) {
		fmt.Fprintf(env.Stdout, "survivalservers %s ACTION: one of %s\nsurvivalservers %s ACTION --help shows an action's flags.\n", group, actions(cmds), group)
		return exitOK
	}
	action := ""
	if _, single := cmds[""]; single && (len(rest) == 0 || cmds[rest[0]] == nil) {
		action = ""
	} else if len(rest) > 0 {
		action, rest = rest[0], rest[1:]
	}
	cmd := cmds[action]
	if cmd == nil {
		fmt.Fprintf(env.Stderr, "survivalservers %s: no action %q; one of %s\n", group, action, actions(cmds))
		return exitUsage
	}
	c := &ctxt{env: env, name: strings.TrimSpace("survivalservers " + group + " " + action)}
	c.fs = flag.NewFlagSet(c.name, flag.ContinueOnError)
	c.fs.SetOutput(io.Discard)
	c.fs.BoolVar(&c.json, "json", false, "print the API's JSON instead of a summary")
	c.fs.StringVar(&c.apiURL, "api", "", "API address (default GS_API_URL, else https://studio.survivalservers.com)")
	c.fs.StringVar(&c.keyFile, "key-file", "", "read the API key from this file instead of GS_API_KEY")
	c.fs.StringVar(&c.idem, "idempotency-key", "", "send this Idempotency-Key, so a re-run of the same step (in CI, say) does the work once")
	err := cmd(ctx, c, rest)
	var ue usageError
	var te timeoutError
	var fe failedError
	var problem *api.Problem
	switch {
	case err == nil:
		return exitOK
	case errors.As(err, &helpAsked{}):
		fmt.Fprint(env.Stdout, c.usage())
		return exitOK
	case errors.As(err, &ue):
		fmt.Fprintf(env.Stderr, "%s: %s\n", c.name, ue.msg)
		if u := c.usage(); u != "" {
			fmt.Fprintf(env.Stderr, "\n%s", u)
		}
		return exitUsage
	case errors.As(err, &te):
		fmt.Fprintf(env.Stderr, "%s: %s\n", c.name, te.what)
		return exitTimeout
	case errors.As(err, &fe):
		fmt.Fprintf(env.Stderr, "%s: %s\n", c.name, fe.msg)
		return exitFailed
	case c.json && errors.As(err, &problem):
		// The API's own error, for a script to read (it was plain text with --json too).
		out := map[string]any{"status": problem.Status, "code": problem.Code, "title": problem.Title, "detail": problem.Detail,
			"request_id": problem.RequestID, "errors": problem.Errors, "next": api.NextStep[problem.Code]}
		if problem.RetryAfter != "" {
			out["retry_after"] = problem.RetryAfter
		}
		_ = json.NewEncoder(env.Stderr).Encode(out)
	case c.json:
		_ = json.NewEncoder(env.Stderr).Encode(map[string]any{"error": err.Error()})
	default:
		fmt.Fprintf(env.Stderr, "%s: %v\n", c.name, err)
	}
	if worthARetry(err) {
		return exitRetry
	}
	return exitFailed
}

// worthARetry is a fault that may pass by itself: a rate limit, the API briefly down or out of capacity, an
// internal fault (the API says the same call is safe to try again), or the network. Its own exit code lets a CI job
// retry these and stop on the rest.
func worthARetry(err error) bool {
	var problem *api.Problem
	if errors.As(err, &problem) {
		switch problem.Status {
		case http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return true
		}
		return false
	}
	var dns *net.DNSError
	if errors.As(err, &dns) {
		return !dns.IsNotFound // a name that does not exist will not start to
	}
	var opErr *net.OpError // refused, reset or cut off on the way
	if errors.As(err, &opErr) {
		return true
	}
	var netErr net.Error // a time limit ran out on the way (not a certificate or an address that is wrong)
	return errors.As(err, &netErr) && netErr.Timeout()
}

func actions(cmds map[string]command) string {
	var names []string
	for k := range cmds {
		if k != "" {
			names = append(names, k)
		}
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

type command func(ctx context.Context, c *ctxt, args []string) error

// ctxt is one command's run: its flags, its client and its output.
type ctxt struct {
	env     Env
	name    string
	fs      *flag.FlagSet
	json    bool
	apiURL  string
	keyFile string
	idem    string
	idemN   int
	argHelp string
	client  *api.Client
}

// parse reads flags anywhere among the arguments (Go's flag package stops at the first argument that is not a
// flag) and checks the count of the rest.
func (c *ctxt) parse(args []string, argHelp string, min, max int) ([]string, error) {
	c.argHelp = argHelp
	for _, a := range args {
		if a == "--" {
			break
		}
		if isHelp(a) {
			return nil, helpAsked{}
		}
	}
	var pos, flags []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			pos = append(pos, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if strings.Contains(name, "=") {
			continue
		}
		f := c.fs.Lookup(name)
		if f == nil {
			return nil, usagef("no flag %s", a)
		}
		if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
			continue
		}
		if i+1 >= len(args) {
			return nil, usagef("flag %s needs a value", a)
		}
		i++
		flags = append(flags, args[i])
	}
	if err := c.fs.Parse(flags); err != nil {
		return nil, usagef("%v", err)
	}
	if len(pos) < min || (max >= 0 && len(pos) > max) {
		return nil, usagef("expected %s", argHelp)
	}
	return pos, nil
}

func (c *ctxt) usage() string {
	var b strings.Builder
	fmt.Fprintf(&b, "usage: %s %s\n", c.name, c.argHelp)
	c.fs.VisitAll(func(f *flag.Flag) {
		def := ""
		if f.DefValue != "" && f.DefValue != "false" && f.DefValue != "[]" && f.DefValue != "0" {
			def = " (default " + f.DefValue + ")"
		}
		fmt.Fprintf(&b, "  --%-18s %s%s\n", f.Name, f.Usage, def)
	})
	return b.String()
}

// api returns the client, made on first use from the flags and environment.
func (c *ctxt) api() (*api.Client, error) {
	if c.client != nil {
		return c.client, nil
	}
	key := strings.TrimSpace(c.env.Getenv("GS_API_KEY"))
	if c.keyFile != "" {
		data, err := os.ReadFile(c.keyFile)
		if err != nil {
			return nil, fmt.Errorf("reading the key file: %w", err)
		}
		key = strings.TrimSpace(string(data))
		if fi, err := os.Stat(c.keyFile); err == nil && runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
			fmt.Fprintf(c.env.Stderr, "warning: other users on this computer can read the key file %s; make it yours alone (chmod 600)\n", c.keyFile)
		}
	}
	if key == "" {
		return nil, errors.New("no API key: set GS_API_KEY to a key from the panel (in CI, a secret), or pass --key-file")
	}
	// --api, then GS_API_URL, then the public API's own address. A malformed address is refused by api.New.
	base := firstOf(strings.TrimSpace(c.apiURL), strings.TrimSpace(c.env.Getenv("GS_API_URL")), api.DefaultURL)
	cl, err := api.New(base, key, c.env.HTTP, "survivalservers/"+Version)
	if err != nil {
		return nil, err
	}
	c.client = cl
	return cl, nil
}

// call sends one call with the --idempotency-key, if any, and decodes the reply.
func (c *ctxt) call(ctx context.Context, method, path string, query map[string][]string, body any) (map[string]any, error) {
	cl, err := c.api()
	if err != nil {
		return nil, err
	}
	var out map[string]any
	err = cl.JSON(ctx, api.Call{Method: method, Path: path, Query: query, Body: body, IdempotencyKey: c.idemFor(method)}, &out)
	return out, err
}

func (c *ctxt) get(ctx context.Context, path string, query map[string][]string) (map[string]any, error) {
	return c.call(ctx, http.MethodGet, path, query, nil)
}

// idemFor is the key for a write: the --idempotency-key itself for a command's first write, then the key with
// "-2", "-3" and so on, so a re-run of the same command line repeats nothing and each call keeps its own key.
func (c *ctxt) idemFor(method string) string {
	if c.idem == "" || (method != http.MethodPost && method != http.MethodPatch) {
		return ""
	}
	c.idemN++
	if c.idemN == 1 {
		return c.idem
	}
	return fmt.Sprintf("%s-%d", c.idem, c.idemN)
}

// waitOn is what a wait is for: what, for its messages ("game server gs_1"); again, the command that waits on
// without doing anything a second time ("" when running the same command again only reads); and show, which
// prints what was read last when the time runs out, so its ID reaches stdout (as JSON with --json).
type waitOn struct {
	what  string
	again string
	show  func(map[string]any) error
}

// wait polls until settled, printing progress on stderr; a failed end is a failedError, running out of time a
// timeoutError. After a start or a create, "run the same command" would start or make a second one, so
// those waits name their get command in w.again.
func (c *ctxt) wait(ctx context.Context, w waitOn, timeout time.Duration, get func(context.Context) (map[string]any, error), done func(map[string]any) (bool, string)) (map[string]any, error) {
	last := ""
	obj, ok, err := api.Wait(ctx, timeout, c.env.Poll, get, done, func(waited time.Duration, state string) {
		if state != last {
			fmt.Fprintf(c.env.Stderr, "waiting for %s: %s (%s)\n", w.what, state, waited.Round(time.Second))
			last = state
		}
	})
	if err != nil {
		return nil, err
	}
	if !ok {
		_, state := done(obj)
		if w.show != nil && obj != nil {
			if err := w.show(obj); err != nil {
				return obj, err
			}
		}
		return obj, timeoutError{timeoutText(w, state, timeout)}
	}
	return obj, nil
}

// timeoutText is "game server gs_1 is still starting after 2m0s; to keep waiting, run ..." (the settled checks
// name the kind with the state: "game server starting").
func timeoutText(w waitOn, state string, timeout time.Duration) string {
	kind := w.what
	if i := strings.LastIndex(kind, " "); i > 0 {
		kind = kind[:i]
	}
	var s string
	switch rest, cut := strings.CutPrefix(state, kind+" "); {
	case state == "":
		s = fmt.Sprintf("%s is not done after %s", w.what, timeout)
	case cut:
		s = fmt.Sprintf("%s is still %s after %s", w.what, rest, timeout)
	default:
		s = fmt.Sprintf("%s: still %s after %s", w.what, state, timeout)
	}
	switch {
	case w.again == "":
		return s + "; run the same command to keep waiting"
	case strings.HasPrefix(w.again, "survivalservers "):
		return s + "; to keep waiting, run " + w.again
	}
	return s + "; " + w.again
}

// ---- output ----

func (c *ctxt) printJSON(v any) error {
	enc := json.NewEncoder(c.env.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// show prints an object: its JSON with --json, or the summary lines.
func (c *ctxt) show(obj any, lines ...string) error {
	if c.json {
		return c.printJSON(obj)
	}
	for _, l := range lines {
		if l != "" {
			fmt.Fprintln(c.env.Stdout, l)
		}
	}
	return nil
}

// listCap is the most rows a list reads when it is given no limit.
const listCap = 10000

// readAll reads a list call to its end, following next_cursor 500 rows at a time, or until max rows (0: listCap),
// and returns them as one page ({"data": [...]}), so --json prints one array of everything read. When rows were
// left it says so on stderr, with how to read further (more) when the command has a way. Before, every list
// stopped at its first page with no sign.
func (c *ctxt) readAll(ctx context.Context, path string, query map[string][]string, max int, more string) (map[string]any, error) {
	if max <= 0 {
		max = listCap
	}
	q := map[string][]string{}
	for k, v := range query {
		q[k] = v
	}
	q["limit"] = []string{api.Itoa(min(max, 500))}
	var rows []any
	for {
		page, err := c.get(ctx, path, q)
		if err != nil {
			return nil, err
		}
		data, _ := page["data"].([]any)
		rows = append(rows, data...)
		next := str(page["next_cursor"])
		if len(rows) >= max && (len(rows) > max || next != "") {
			note := fmt.Sprintf("showing the first %d; more exist", max)
			if more != "" {
				note += ": " + more
			}
			fmt.Fprintln(c.env.Stderr, note)
			return map[string]any{"data": rows[:max]}, nil
		}
		if next == "" {
			if rows == nil {
				rows = []any{}
			}
			return map[string]any{"data": rows}, nil
		}
		q["cursor"] = []string{next}
	}
}

// table prints rows under headers, or the items as JSON with --json.
func (c *ctxt) table(items any, headers []string, rows [][]string) error {
	if c.json {
		return c.printJSON(items)
	}
	if len(rows) == 0 {
		fmt.Fprintln(c.env.Stdout, "(none)")
		return nil
	}
	tw := tabwriter.NewWriter(c.env.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(headers, "\t"))
	for _, r := range rows {
		fmt.Fprintln(tw, strings.Join(r, "\t"))
	}
	return tw.Flush()
}

// ---- small helpers ----

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

// pairs reads NAME=value flags into a map.
func pairs(list []string, what string) (map[string]string, error) {
	if len(list) == 0 {
		return nil, nil
	}
	out := map[string]string{}
	for _, kv := range list {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return nil, usagef("%s %q is not NAME=value", what, kv)
		}
		out[k] = v
	}
	return out, nil
}

func str(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	if f, ok := v.(float64); ok {
		return fmt.Sprintf("%g", f)
	}
	return fmt.Sprint(v)
}

func obj(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func list(v any) []map[string]any {
	items, _ := v.([]any)
	out := make([]map[string]any, 0, len(items))
	for _, it := range items {
		if m, ok := it.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func anyList(v any) []any {
	items, _ := v.([]any)
	return items
}

func firstOf(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

// address is a game server's or persistent server's address for the game port, or every port.
func address(m map[string]any) string {
	a := obj(m["address"])
	ip := firstOf(str(a["ipv4"]), str(a["ipv6"]))
	if ip == "" {
		return ""
	}
	ports := obj(a["ports"])
	if game := obj(ports["game"]); game != nil {
		return fmt.Sprintf("%s:%s", ip, str(game["port"]))
	}
	var parts []string
	for name, p := range ports {
		parts = append(parts, fmt.Sprintf("%s=%s:%s", name, ip, str(obj(p)["port"])))
	}
	sort.Strings(parts)
	return strings.Join(parts, " ")
}

func esc(s string) string { return api.PathEscape(s) }

func jsonText(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(data)
}

func readJSONFile(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("%s is not a JSON object: %w", path, err)
	}
	return m, nil
}

const helpText = `survivalservers: run your game's dedicated servers from your computer or your CI.

The key comes from GS_API_KEY (or --key-file). The API address comes from --api, else GS_API_URL, else
https://studio.survivalservers.com.
Every command takes --json for the API's own JSON, and writes take --idempotency-key.

  survivalservers account                                  the studio, its limits and free vCPU-hours
  survivalservers regions [--os windows|linux] [--tier T] [--continent C]   where game servers can run
  survivalservers continents                               the continents and their regions

  survivalservers apps list | get APP | create SLUG --os windows|linux [--name N] [--tier T] [--region R]... [--continent C]...
  survivalservers apps icon APP FILE | banner APP FILE | color APP #rrggbb   (--remove, --clear) an app's look
  survivalservers studio rename NAME | icon FILE | accent #rrggbb            (--remove, --clear) the studio's look
  survivalservers builds upload APP SERVER.zip [--wait]    a Windows build, uploaded in parts
  survivalservers builds image APP IMAGE [--wait]          a Linux build from an image in any registry
  survivalservers builds get BUILD [--wait] | list APP | delete BUILD
  survivalservers registry login                           docker login to our registry for pushing images
  survivalservers versions create APP SETTINGS.json [--name N] [--build B] [--dry-run] [--wait]
  survivalservers versions list APP | get APP VERSION [--wait] | delete APP VERSION
  survivalservers versions deprecate APP VERSION [--undo]  no new starts of it (running ones carry on); --undo allows them
  survivalservers branches set APP BRANCH VERSION | list APP
  survivalservers deploy APP (--zip FILE | --image REF) --settings SETTINGS.json --name N [--branch B]
                                                           build, version and branch in one step, waiting for each

  survivalservers game-servers start APP [--version V | --branch B] [--region R]... [--continent C]... [--env K=V]... [--ttl 2h] [--wait]
  survivalservers game-servers list [--app A] [--region R] [--status S,S] [--label k:v]... | get ID [--wait]
  survivalservers game-servers stop ID [--shutdown-time 30s]
  survivalservers game-servers events ID
  survivalservers logs ID [--source console] [--tail 200] [--follow] | logs list ID
  survivalservers files ID [PATH] | files get ID PATH [-o FILE]

  survivalservers persistent-servers list [--app A] | get ID [--wait] | create SETTINGS.json [--wait]
  survivalservers persistent-servers start|stop|restart ID [--wait] | update ID [--version V | --branch B] [--apply now]
  survivalservers persistent-servers backup ID | backups ID | restore ID ...   the same as backups take, backups list and restore
  survivalservers backups list SERVER | take SERVER [--label L] [--lock] | lock BACKUP | unlock BACKUP | delete BACKUP
  survivalservers backups download BACKUP [-o file.zip]    waits for the zip and checks every file's SHA-256
  survivalservers backups upload SERVER saves.zip          your own saves as a backup; the persistent server stays as it is until you restore it
  survivalservers restore ID (--backup BACKUP | --at "14:00") [--tz Z] [--version then] [--no-start]
                                                           one persistent server back to a backup or a time; it is backed up first, so you can undo
  survivalservers rollback --before TIME [--app A] [--continent C] [--region R] [--label k=v]... [--version then]
                           [--try-one-first] [--warn 15m,5m,1m --warn-command LINE] [--yes]
                                                           many persistent servers back to before a time: shows the preview, then asks
  survivalservers pools list APP | set APP REGION (--version V | --branch B) --cpu N --memory-gb N [--mode auto] --min N [--max N]
  survivalservers pools remove APP REGION                  ready servers: game servers started ahead, waiting for players
  survivalservers profile get APP VERSION | set APP VERSION (game-profile.json | --from GAME) | templates   a version's game profile
  survivalservers command SERVER COMMAND [name=value]...   run a game command on one game server
  survivalservers command SERVER --line "status" [--via rcon]   send a line as it is
  survivalservers bulk-actions command --app A [--region R]... [--label k:v]... COMMAND [name=value]... [--dry-run] [--wait]
                                                           the same on many game servers, with --line too
  survivalservers bulk-actions list | get ID | replies ID | cancel ID
  survivalservers bulk-actions watch ID | undo ID | retry ID | resume ID
  survivalservers players SERVER                           who is on a game server now
  survivalservers config get SERVER | set SERVER name=value... [--live] [--unset name]   a persistent server's game settings
  survivalservers saved list | create NAME ACTION request.json [--config-set C] [--cron "0 18 * * 5"] [--time-zone Z]
  survivalservers saved run ID [--dry-run] | pause ID | resume ID | delete ID   bulk actions kept under a name
  survivalservers usage [--from 2026-10-01] [--to 2026-11-01] [--group-by app,region] | prices

  survivalservers ai setup [--project] [--yes]             our MCP server and rules in your AI tools; no key needed
  survivalservers mcp [--http ADDR]                        the MCP server itself, for an AI tool to start (key: GS_API_KEY)
  survivalservers dev [flags] -- ./MyServer [args...]      your game server on this computer, as our machines run it

Exit codes: 0 done, 1 refused or failed, 2 wrong command line, 3 still waiting when --timeout ran out,
4 the API was busy or out of reach (a rate limit, a brief outage, the network): worth a retry.
`
