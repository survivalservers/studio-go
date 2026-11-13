package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	api "github.com/survivalservers/studio-go"
)

// agentLabel marks game servers and persistent servers started through this server, so they can be found and stopped.
const agentLabel, agentLabelValue = "started_by", "mcp"

var gsStatuses = []string{"starting", "running", "stopping", "stopped", "failed"}

type startIn struct {
	App         string            `json:"app" jsonschema:"the app's slug or ID"`
	Version     string            `json:"version,omitempty" jsonschema:"the version name to run; leave out version and branch for the live branch"`
	Branch      string            `json:"branch,omitempty" jsonschema:"run whatever version this branch points at, instead of a version"`
	Regions     []string          `json:"regions,omitempty" jsonschema:"preferred regions in order (from list_regions); default the app's placement"`
	Continents  []string          `json:"continents,omitempty" jsonschema:"any region in these continents, after regions, for example north-america"`
	StartMode   string            `json:"start_mode,omitempty" jsonschema:"auto (default: a ready server when one is waiting, else a fresh start), warm (ready servers only) or cold (always a fresh start)"`
	Env         map[string]string `json:"env,omitempty" jsonschema:"extra environment variables for this game server"`
	Args        []string          `json:"args,omitempty" jsonschema:"extra launch arguments for this game server"`
	Labels      map[string]string `json:"labels,omitempty" jsonschema:"labels to find it by later, for example match_id"`
	TTL         string            `json:"ttl,omitempty" jsonschema:"stop it after this long, for example 30m or 2h"`
	ExternalID  string            `json:"external_id,omitempty" jsonschema:"your own ID for it, for example a match ID"`
	RetryKey    string            `json:"idempotency_key,omitempty" jsonschema:"send the same key when you retry this start, so a retry returns the first game server instead of starting a second; default the external_id, else a new key for every call"`
	WaitSeconds *int              `json:"wait_seconds,omitempty" jsonschema:"how long to wait for it to be ready: 0 returns at once; default 120, at most 900"`
}

// retryKey is the Idempotency-Key for a start or a create: the key the agent gave, else one made from its own ID
// for the object (a match ID makes a good key, as the API docs say), else "" and the client makes a new one. The API
// takes keys of up to 255 characters, so a longer own ID is hashed.
func retryKey(asked, prefix, ownID string) string {
	if k := strings.TrimSpace(asked); k != "" {
		return k
	}
	if ownID == "" {
		return ""
	}
	if k := prefix + ownID; len(k) <= 255 {
		return k
	}
	sum := sha256.Sum256([]byte(ownID))
	return prefix + hex.EncodeToString(sum[:])
}

type idIn struct {
	ID string `json:"id" jsonschema:"the game server's ID"`
}

type waitGSIn struct {
	ID          string `json:"id" jsonschema:"the game server's ID"`
	WaitSeconds *int   `json:"wait_seconds,omitempty" jsonschema:"how long to wait for it to be ready or end: 0 reads it now; default 120, at most 900"`
}

type stopIn struct {
	ID     string `json:"id" jsonschema:"the game server's ID"`
	Grace  string `json:"shutdown_time,omitempty" jsonschema:"time to save and exit before it is killed, for example 30s; default the version's"`
	Reason string `json:"reason,omitempty" jsonschema:"why, kept on the game server's timeline"`
}

type listGSIn struct {
	App         string   `json:"app,omitempty" jsonschema:"only this app's game servers"`
	Version     string   `json:"version,omitempty" jsonschema:"only this version (with app)"`
	Status      []string `json:"status,omitempty" jsonschema:"only these states; default starting and running"`
	Region      string   `json:"region,omitempty" jsonschema:"only this region"`
	Labels      []string `json:"labels,omitempty" jsonschema:"only game servers with all these labels, each key:value"`
	StartedHere bool     `json:"started_here,omitempty" jsonschema:"only game servers started through this tool server"`
	Limit       int      `json:"limit,omitempty" jsonschema:"at most this many, newest first (default 50, at most 500)"`
}

type logIn struct {
	ID     string `json:"id" jsonschema:"the game server's ID"`
	Source string `json:"source,omitempty" jsonschema:"console (default: stdout and stderr together), stdout, stderr, or a log file's source name from diagnose_game_server"`
	Tail   int    `json:"tail,omitempty" jsonschema:"the last this many lines (default 200, at most 5000)"`
}

type filesIn struct {
	ID   string `json:"id" jsonschema:"the game server's ID"`
	Path string `json:"path,omitempty" jsonschema:"a folder inside the game server's folder; empty for the top"`
}

type fileIn struct {
	ID       string `json:"id" jsonschema:"the game server's ID"`
	Path     string `json:"path" jsonschema:"a file inside the game server's folder, as list_game_server_files shows it"`
	MaxBytes int    `json:"max_bytes,omitempty" jsonschema:"read at most this much of a text file (default 65536, at most 1048576)"`
}

func (s *srv) addRunTools(server *mcp.Server) {
	tool(s, server, "start_game_server", "Start a game server", "Starts one game server from a version or branch in the best region allowed and waits until players can join; returns the address. It runs (and is billed) until it exits, is stopped, or its ttl ends.", changes,
		map[string][]string{"start_mode": {"auto", "warm", "cold"}},
		func(ctx context.Context, req *mcp.CallToolRequest, in startIn) (*mcp.CallToolResult, error) {
			if err := s.allowedApp(ctx, in.App); err != nil {
				return nil, err
			}
			if in.Version != "" && in.Branch != "" {
				return nil, fmt.Errorf("give version or branch, not both")
			}
			ttl, err := s.ttl(in.TTL)
			if err != nil {
				return nil, err
			}
			body := map[string]any{"app": in.App}
			setIf(body, "version", in.Version)
			setIf(body, "branch", in.Branch)
			setIf(body, "start_mode", in.StartMode)
			setIf(body, "external_id", in.ExternalID)
			setIf(body, "ttl", ttl)
			if len(in.Regions) > 0 || len(in.Continents) > 0 {
				p := map[string]any{}
				if len(in.Regions) > 0 {
					p["regions"] = in.Regions
				}
				if len(in.Continents) > 0 {
					p["continents"] = in.Continents
				}
				body["placement"] = p
			}
			if len(in.Env) > 0 {
				body["env"] = in.Env
			}
			if len(in.Args) > 0 {
				body["args"] = in.Args
			}
			labels := map[string]string{agentLabel: agentLabelValue}
			for k, v := range in.Labels {
				labels[k] = v
			}
			body["labels"] = labels
			var gs map[string]any
			key := retryKey(in.RetryKey, "start-", in.ExternalID)
			if err := s.client.JSON(ctx, api.Call{Method: http.MethodPost, Path: "/v0/game-servers", Body: body, IdempotencyKey: key}, &gs); err != nil {
				return nil, err
			}
			id := str(gs["id"])
			if str(gs["status"]) == "starting" {
				waited, _, _, err := s.wait(ctx, req, waitSeconds(in.WaitSeconds, 120), func(ctx context.Context) (map[string]any, error) {
					return s.client.Get(ctx, "/v0/game-servers/"+api.PathEscape(id), nil)
				}, api.GameServerSettled)
				if err != nil {
					return textResult(fmt.Sprintf("Game server %s started in %s but reading it failed: %v. Use wait_for_game_server.", id, str(gs["region"]), err), gs), nil
				}
				gs = waited
			}
			return textResult(gsSummary(gs, "Game server"), gs), nil
		})

	tool(s, server, "wait_for_game_server", "Read or wait for a game server", "Reads a game server and waits until it is ready for players (or has ended): its state, address, players, and how it exited.", reads, nil,
		func(ctx context.Context, req *mcp.CallToolRequest, in waitGSIn) (*mcp.CallToolResult, error) {
			if _, err := s.gameServer(ctx, in.ID); err != nil {
				return nil, err
			}
			gs, _, _, err := s.wait(ctx, req, waitSeconds(in.WaitSeconds, 120), func(ctx context.Context) (map[string]any, error) {
				return s.client.Get(ctx, "/v0/game-servers/"+api.PathEscape(in.ID), nil)
			}, api.GameServerSettled)
			if err != nil {
				return nil, err
			}
			return textResult(gsSummary(gs, "Game server"), gs), nil
		})

	tool(s, server, "list_game_servers", "List game servers", "Game servers, newest first, running ones by default. Use it to find servers left running.", reads,
		map[string][]string{"status": gsStatuses},
		func(ctx context.Context, _ *mcp.CallToolRequest, in listGSIn) (*mcp.CallToolResult, error) {
			q := url.Values{"limit": {api.Itoa(clamp(in.Limit, 50, 500))}}
			if in.App != "" {
				if err := s.allowedApp(ctx, in.App); err != nil {
					return nil, err
				}
				q.Set("app", in.App)
			}
			setQ(q, "version", in.Version)
			setQ(q, "region", in.Region)
			status := in.Status
			if len(status) == 0 {
				status = []string{"starting", "running"}
			}
			q.Set("status", strings.Join(status, ",")) // one comma-separated value (the API's form, not exploded)
			for _, l := range in.Labels {
				q.Add("label", l)
			}
			if in.StartedHere {
				q.Add("label", agentLabel+":"+agentLabelValue)
			}
			page, err := s.client.Get(ctx, "/v0/game-servers", q)
			if err != nil {
				return nil, err
			}
			items, err := s.filterAllowed(ctx, list(page["data"]), "app")
			if err != nil {
				return nil, err
			}
			rows := []map[string]any{}
			for _, g := range items {
				r := pick(g, "id", "status", "app", "version", "region", "players", "labels", "created_at", "expires_at")
				if a := address(g); a != "" {
					r["address"] = a
				}
				rows = append(rows, r)
			}
			more := ""
			if str(page["next_cursor"]) != "" {
				more = " There are more; narrow the filters."
			}
			return textResult(fmt.Sprintf("%d game servers (%s).%s", len(rows), strings.Join(status, ", "), more), rows), nil
		})

	tool(s, server, "stop_game_server", "Stop a game server", "Asks one game server to save and exit (the version's stop method), then ends it after its shutdown time. Its logs and files stay readable for 90 days.", removes, nil,
		func(ctx context.Context, _ *mcp.CallToolRequest, in stopIn) (*mcp.CallToolResult, error) {
			if _, err := s.gameServer(ctx, in.ID); err != nil {
				return nil, err
			}
			body := map[string]any{}
			setIf(body, "shutdown_time", in.Grace)
			setIf(body, "reason", firstOf(in.Reason, "stopped by an AI agent"))
			var gs map[string]any
			if err := s.client.JSON(ctx, api.Call{Method: http.MethodPost, Path: "/v0/game-servers/" + api.PathEscape(in.ID) + "/stop", Body: body}, &gs); err != nil {
				return nil, err
			}
			return textResult(fmt.Sprintf("Game server %s is %s.", in.ID, str(gs["status"])), pick(gs, "id", "status", "app", "version", "region", "stopped_at", "exit")), nil
		})

	tool(s, server, "diagnose_game_server", "Diagnose a game server", "Everything needed to see why a game server failed, crashed or never became ready, in one call: its state and exit, its timeline, the end of its console log, its other logs, and crash dumps and files kept after it exited.", reads, nil,
		func(ctx context.Context, req *mcp.CallToolRequest, in idIn) (*mcp.CallToolResult, error) {
			gs, err := s.gameServer(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			kept := s.awaitKept(ctx, req, gs)
			base := "/v0/game-servers/" + api.PathEscape(in.ID)
			var b strings.Builder
			b.WriteString(gsSummary(gs, "Game server"))
			fmt.Fprintf(&b, "\n\nState: %s", jsonText(pick(gs, "status", "app", "version", "region", "start_mode", "size", "created_at", "running_at", "stopped_at", "exit", "restarts", "timings_ms")))
			if ev, err := s.client.Get(ctx, base+"/events", url.Values{"limit": {"100"}}); err == nil {
				// An event's data can carry what the game wrote (an exit message, a reply), so the timeline is fenced too.
				var tl strings.Builder
				for i, e := range list(ev["data"]) {
					if i > 0 {
						tl.WriteString("\n")
					}
					fmt.Fprintf(&tl, "- %s %s", str(e["at"]), str(e["type"]))
					if d := obj(e["data"]); len(d) > 0 {
						fmt.Fprintf(&tl, " %s", jsonText(d))
					}
				}
				b.WriteString("\n\n" + gameBlock("Timeline", tl.String()))
			} else {
				fmt.Fprintf(&b, "\n\nTimeline: not readable (%v)", err)
			}
			if logs, err := s.client.Get(ctx, base+"/logs", nil); err == nil {
				fmt.Fprintf(&b, "\n\nLogs (read one with read_game_server_log): %s", jsonText(logs["sources"]))
			}
			if text, err := s.logText(ctx, in.ID, "console", 80); err == nil {
				b.WriteString("\n\n" + gameBlock("Last 80 console lines", text))
			} else {
				fmt.Fprintf(&b, "\n\nConsole: not readable (%v)", err)
			}
			if arts, err := s.client.Get(ctx, base+"/artifacts", url.Values{"limit": {"50"}}); err == nil && len(list(arts["data"])) > 0 {
				// The game names these files.
				var kept []string
				for _, a := range list(arts["data"]) {
					kept = append(kept, fmt.Sprintf("- %s %s (%s bytes)", str(a["kind"]), str(a["path"]), num(a["bytes"])))
				}
				b.WriteString("\n\n" + gameBlock("Kept after exit (read them with read_game_server_file)", strings.Join(kept, "\n")))
			}
			if !kept {
				b.WriteString("\n\n" + notKept(in.ID, "diagnose_game_server"))
			}
			return plainResult(b.String()), nil
		})

	tool(s, server, "read_game_server_log", "Read a game server's log", "The end of one of a game server's logs, while it runs or after it exited (kept 90 days).", reads, nil,
		func(ctx context.Context, req *mcp.CallToolRequest, in logIn) (*mcp.CallToolResult, error) {
			gs, err := s.gameServer(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			kept := s.awaitKept(ctx, req, gs)
			tail := clamp(in.Tail, 200, 5000)
			source := firstOf(in.Source, "console")
			text, err := s.logText(ctx, in.ID, source, tail)
			var p *api.Problem
			if !kept && errors.As(err, &p) && p.Status == http.StatusNotFound {
				return plainResult(notKept(in.ID, "read_game_server_log")), nil
			}
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(text) == "" {
				return plainResult(fmt.Sprintf("The %s log of %s is empty.", source, in.ID)), nil
			}
			return plainResult(gameBlock(fmt.Sprintf("Last %d lines of the %s log of %s", tail, source, in.ID), text)), nil
		})

	tool(s, server, "list_game_server_files", "List a game server's files", "A folder of a game server's own folder: while it runs, or after it exited for the files its version keeps (logs, saves, results, crash dumps).", reads, nil,
		func(ctx context.Context, req *mcp.CallToolRequest, in filesIn) (*mcp.CallToolResult, error) {
			gs, err := s.gameServer(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			kept := s.awaitKept(ctx, req, gs)
			q := url.Values{"limit": {"500"}}
			setQ(q, "path", in.Path)
			page, err := s.client.Get(ctx, "/v0/game-servers/"+api.PathEscape(in.ID)+"/files", q)
			if err != nil {
				return nil, err
			}
			rows := []map[string]any{}
			for _, f := range list(page["data"]) {
				rows = append(rows, pick(f, "path", "type", "bytes", "modified_at"))
			}
			state := "kept after exit"
			if page["live"] == true {
				state = "live"
			}
			summary := fmt.Sprintf("%s (%s): %d entries.", firstOf(str(page["path"]), "/"), state, len(rows))
			if !kept {
				summary += " " + notKept(in.ID, "list_game_server_files")
			}
			// The game names its files and folders.
			return plainResult(summary + "\n\n" + gameJSON("The entries", rows)), nil
		})

	tool(s, server, "read_game_server_file", "Read a game server's file", "Reads a text file from a game server's folder (a config file, a log, a result). For a binary or large file it returns a download link valid 15 minutes instead.", reads, nil,
		func(ctx context.Context, req *mcp.CallToolRequest, in fileIn) (*mcp.CallToolResult, error) {
			gs, err := s.gameServer(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			kept := s.awaitKept(ctx, req, gs)
			link, err := s.client.DownloadLink(ctx, "/v0/game-servers/"+api.PathEscape(in.ID)+"/files/download", url.Values{"path": {in.Path}})
			var p *api.Problem
			if !kept && errors.As(err, &p) && p.Status == http.StatusNotFound {
				return plainResult(notKept(in.ID, "read_game_server_file")), nil
			}
			if err != nil {
				return nil, err
			}
			max := clamp(in.MaxBytes, 64<<10, 1<<20)
			data, total, err := fetchStart(ctx, s.cfg.Storage, link, max)
			if err != nil {
				return nil, err
			}
			if !utf8.Valid(data) || strings.ContainsRune(string(data), 0) {
				return plainResult(fmt.Sprintf("%s is not a text file (%s). Download link, valid 15 minutes: %s", in.Path, sizeText(total), link)), nil
			}
			note := ""
			if total > int64(len(data)) {
				note = fmt.Sprintf(" (the first %s of %s; the whole file: %s)", sizeText(int64(len(data))), sizeText(total), link)
			}
			return plainResult(gameBlock(in.Path+note, string(data))), nil
		})
}

// gameServer reads a game server and checks it belongs to an allowed app.
func (s *srv) gameServer(ctx context.Context, id string) (map[string]any, error) {
	gs, err := s.client.Get(ctx, "/v0/game-servers/"+api.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	return gs, s.objectAllowed(ctx, gs, "game server")
}

// recentExit is how long after a game server's exit the tools wait for its machine to send what it left behind;
// past that, a run with nothing sent yet is reported, not waited for (its machine may be away).
const recentExit = 2 * time.Minute

// awaitKept is for the tools that read what a game server left behind. Its machine sends the last run's logs and
// files a few seconds after the exit (the timeline's artifacts_collected after exited); until then the API has no
// console log for it and answers not found. For a game server that exited in the last two minutes it waits (up to
// KeptWait, with progress) until they are in. It reports false only when that run's logs and files are known not
// to be in yet; a running game server, one that never ran, or a timeline it cannot read count as in.
func (s *srv) awaitKept(ctx context.Context, req *mcp.CallToolRequest, gs map[string]any) bool {
	if st := str(gs["status"]); st != "stopped" && st != "failed" {
		return true
	}
	id := str(gs["id"])
	pending, exitedAt, err := s.keptPending(ctx, id)
	if err != nil || !pending {
		return true
	}
	if exitedAt.IsZero() || time.Since(exitedAt) > recentExit {
		return false
	}
	_, done, _, err := s.wait(ctx, req, int((s.cfg.KeptWait+time.Second-1)/time.Second), func(ctx context.Context) (map[string]any, error) {
		pending, _, err := s.keptPending(ctx, id)
		return map[string]any{"pending": pending}, err
	}, func(m map[string]any) (bool, string) {
		return m["pending"] == false, "waiting for the machine to send the logs and files of the last run"
	})
	return err == nil && done
}

// keptPending reads a game server's whole timeline: whether its last exit has no artifacts_collected after it, and
// when that exit was.
func (s *srv) keptPending(ctx context.Context, id string) (bool, time.Time, error) {
	pending := false
	var exitedAt time.Time
	q := url.Values{"limit": {"500"}}
	for range 20 {
		page, err := s.client.Get(ctx, "/v0/game-servers/"+api.PathEscape(id)+"/events", q)
		if err != nil {
			return false, time.Time{}, err
		}
		for _, e := range list(page["data"]) {
			switch str(e["type"]) {
			case "exited":
				pending = true
				exitedAt, _ = time.Parse(time.RFC3339Nano, str(e["at"]))
			case "artifacts_collected":
				pending = false
			}
		}
		next := str(page["next_cursor"])
		if next == "" {
			break
		}
		q.Set("cursor", next)
	}
	return pending, exitedAt, nil
}

// notKept is the answer for a game server whose last run's logs and files its machine has not sent yet.
func notKept(id, tool string) string {
	return fmt.Sprintf("The machine has not sent the logs and files of %s's last run yet: they come a few seconds after the exit, or once a machine that lost its link is back. Next: call %s again in a minute.", id, tool)
}

// ttl is the run time to ask for: the agent's, capped at MaxTTL, or MaxTTL when the agent sets none.
func (s *srv) ttl(asked string) (string, error) {
	if asked == "" {
		if s.cfg.MaxTTL > 0 {
			return formatDuration(s.cfg.MaxTTL), nil
		}
		return "", nil
	}
	d, err := parseDuration(asked)
	if err != nil {
		return "", fmt.Errorf("ttl: %w", err)
	}
	if s.cfg.MaxTTL > 0 && (d == 0 || d > s.cfg.MaxTTL) {
		return "", fmt.Errorf("ttl %s is longer than this server allows (%s, set by GS_MAX_TTL); ask the developer before running a game server longer, or use a persistent server for one that stays up", asked, formatDuration(s.cfg.MaxTTL))
	}
	return asked, nil
}

func (s *srv) logText(ctx context.Context, id, source string, tail int) (string, error) {
	reply, err := s.client.Do(ctx, api.Call{Method: http.MethodGet, Path: "/v0/game-servers/" + api.PathEscape(id) + "/logs/stream",
		Query: url.Values{"source": {source}, "tail": {api.Itoa(tail)}}, Accept: "text/plain"})
	if err != nil {
		return "", err
	}
	text := string(reply.Body)
	if len(text) > 200<<10 {
		text = "...\n" + text[len(text)-200<<10:]
	}
	return strings.TrimRight(text, "\n"), nil
}

// fetchStart reads up to max bytes from a download link (no API key: the link carries its own permission) and the
// file's whole size.
func fetchStart(ctx context.Context, client *http.Client, link string, max int) ([]byte, int64, error) {
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, 0, fmt.Errorf("the download link answered %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(max)))
	if err != nil {
		return nil, 0, err
	}
	total := resp.ContentLength
	if total < int64(len(data)) {
		total = int64(len(data))
	}
	return data, total, nil
}

// gsSummary is one or two sentences on a game server: where it is, whether players can join, how it ended.
func gsSummary(gs map[string]any, what string) string {
	id, st := str(gs["id"]), str(gs["status"])
	switch st {
	case "running":
		s := fmt.Sprintf("%s %s is ready in %s. Players connect to %s.", what, id, str(gs["region"]), address(gs))
		if exp := str(gs["expires_at"]); exp != "" {
			s += " It stops on its own at " + exp + "."
		}
		return s
	case "starting":
		return fmt.Sprintf("%s %s is still starting in %s (address %s once ready). Call wait_for_game_server again.", what, id, str(gs["region"]), address(gs))
	case "stopping":
		return fmt.Sprintf("%s %s is stopping.", what, id)
	default:
		ex := obj(gs["exit"])
		s := fmt.Sprintf("%s %s has %s", what, id, st)
		if r := str(ex["reason"]); r != "" {
			s += " (" + r
			if c := ex["code"]; c != nil {
				s += ", exit code " + num(c)
			}
			if ex["crashed"] == true && str(ex["reason"]) != "crashed" {
				s += ", crashed"
			}
			s += ")"
		}
		s += "."
		if st == "failed" || ex["crashed"] == true {
			s += " diagnose_game_server shows why."
		}
		return s
	}
}

func setIf(m map[string]any, k, v string) {
	if v != "" {
		m[k] = v
	}
}

func setQ(q url.Values, k, v string) {
	if v != "" {
		q.Set(k, v)
	}
}

func clamp(v, def, max int) int {
	switch {
	case v <= 0:
		return def
	case v > max:
		return max
	}
	return v
}

func sizeText(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}
