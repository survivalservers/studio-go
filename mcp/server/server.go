// Package server is the MCP server: tools that take a game from a server build to running game servers and
// persistent servers through the public API, read what happened after a game server exits, and set a game up from the docs.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	api "github.com/survivalservers/studio-go"
)

// Version is this server's version, sent to clients and in the API's User-Agent.
var Version = "0.1.1"

// Config is how one server behaves. Local servers run on the developer's computer (stdio) and may read the
// files the agent names; hosted servers (HTTP) never touch a disk and take the key from each request.
type Config struct {
	APIURL string
	Key    string
	// ReadOnly leaves every tool that changes something out of the tool list.
	ReadOnly bool
	// Apps, when not empty, is the only apps (slugs or IDs) the tools act on or read, including game servers,
	// persistent servers and builds reached by their own IDs.
	Apps []string
	// MaxTTL is the longest a game server started here may run, and its run time when the agent sets none. Zero
	// means no limit.
	MaxTTL time.Duration
	// Local servers may read local files (zip uploads).
	Local bool
	// HTTPClient is used for API calls (tests); Storage for upload parts.
	HTTPClient *http.Client
	Storage    *http.Client
	// Docker runs docker with the given arguments and stdin and returns what it printed (tests); nil runs the real
	// one. The local server's get_registry_login logs Docker in with it, so the password never reaches the chat.
	Docker func(ctx context.Context, stdin io.Reader, args ...string) ([]byte, error)
	// Poll is how often the wait tools check (default 3 seconds).
	Poll time.Duration
	// KeptWait is how long the tools that read what a game server left behind wait, right after it exited, for its
	// machine to send that run's logs and files (default 30 seconds; they usually come within seconds).
	KeptWait time.Duration

	// Resource is the hosted server's own address as tools add it (https://mcp.example.com/mcp). With it the hosted
	// server is an OAuth protected resource (RFC 9728): a request with no token is answered 401 pointing at its
	// metadata, so a tool signs in through AuthServer with no key in its setup.
	Resource string
	// AuthServer is the sign-in server (its issuer); default APIURL.
	AuthServer string
	// ExchangeSecretFile holds the secret the hosted server signs in with to swap tools' sign-in tokens (oauth.go):
	// 32 or more random bytes as hex, mode 0600. Needed with Resource.
	ExchangeSecretFile string
}

// ConfigFromEnv reads GS_API_URL (the public API's own address when it is not set), GS_API_KEY, GS_READ_ONLY, GS_APPS and GS_MAX_TTL, and for the hosted server's
// sign-in GS_MCP_RESOURCE, GS_AUTH_URL and GS_MCP_EXCHANGE_SECRET_FILE (the file's name; the secret is never in the
// environment).
func ConfigFromEnv() (Config, error) {
	c := Config{
		APIURL:     firstOf(strings.TrimSpace(os.Getenv("GS_API_URL")), api.DefaultURL),
		Key:        strings.TrimSpace(os.Getenv("GS_API_KEY")),
		ReadOnly:   os.Getenv("GS_READ_ONLY") == "1" || strings.EqualFold(os.Getenv("GS_READ_ONLY"), "true"),
		MaxTTL:     2 * time.Hour,
		Resource:   strings.TrimRight(strings.TrimSpace(os.Getenv("GS_MCP_RESOURCE")), "/"),
		AuthServer: strings.TrimRight(strings.TrimSpace(os.Getenv("GS_AUTH_URL")), "/"),

		ExchangeSecretFile: strings.TrimSpace(os.Getenv("GS_MCP_EXCHANGE_SECRET_FILE")),
	}
	for _, a := range strings.Split(os.Getenv("GS_APPS"), ",") {
		if a = strings.TrimSpace(a); a != "" {
			c.Apps = append(c.Apps, a)
		}
	}
	if v := strings.TrimSpace(os.Getenv("GS_MAX_TTL")); v != "" {
		d, err := parseDuration(v)
		if err != nil {
			return c, fmt.Errorf("GS_MAX_TTL: %w", err)
		}
		c.MaxTTL = d
	}
	return c, nil
}

// parseDuration reads the API's duration form: a number with ms, s, m, h or d, or a bare number of seconds.
func parseDuration(v string) (time.Duration, error) {
	v = strings.TrimSpace(v)
	if v == "0" {
		return 0, nil
	}
	if strings.HasSuffix(v, "d") {
		d, err := time.ParseDuration(strings.TrimSuffix(v, "d") + "h")
		return d * 24, err
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d, nil
	}
	d, err := time.ParseDuration(v + "s")
	if err != nil {
		return 0, fmt.Errorf("%q is not a duration such as 30m, 2h or 1d", v)
	}
	return d, nil
}

func formatDuration(d time.Duration) string {
	switch {
	case d%(24*time.Hour) == 0:
		return fmt.Sprintf("%dd", d/(24*time.Hour))
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", d/time.Minute)
	default:
		return fmt.Sprintf("%ds", d/time.Second)
	}
}

// srv is one server's state: its client and the allowlist resolved to app IDs.
type srv struct {
	cfg    Config
	client *api.Client

	appsOnce sync.Mutex
	appIDs   map[string]bool // allowed app IDs and slugs, once resolved
}

// New returns the MCP server for cfg.
func New(cfg Config) (*mcp.Server, error) {
	client, err := api.New(cfg.APIURL, cfg.Key, withApprovalHeader(cfg.HTTPClient), "survivalservers-mcp/"+Version)
	if err != nil {
		return nil, err
	}
	if cfg.Poll <= 0 {
		cfg.Poll = 3 * time.Second
	}
	if cfg.KeptWait <= 0 {
		cfg.KeptWait = 30 * time.Second
	}
	s := &srv{cfg: cfg, client: client}
	server := mcp.NewServer(&mcp.Implementation{Name: "survival-servers", Title: "Survival Servers Studio", Version: Version},
		&mcp.ServerOptions{Instructions: s.instructions(), SchemaCache: sdkSchemas})
	s.addBuildTools(server)
	s.addRunTools(server)
	s.addPersistentServerTools(server)
	s.addCommandTools(server)
	s.addMoneyTools(server)
	s.addDocs(server)
	return server, nil
}

func (s *srv) instructions() string {
	var b strings.Builder
	b.WriteString(`Tools for hosting a game's dedicated servers: Windows builds as a zip, Linux builds as an image, started on demand or kept running as persistent servers.

From a build to players connecting:
1. setup_guide for the engine (unreal, unity, godot or custom), when the game is not set up yet.
2. create_app once per game.
3. A build: upload_windows_build with the zip, or create_linux_build with an image (push to any registry, or to ours after get_registry_login).
4. create_version: the build plus how to launch it. Then wait_for_version until a region has it.
5. start_game_server for a match (it waits and returns the address), or create_persistent_server for a server that keeps its saves.
6. When something fails: diagnose_game_server, read_game_server_log and game_server_files work after the server has exited too.

Rules:
` + api.SkillRules() + `
- Read before you change: list or wait_for_* first, then act on the IDs you got back.
- No tool stops many game servers at once. On many servers: plan_bulk_action, show the plan, then run_bulk_action.`)
	if s.cfg.ReadOnly {
		b.WriteString("\n- This server is read-only: tools that change things are not available.")
	}
	if len(s.cfg.Apps) > 0 {
		fmt.Fprintf(&b, "\n- Only these apps can be used: %s.", strings.Join(s.cfg.Apps, ", "))
	}
	if s.cfg.MaxTTL > 0 {
		fmt.Fprintf(&b, "\n- Game servers started here stop on their own after %s at most.", formatDuration(s.cfg.MaxTTL))
	}
	if !s.cfg.Local {
		b.WriteString("\n- This hosted server cannot read the developer's files. Upload a Windows zip with begin_windows_upload, run its script, then complete_windows_upload.")
	}
	return b.String()
}

// ---- results ----

func textResult(summary string, v any) *mcp.CallToolResult {
	var b strings.Builder
	b.WriteString(summary)
	if v != nil {
		data, err := json.MarshalIndent(v, "", "  ")
		if err == nil {
			b.WriteString("\n\n")
			b.Write(data)
		}
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: b.String()}}}
}

func plainResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}

// ---- allowlist ----

var errNotAllowed = errors.New("not allowed")

// allowedApp reports whether app (a slug or ID) may be used.
func (s *srv) allowedApp(ctx context.Context, app string) error {
	if len(s.cfg.Apps) == 0 {
		return nil
	}
	ids, err := s.allowedIDs(ctx)
	if err != nil {
		return err
	}
	if ids[app] {
		return nil
	}
	return fmt.Errorf("%w: app %q is not one this server may use (GS_APPS allows %s)", errNotAllowed, app, strings.Join(s.cfg.Apps, ", "))
}

func (s *srv) allowedIDs(ctx context.Context) (map[string]bool, error) {
	s.appsOnce.Lock()
	defer s.appsOnce.Unlock()
	if s.appIDs != nil {
		return s.appIDs, nil
	}
	ids := map[string]bool{}
	for _, a := range s.cfg.Apps {
		ids[a] = true
		app, err := s.client.Get(ctx, "/v0/apps/"+api.PathEscape(a), nil)
		var p *api.Problem
		if errors.As(err, &p) && p.Status == http.StatusNotFound {
			continue // an app that does not exist yet: allowed by name, so create_app can make it
		}
		if err != nil {
			return nil, err
		}
		ids[str(app["id"])] = true
		ids[str(app["slug"])] = true
	}
	s.appIDs = ids
	return ids, nil
}

// forgetApps drops the resolved allowlist (after create_app made an allowed app).
func (s *srv) forgetApps() {
	s.appsOnce.Lock()
	s.appIDs = nil
	s.appsOnce.Unlock()
}

// objectAllowed checks that an object reached by its own ID (game server, persistent server, build) belongs to an allowed app.
func (s *srv) objectAllowed(ctx context.Context, obj map[string]any, what string) error {
	if len(s.cfg.Apps) == 0 {
		return nil
	}
	if err := s.allowedApp(ctx, str(obj["app"])); err != nil {
		return fmt.Errorf("%w: %s %s belongs to an app this server may not use", errNotAllowed, what, str(obj["id"]))
	}
	return nil
}

// limited reports whether GS_APPS fences this server to some apps.
func (s *srv) limited() bool { return len(s.cfg.Apps) > 0 }

// needApp is the answer to a call that names no app while GS_APPS is set: without one, it could reach any app.
func (s *srv) needApp(field string) error {
	return fmt.Errorf("%w: name %s (GS_APPS allows %s)", errNotAllowed, field, strings.Join(s.cfg.Apps, ", "))
}

// targetsAllowed checks that every target a plan reaches or skips (each with its "app") is of an allowed app. A
// target with no app counts as another app's, so a plan the API answered without them is refused, not run.
func (s *srv) targetsAllowed(ctx context.Context, plan map[string]any) error {
	if !s.limited() {
		return nil
	}
	for _, t := range append(list(plan["targets"]), list(plan["skipped"])...) {
		if err := s.allowedApp(ctx, str(t["app"])); err != nil {
			return fmt.Errorf("%w: %s belongs to an app this server may not use, so this plan is not shown or run", errNotAllowed, firstOf(str(t["name"]), str(t["id"])))
		}
	}
	return nil
}

// backupAllowed checks that a backup belongs to a persistent server of an allowed app.
func (s *srv) backupAllowed(ctx context.Context, id string) error {
	if !s.limited() {
		return nil
	}
	b, err := s.client.Get(ctx, "/v0/backups/"+api.PathEscape(id), nil)
	if err != nil {
		return err
	}
	if _, err := s.persistentServer(ctx, str(b["persistent_server"])); err != nil {
		if errors.Is(err, errNotAllowed) {
			return fmt.Errorf("%w: backup %s belongs to an app this server may not use", errNotAllowed, id)
		}
		return err
	}
	return nil
}

// bulkActionAllowed checks that a bulk action reaches only allowed apps: its selector's app, when it has one, and
// every one of its targets. One with no app in its selector and no targets yet could reach any app, so it is refused.
// It returns the bulk action (read only when the server is limited, or when get is set).
func (s *srv) bulkActionAllowed(ctx context.Context, id string, get bool) (map[string]any, error) {
	if !s.limited() && !get {
		return nil, nil
	}
	path := "/v0/bulk-actions/" + api.PathEscape(id)
	op, err := s.client.Get(ctx, path, nil)
	if err != nil || !s.limited() {
		return op, err
	}
	refuse := fmt.Errorf("%w: bulk action %s reaches an app this server may not use", errNotAllowed, id)
	app := str(obj(op["selector"])["app"])
	if app != "" {
		if err := s.allowedApp(ctx, app); err != nil {
			return nil, refuse
		}
	}
	seen := 0
	q := url.Values{"limit": {"500"}}
	for page := 0; ; page++ {
		if page == 40 {
			return nil, fmt.Errorf("%w: bulk action %s has too many targets to check against GS_APPS", errNotAllowed, id)
		}
		res, err := s.client.Get(ctx, path+"/targets", q)
		if err != nil {
			return nil, err
		}
		for _, t := range list(res["data"]) {
			seen++
			if err := s.allowedApp(ctx, str(t["app"])); err != nil {
				return nil, refuse
			}
		}
		next := str(res["next_cursor"])
		if next == "" {
			break
		}
		q.Set("cursor", next)
	}
	if seen == 0 && app == "" {
		return nil, fmt.Errorf("%w: bulk action %s names no app and has no targets yet, so it could reach any app", errNotAllowed, id)
	}
	return op, nil
}

// filterAllowed keeps the list items whose app (in field, "app" or an app's own "id") is allowed.
func (s *srv) filterAllowed(ctx context.Context, items []map[string]any, field string) ([]map[string]any, error) {
	if len(s.cfg.Apps) == 0 {
		return items, nil
	}
	ids, err := s.allowedIDs(ctx)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(items, func(m map[string]any) bool { return !ids[str(m[field])] }), nil
}

// ---- waiting ----

// wait polls get until done says so, the time runs out or the context ends, sending progress notes when the client
// asked for them.
func (s *srv) wait(ctx context.Context, req *mcp.CallToolRequest, seconds int, get func(context.Context) (map[string]any, error), done func(map[string]any) (bool, string)) (obj map[string]any, finished bool, waited time.Duration, err error) {
	start := time.Now()
	var progress func(time.Duration, string)
	if req != nil && req.Session != nil {
		if tok := req.Params.GetProgressToken(); tok != nil {
			progress = func(w time.Duration, state string) {
				_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{ProgressToken: tok, Progress: w.Seconds(), Total: float64(seconds), Message: state})
			}
		}
	}
	obj, finished, err = api.Wait(ctx, time.Duration(seconds)*time.Second, s.cfg.Poll, get, done, progress)
	return obj, finished, time.Since(start), err
}

// ---- small helpers ----

func str(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
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

// pick keeps only the named fields, in that order, for compact lists.
func pick(m map[string]any, fields ...string) map[string]any {
	out := make(map[string]any, len(fields))
	for _, f := range fields {
		if v, ok := m[f]; ok && v != nil {
			out[f] = v
		}
	}
	return out
}

// address formats a game server's or persistent server's address as ip:port for the game port (or every port).
func address(m map[string]any) string {
	a := obj(m["address"])
	ip := str(a["ipv4"])
	if ip == "" {
		ip = str(a["ipv6"])
	}
	if ip == "" {
		return ""
	}
	ports := obj(a["ports"])
	if game := obj(ports["game"]); game != nil {
		return fmt.Sprintf("%s:%v (%v)", ip, game["port"], firstOf(str(game["protocol"]), "udp"))
	}
	var parts []string
	for name, p := range ports {
		parts = append(parts, fmt.Sprintf("%s %s:%v", name, ip, obj(p)["port"]))
	}
	slices.Sort(parts)
	return strings.Join(parts, ", ")
}

func firstOf(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

func boolPtr(b bool) *bool { return &b }

// num formats a JSON number without an exponent.
func num(v any) string {
	if f, ok := v.(float64); ok {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return str(v)
}

// jsonText is v as compact JSON, for lines in a text answer.
func jsonText(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(data)
}

// approvalKey carries an AI agent's approved request ("Ask me first") on a call's context.
type approvalKey struct{}

// withApproval makes a call that carries the approved request id as the Approval header: the same call made again once
// a member said yes. Empty: the call as it is.
func withApproval(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, approvalKey{}, id)
}

// approvalTransport sets the Approval header from the request's context. It sits in this server's own HTTP client, so
// every call a tool makes with that context carries it, whatever helper of the API client makes the call.
type approvalTransport struct{ base http.RoundTripper }

func (t approvalTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if id, _ := r.Context().Value(approvalKey{}).(string); id != "" {
		r = r.Clone(r.Context())
		r.Header.Set("Approval", id)
	}
	return t.base.RoundTrip(r)
}

// withApprovalHeader is the HTTP client for API calls with approvalTransport in it: a copy of the one given (tests), or
// the API client's own default (AnswerTimeout, redirects not followed). This server opens no streams.
func withApprovalHeader(hc *http.Client) *http.Client {
	var c http.Client
	if hc != nil {
		c = *hc
	} else {
		c = http.Client{Timeout: api.AnswerTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	base := c.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	c.Transport = approvalTransport{base: base}
	return &c
}
