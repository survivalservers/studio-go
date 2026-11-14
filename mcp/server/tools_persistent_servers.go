package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	api "github.com/survivalservers/studio-go"
)

var persistentServerStatuses = []string{"starting", "running", "stopping", "stopped", "moving", "failed", "paused", "resuming"}

// backupKinds are the words for the API's backup kinds, in the order list_backups explains them.
var backupKinds = []struct{ kind, word, line string }{
	{"continuous", "Continuous", "Copied within seconds of each game save; the newest 3 are kept, apart from your other backups."},
	{"scheduled", "Scheduled", "Taken on the backup schedule while it runs."},
	{"stop", "At stop", "Taken every time it stops."},
	{"manual", "By hand", "Taken with back_up_persistent_server."},
	{"recovered", "Recovered", "Newer saves found on a machine that came back after the persistent server moved."},
	{"before_restore", "Before restore", "How it was just before a restore or rollback; undo goes back to it."},
	{"before_update", "Before update", "Taken at the stop before a new version started: the old version's last save, kept 30 days."},
	{"imported", "Imported", "Saves you uploaded with upload_save."},
}

// backupWord is the word for a backup kind, or the kind itself when it is new.
func backupWord(kind string) string {
	for _, k := range backupKinds {
		if k.kind == kind {
			return k.word
		}
	}
	return kind
}

func backupsDescription() string {
	var b strings.Builder
	b.WriteString("A persistent server's backups, newest first, each with its kind:")
	for _, k := range backupKinds {
		fmt.Fprintf(&b, " %s (%s): %s", k.word, k.kind, k.line)
	}
	return b.String()
}

type backupPolicyIn struct {
	Every   string `json:"every,omitempty" jsonschema:"how often to back up the saves while it runs, for example 6h (default 10m)"`
	KeepFor string `json:"keep_for,omitempty" jsonschema:"how long backups are kept: 7d, 30d (default) or 1y"`
}

type restartIn struct {
	Schedule string `json:"schedule" jsonschema:"a cron schedule for planned restarts, for example 0 5 * * *"`
	Timezone string `json:"timezone,omitempty" jsonschema:"the schedule's time zone, for example Europe/Berlin (default UTC)"`
}

type whenEmptyIn struct {
	Enabled   *bool  `json:"enabled,omitempty" jsonschema:"false turns pausing off"`
	After     string `json:"after,omitempty" jsonschema:"paused after this long with no players, 2m to 24h (default 10m)"`
	Then      string `json:"then,omitempty" jsonschema:"pause (default: resumes in well under a second with its memory kept) or stop (a backup, then stopped: resuming is a fresh start)"`
	StopAfter string `json:"stop_after,omitempty" jsonschema:"a paused persistent server stops fully after this long paused, 10m to 7d, or 0 for never (default 1h)"`
}

type createPersistentServerIn struct {
	App         string            `json:"app" jsonschema:"the app's slug or ID"`
	Name        string            `json:"name" jsonschema:"the persistent server's name, for example EU Official PvE 1"`
	Region      string            `json:"region" jsonschema:"the region it lives in (from list_regions); its address stays the same across restarts"`
	Version     string            `json:"version,omitempty" jsonschema:"the version to run (or branch)"`
	Branch      string            `json:"branch,omitempty" jsonschema:"follow this branch: the persistent server updates to its version at the next restart (or version)"`
	CPU         float64           `json:"cpu,omitempty" jsonschema:"vCPUs, when not the version's size"`
	MemoryGB    float64           `json:"memory_gb,omitempty" jsonschema:"memory in GB, when not the version's size"`
	Env         map[string]string `json:"env,omitempty" jsonschema:"extra environment variables"`
	Args        []string          `json:"args,omitempty" jsonschema:"extra launch arguments"`
	Labels      map[string]string `json:"labels,omitempty" jsonschema:"labels to find it by"`
	SavePaths   []string          `json:"save_paths,omitempty" jsonschema:"folders that hold its saves, at least one: the only folders kept between runs and copied into backups; relative to the top of the build for Windows, absolute inside the container for Linux images (for example /data/saves)"`
	Backup      *backupPolicyIn   `json:"backup,omitempty" jsonschema:"when backups are taken and how many are kept"`
	Restart     *restartIn        `json:"restart,omitempty" jsonschema:"planned restarts"`
	WhenEmpty   *whenEmptyIn      `json:"when_empty,omitempty" jsonschema:"pause when nobody plays and resume on the first player, who is answered at once"`
	Start       *bool             `json:"start,omitempty" jsonschema:"start it now (default true)"`
	RetryKey    string            `json:"idempotency_key,omitempty" jsonschema:"send the same key when you retry this create, so a retry returns the first persistent server instead of making a second; default a new key for every call"`
	WaitSeconds *int              `json:"wait_seconds,omitempty" jsonschema:"how long to wait for it to be running: 0 returns at once; default 180, at most 900"`
}

type persistentServerIn struct {
	ID          string `json:"id" jsonschema:"the persistent server's ID"`
	WaitSeconds *int   `json:"wait_seconds,omitempty" jsonschema:"how long to wait for it to settle: 0 returns at once; default 180, at most 900"`
}

type stopPersistentServerIn struct {
	ID          string `json:"id" jsonschema:"the persistent server's ID"`
	Grace       string `json:"shutdown_time,omitempty" jsonschema:"time to save and exit before it is killed, for example 60s"`
	WaitSeconds *int   `json:"wait_seconds,omitempty" jsonschema:"how long to wait for it to be stopped: 0 returns at once; default 180, at most 900"`
	Approval    string `json:"approval,omitempty" jsonschema:"only after approval_required: the request's ID (apr_...), once a member said yes in the panel"`
}

type listPersistentServersIn struct {
	App    string   `json:"app,omitempty" jsonschema:"only this app's persistent servers"`
	Status []string `json:"status,omitempty" jsonschema:"only persistent servers in these states"`
}

type backupsIn struct {
	ID string `json:"id" jsonschema:"the persistent server's ID"`
}

type planRollbackIn struct {
	At     string `json:"at,omitempty" jsonschema:"RFC 3339 time: each server goes back to its newest backup from before this moment"`
	Backup string `json:"backup,omitempty" jsonschema:"one server only: go back to this backup, from list_backups, with its ID in ids"`
	// BeforeUpdate is a rollout of persistent servers, in place of at.
	BeforeUpdate string            `json:"before_update,omitempty" jsonschema:"in place of at, a rollout's bulk action ID: each server goes back to its last save before that rollout's update (send version then to put the old version back too)"`
	IDs          []string          `json:"ids,omitempty" jsonschema:"only these persistent servers"`
	App          string            `json:"app,omitempty" jsonschema:"only this app's persistent servers"`
	Continents   []string          `json:"continents,omitempty" jsonschema:"only these continents"`
	Regions      []string          `json:"regions,omitempty" jsonschema:"only these regions"`
	Labels       map[string]string `json:"labels,omitempty" jsonschema:"only servers with these labels"`
	Versions     []string          `json:"versions,omitempty" jsonschema:"only servers on these versions (name the app too)"`
	Version      string            `json:"version,omitempty" jsonschema:"keep the version each runs now (default), or then: also put back the version it ran then"`
	TryOneFirst  bool              `json:"try_one_first,omitempty" jsonschema:"do one server, then pause until resumed"`
}

type rollBackIn struct {
	Plan string `json:"plan" jsonschema:"the plan_id plan_rollback answered"`
}

// rollbackPlan is what plan_rollback's plan_id carries: the preview's request (one server's restore, or a rollback of
// many), so roll_back can make the preview again and check it, and what the developer was shown: each server with the
// backup it goes back to, and when the preview runs out. Like plan_bulk_action's, it keeps the hosted server stateless
// and is a guard against acting blind, not a permission.
type rollbackPlan struct {
	Server  string         `json:"server,omitempty"`
	Body    map[string]any `json:"body"`
	Targets string         `json:"targets"`
	Expires string         `json:"expires,omitempty"`
}

type downloadBackupIn struct {
	Backup string `json:"backup" jsonschema:"the backup's ID, from list_backups"`
}

type uploadSaveIn struct {
	ID      string `json:"id" jsonschema:"the persistent server's ID"`
	ZipPath string `json:"zip_path" jsonschema:"path on this computer to a zip of saves, up to 5 GB"`
	Label   string `json:"label,omitempty" jsonschema:"your name for the backup"`
}

type updatePersistentServerIn struct {
	ID        string            `json:"id" jsonschema:"the persistent server's ID"`
	Version   string            `json:"version,omitempty" jsonschema:"run this version from now on"`
	Branch    string            `json:"branch,omitempty" jsonschema:"follow this branch from now on"`
	Env       map[string]string `json:"env,omitempty" jsonschema:"replace its extra environment variables"`
	Args      []string          `json:"args,omitempty" jsonschema:"replace its extra launch arguments"`
	WhenEmpty *whenEmptyIn      `json:"when_empty,omitempty" jsonschema:"change when it pauses; {\"enabled\": false} turns pausing off"`
	Apply     string            `json:"apply,omitempty" jsonschema:"next_restart (default: players are not interrupted) or now (restarts it, with a backup at the stop)"`
}

func (s *srv) addPersistentServerTools(server *mcp.Server) {
	tool(s, server, "list_persistent_servers", "List persistent servers", "Persistent servers: game servers that keep running with their saves and address, with their state, address, players and last backup.", reads,
		map[string][]string{"status": persistentServerStatuses},
		func(ctx context.Context, _ *mcp.CallToolRequest, in listPersistentServersIn) (*mcp.CallToolResult, error) {
			q := url.Values{"limit": {"500"}}
			if in.App != "" {
				if err := s.allowedApp(ctx, in.App); err != nil {
					return nil, err
				}
				q.Set("app", in.App)
			}
			if len(in.Status) > 0 {
				q.Set("status", strings.Join(in.Status, ","))
			}
			page, err := s.client.Get(ctx, "/v0/persistent-servers", q)
			if err != nil {
				return nil, err
			}
			items, err := s.filterAllowed(ctx, list(page["data"]), "app")
			if err != nil {
				return nil, err
			}
			rows := []map[string]any{}
			for _, p := range items {
				r := pick(p, "id", "name", "status", "app", "version", "branch", "region", "players", "last_backup")
				if a := address(p); a != "" {
					r["address"] = a
				}
				rows = append(rows, r)
			}
			return textResult(fmt.Sprintf("%d persistent servers.", len(rows)), rows), nil
		})

	tool(s, server, "create_persistent_server", "Create a persistent server", "Makes a persistent server in one region: it keeps its address and saves across restarts, backs up its saves, restarts on a schedule, and can pause when nobody plays. It is billed for as long as it runs, until stop_persistent_server.", changes,
		map[string][]string{"when_empty.then": {"pause", "stop"}},
		func(ctx context.Context, req *mcp.CallToolRequest, in createPersistentServerIn) (*mcp.CallToolResult, error) {
			if err := s.allowedApp(ctx, in.App); err != nil {
				return nil, err
			}
			if (in.Version == "") == (in.Branch == "") {
				return nil, fmt.Errorf("give either version or branch")
			}
			body := map[string]any{"app": in.App, "name": in.Name, "region": in.Region}
			setIf(body, "version", in.Version)
			setIf(body, "branch", in.Branch)
			if in.CPU > 0 || in.MemoryGB > 0 {
				if in.CPU <= 0 || in.MemoryGB <= 0 {
					return nil, fmt.Errorf("give both cpu and memory_gb, or neither for the version's size")
				}
				body["size"] = map[string]any{"cpu": in.CPU, "memory_gb": in.MemoryGB}
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
			if len(in.SavePaths) > 0 {
				body["save_paths"] = in.SavePaths
			}
			if p := in.Backup; p != nil {
				bp := map[string]any{}
				setIf(bp, "every", p.Every)
				setIf(bp, "keep_for", p.KeepFor)
				body["backup"] = bp
			}
			if r := in.Restart; r != nil {
				rs := map[string]any{"schedule": r.Schedule}
				setIf(rs, "timezone", r.Timezone)
				body["restart"] = rs
			}
			if in.WhenEmpty != nil {
				body["when_empty"] = whenEmptyBody(in.WhenEmpty)
			}
			start := in.Start == nil || *in.Start
			body["start"] = start
			var p map[string]any
			if err := s.client.JSON(ctx, api.Call{Method: http.MethodPost, Path: "/v0/persistent-servers", Body: body, IdempotencyKey: retryKey(in.RetryKey, "", "")}, &p); err != nil {
				return nil, err
			}
			if start {
				p = s.settlePersistentServer(ctx, req, p, in.WaitSeconds)
			}
			return textResult(persistentServerSummary(p), p), nil
		})

	tool(s, server, "wait_for_persistent_server", "Read or wait for a persistent server", "Reads a persistent server and waits until it settles (running, stopped, paused or failed): state, address, players, running game server and last backup.", reads, nil,
		func(ctx context.Context, req *mcp.CallToolRequest, in persistentServerIn) (*mcp.CallToolResult, error) {
			p, err := s.persistentServer(ctx, in.ID)
			if err != nil {
				return nil, err
			}
			p = s.settlePersistentServer(ctx, req, p, in.WaitSeconds)
			return textResult(persistentServerSummary(p), p), nil
		})

	tool(s, server, "start_persistent_server", "Start a persistent server", "Starts a stopped persistent server on its machine and port, from its saves.", changes, nil,
		func(ctx context.Context, req *mcp.CallToolRequest, in persistentServerIn) (*mcp.CallToolResult, error) {
			if _, err := s.persistentServer(ctx, in.ID); err != nil {
				return nil, err
			}
			var p map[string]any
			if err := s.client.JSON(ctx, api.Call{Method: http.MethodPost, Path: persistentServerPath(in.ID) + "/start"}, &p); err != nil {
				return nil, err
			}
			p = s.settlePersistentServer(ctx, req, p, in.WaitSeconds)
			return textResult(persistentServerSummary(p), p), nil
		})

	tool(s, server, "stop_persistent_server", "Stop a persistent server", "Stops a persistent server: it saves and exits, a backup is taken at the stop, and billing stops. Its saves, backups and address are kept for start_persistent_server.", removes, nil,
		func(ctx context.Context, req *mcp.CallToolRequest, in stopPersistentServerIn) (*mcp.CallToolResult, error) {
			if _, err := s.persistentServer(ctx, in.ID); err != nil {
				return nil, err
			}
			body := map[string]any{}
			setIf(body, "shutdown_time", in.Grace)
			var p map[string]any
			if err := s.client.JSON(withApproval(ctx, in.Approval), api.Call{Method: http.MethodPost, Path: persistentServerPath(in.ID) + "/stop", Body: body}, &p); err != nil {
				return nil, err
			}
			p = s.settlePersistentServer(ctx, req, p, in.WaitSeconds)
			return textResult(persistentServerSummary(p), p), nil
		})

	tool(s, server, "back_up_persistent_server", "Back up a persistent server", "Backs up a running persistent server's saves now (for example before an update), kept until its backup policy deletes it.", changes, nil,
		func(ctx context.Context, _ *mcp.CallToolRequest, in backupsIn) (*mcp.CallToolResult, error) {
			if _, err := s.persistentServer(ctx, in.ID); err != nil {
				return nil, err
			}
			var b map[string]any
			if err := s.client.JSON(ctx, api.Call{Method: http.MethodPost, Path: persistentServerPath(in.ID) + "/backups"}, &b); err != nil {
				return nil, err
			}
			return textResult(fmt.Sprintf("Backup %s of persistent server %s is %s.", str(b["id"]), in.ID, str(b["status"])), b), nil
		})

	tool(s, server, "list_backups", "List a persistent server's backups", backupsDescription(), reads, nil,
		func(ctx context.Context, _ *mcp.CallToolRequest, in backupsIn) (*mcp.CallToolResult, error) {
			if _, err := s.persistentServer(ctx, in.ID); err != nil {
				return nil, err
			}
			page, err := s.client.Get(ctx, persistentServerPath(in.ID)+"/backups", url.Values{"limit": {"100"}})
			if err != nil {
				return nil, err
			}
			rows := list(page["data"])
			return textResult(fmt.Sprintf("%d backups of persistent server %s.", len(rows), in.ID), rows), nil
		})

	tool(s, server, "plan_rollback", "Preview a restore or rollback", "Previews going back: one server to a backup or a time, or many to before a time or to before a rollout's update. Nothing changes. Show the developer the table it answers (each server, where it goes back to and how much play is lost) and get their yes before calling roll_back with its plan_id.", reads,
		map[string][]string{"version": {"keep", "then"}},
		func(ctx context.Context, _ *mcp.CallToolRequest, in planRollbackIn) (*mcp.CallToolResult, error) {
			if in.At == "" && in.Backup == "" && in.BeforeUpdate == "" {
				return nil, errors.New("give at, before_update, or backup with one server in ids")
			}
			var rp rollbackPlan
			if in.Backup != "" {
				if len(in.IDs) != 1 {
					return nil, errors.New("a backup belongs to one server: name it in ids")
				}
				if _, err := s.persistentServer(ctx, in.IDs[0]); err != nil {
					return nil, err
				}
				if err := s.backupAllowed(ctx, in.Backup); err != nil {
					return nil, err
				}
				rp = rollbackPlan{Server: in.IDs[0], Body: map[string]any{"backup": in.Backup, "version": orKeep(in.Version), "dry_run": true}}
			} else {
				if s.limited() && in.App == "" {
					return nil, s.needApp("app")
				}
				if in.App != "" {
					if err := s.allowedApp(ctx, in.App); err != nil {
						return nil, err
					}
				}
				sel := map[string]any{"target": "persistent_servers"}
				setIf(sel, "app", in.App)
				for name, v := range map[string][]string{"ids": in.IDs, "continents": in.Continents, "regions": in.Regions, "versions": in.Versions} {
					if len(v) > 0 {
						sel[name] = v
					}
				}
				if len(in.Labels) > 0 {
					sel["labels"] = in.Labels
				}
				body := map[string]any{"selector": sel, "version": orKeep(in.Version)}
				if in.BeforeUpdate != "" {
					body["before_update"] = in.BeforeUpdate
				} else {
					body["at"] = in.At
				}
				if in.TryOneFirst {
					body["rollout"] = map[string]any{"first": map[string]any{"count": 1}}
				}
				rp = rollbackPlan{Body: body}
			}
			plan, err := s.previewRollback(ctx, rp)
			if err != nil {
				return nil, err
			}
			if err := s.targetsAllowed(ctx, plan); err != nil {
				return nil, err
			}
			rp.Targets, rp.Expires = restorePoints(plan), str(plan["expires_at"])
			id, err := json.Marshal(rp)
			if err != nil {
				return nil, err
			}
			planID := base64.RawURLEncoding.EncodeToString(id)
			shown := pick(plan, "total", "players", "targets", "skipped", "rollout", "expires_at")
			return textResult(rollbackTable(plan)+"\n\nplan_id: "+planID, shown), nil
		})

	tool(s, server, "roll_back", "Run a restore or rollback", "Runs a preview from plan_rollback, and only that: if a server or the backup each goes back to changed since, or the preview ran out (15 minutes), it is refused and the preview must be made again. Each server is saved and stopped, its current state is backed up, then it is restored and started. Play since its backup is lost. Show the preview and get the developer's yes first; undo_bulk_action puts every server back.", removes, nil,
		func(ctx context.Context, _ *mcp.CallToolRequest, in rollBackIn) (*mcp.CallToolResult, error) {
			raw, err := base64.RawURLEncoding.DecodeString(in.Plan)
			var rp rollbackPlan
			if err == nil {
				err = json.Unmarshal(raw, &rp)
			}
			if err != nil || rp.Body == nil {
				return nil, errors.New("that is not a plan_id from plan_rollback: call plan_rollback, show its table, then send the plan_id it answered")
			}
			if t, err := time.Parse(time.RFC3339, rp.Expires); err == nil && time.Now().After(t) {
				return nil, errors.New("the preview ran out: call plan_rollback again and show the new table")
			}
			if rp.Server != "" {
				if _, err := s.persistentServer(ctx, rp.Server); err != nil {
					return nil, err
				}
				if b := str(rp.Body["backup"]); b != "" {
					if err := s.backupAllowed(ctx, b); err != nil {
						return nil, err
					}
				}
			} else if s.limited() {
				app := str(obj(rp.Body["selector"])["app"])
				if app == "" {
					return nil, s.needApp("app")
				}
				if err := s.allowedApp(ctx, app); err != nil {
					return nil, err
				}
			}
			// The plan ID is the request, not a permission: make the preview again and run that one, only when it
			// still reaches the same servers with the same backups, every one of an allowed app.
			plan, err := s.previewRollback(ctx, rp)
			if err != nil {
				return nil, err
			}
			if err := s.targetsAllowed(ctx, plan); err != nil {
				return nil, err
			}
			if restorePoints(plan) != rp.Targets {
				return nil, fmt.Errorf("the servers or backups changed since the preview; show this one and get a new yes, then call plan_rollback again for its plan_id. Now:\n\n%s", rollbackTable(plan))
			}
			var res map[string]any
			if err := s.client.JSON(ctx, api.Call{Method: http.MethodPost, Path: "/v0/bulk-actions/rollbacks", Body: map[string]any{"plan": str(plan["id"])}}, &res); err != nil {
				return nil, err
			}
			op, _ := res["bulk_action"].(map[string]any)
			return textResult(fmt.Sprintf("Rolling back as bulk action %s. undo_bulk_action puts every server back as it was just before.", str(op["id"])), op), nil
		})

	tool(s, server, "download_backup", "Download a backup", "Builds a zip of a backup, with a SHA256SUMS file, and returns a link good for 15 minutes once it is ready. While it builds, call this again. Backups hold player data: the key needs backups:read.", reads, nil,
		func(ctx context.Context, _ *mcp.CallToolRequest, in downloadBackupIn) (*mcp.CallToolResult, error) {
			if err := s.backupAllowed(ctx, in.Backup); err != nil {
				return nil, err
			}
			var e map[string]any
			if err := s.client.JSON(ctx, api.Call{Method: http.MethodPost, Path: "/v0/backups/" + url.PathEscape(in.Backup) + "/export"}, &e); err != nil {
				return nil, err
			}
			switch str(e["status"]) {
			case "ready":
				return textResult(fmt.Sprintf("Backup %s is ready: %s", in.Backup, str(e["url"])), e), nil
			case "failed":
				return nil, fmt.Errorf("the zip could not be built: %s", str(e["error"]))
			}
			return textResult(fmt.Sprintf("The zip of %s is being built. Call download_backup again in a moment.", in.Backup), e), nil
		})

	if s.cfg.Local {
		tool(s, server, "upload_save", "Upload a save", "Sends a zip of saves from this computer and keeps it as an Imported backup of the persistent server. The server stays as it is until you restore it with plan_rollback and roll_back. Every path in the zip must be under one of the server's save paths.", changes, nil,
			func(ctx context.Context, req *mcp.CallToolRequest, in uploadSaveIn) (*mcp.CallToolResult, error) {
				if _, err := s.persistentServer(ctx, in.ID); err != nil {
					return nil, err
				}
				f, err := os.Open(in.ZipPath)
				if err != nil {
					return nil, err
				}
				defer f.Close()
				fi, err := f.Stat()
				if err != nil {
					return nil, err
				}
				body := map[string]any{"bytes": fi.Size()}
				if in.Label != "" {
					body["label"] = in.Label
				}
				base := persistentServerPath(in.ID) + "/backups/uploads"
				var up map[string]any
				if err := s.client.JSON(ctx, api.Call{Method: http.MethodPost, Path: base, Body: body}, &up); err != nil {
					return nil, err
				}
				put, err := http.NewRequestWithContext(ctx, http.MethodPut, str(up["url"]), f)
				if err != nil {
					return nil, err
				}
				put.ContentLength = fi.Size()
				resp, err := http.DefaultClient.Do(put)
				if err != nil {
					return nil, err
				}
				resp.Body.Close()
				if resp.StatusCode >= 300 {
					return nil, fmt.Errorf("storage answered %d", resp.StatusCode)
				}
				at := base + "/" + url.PathEscape(str(up["id"]))
				if err := s.client.JSON(ctx, api.Call{Method: http.MethodPost, Path: at + "/complete"}, nil); err != nil {
					return nil, err
				}
				done, _, _, err := s.wait(ctx, req, 600, func(ctx context.Context) (map[string]any, error) { return s.client.Get(ctx, at, nil) },
					func(u map[string]any) (bool, string) { return str(u["status"]) != "processing", str(u["status"]) })
				if err != nil {
					return nil, err
				}
				switch str(done["status"]) {
				case "done":
					return textResult(fmt.Sprintf("Backup %s holds your save. The server stays as it is until you restore it.", str(done["backup"])), done), nil
				case "failed":
					return nil, fmt.Errorf("the save was not taken: %s", str(done["error"]))
				}
				return textResult("The save is still being checked. It shows in list_backups once stored.", done), nil
			})
	}

	tool(s, server, "undo_bulk_action", "Undo a bulk action", "Puts every target of a finished rollback, rollout or config change back as it was just before it. Confirm with the developer before calling this.", removes, nil,
		func(ctx context.Context, _ *mcp.CallToolRequest, in bulkActionIn) (*mcp.CallToolResult, error) {
			if _, err := s.bulkActionAllowed(ctx, in.ID, false); err != nil {
				return nil, err
			}
			var res map[string]any
			if err := s.client.JSON(ctx, api.Call{Method: http.MethodPost, Path: "/v0/bulk-actions/" + url.PathEscape(in.ID) + "/undo", Body: map[string]any{}}, &res); err != nil {
				return nil, err
			}
			op, _ := res["bulk_action"].(map[string]any)
			return textResult(fmt.Sprintf("Undoing %s as bulk action %s.", in.ID, str(op["id"])), op), nil
		})

	tool(s, server, "update_persistent_server", "Update a persistent server", "Moves a persistent server to another version or branch, or changes its extra settings or when it pauses: at its next restart by default, or now. A paused persistent server takes a new version at once, since nobody is on it.", removes,
		map[string][]string{"apply": {"next_restart", "now"}, "when_empty.then": {"pause", "stop"}},
		func(ctx context.Context, _ *mcp.CallToolRequest, in updatePersistentServerIn) (*mcp.CallToolResult, error) {
			if _, err := s.persistentServer(ctx, in.ID); err != nil {
				return nil, err
			}
			body := map[string]any{}
			setIf(body, "version", in.Version)
			setIf(body, "branch", in.Branch)
			setIf(body, "apply", in.Apply)
			if in.Env != nil {
				body["env"] = in.Env
			}
			if in.Args != nil {
				body["args"] = in.Args
			}
			if in.WhenEmpty != nil {
				body["when_empty"] = whenEmptyBody(in.WhenEmpty)
			}
			if len(body) == 0 || (len(body) == 1 && body["apply"] != nil) {
				return nil, fmt.Errorf("nothing to change: give version, branch, env, args or when_empty")
			}
			var p map[string]any
			if err := s.client.JSON(ctx, api.Call{Method: http.MethodPatch, Path: persistentServerPath(in.ID), Body: body}, &p); err != nil {
				return nil, err
			}
			when := "at its next restart"
			if in.Apply == "now" {
				when = "now"
			}
			return textResult(fmt.Sprintf("Persistent server %s updated; it takes effect %s.", in.ID, when), p), nil
		})
}

func whenEmptyBody(in *whenEmptyIn) map[string]any {
	out := map[string]any{}
	if in.Enabled != nil {
		out["enabled"] = *in.Enabled
	}
	setIf(out, "after", in.After)
	setIf(out, "then", in.Then)
	setIf(out, "stop_after", in.StopAfter)
	return out
}

func persistentServerPath(id string) string { return "/v0/persistent-servers/" + api.PathEscape(id) }

// persistentServer reads a persistent server and checks it belongs to an allowed app.
func (s *srv) persistentServer(ctx context.Context, id string) (map[string]any, error) {
	p, err := s.client.Get(ctx, persistentServerPath(id), nil)
	if err != nil {
		return nil, err
	}
	return p, s.objectAllowed(ctx, p, "persistent server")
}

// settlePersistentServer waits for a persistent server to stop changing; on a read error it returns it as it was.
func (s *srv) settlePersistentServer(ctx context.Context, req *mcp.CallToolRequest, p map[string]any, asked *int) map[string]any {
	id := str(p["id"])
	settled, _, _, err := s.wait(ctx, req, waitSeconds(asked, 180), func(ctx context.Context) (map[string]any, error) {
		return s.client.Get(ctx, persistentServerPath(id), nil)
	}, api.PersistentServerSettled)
	if err != nil {
		return p
	}
	return settled
}

func persistentServerSummary(p map[string]any) string {
	id, name, st := str(p["id"]), str(p["name"]), str(p["status"])
	var b strings.Builder
	fmt.Fprintf(&b, "Persistent server %s (%s) in %s is %s", name, id, str(p["region"]), st)
	if want := str(p["desired_state"]); want != "" && want != st && st != "failed" && st != "paused" {
		fmt.Fprintf(&b, ", on its way to %s", want)
	}
	b.WriteString(".")
	if a := address(p); a != "" && (st == "running" || st == "paused") {
		fmt.Fprintf(&b, " Players connect to %s.", a)
	}
	if st == "paused" && str(p["paused_since"]) != "" {
		fmt.Fprintf(&b, " Paused since %s; the first player resumes it.", str(p["paused_since"]))
	}
	if bk := obj(p["last_backup"]); bk != nil {
		fmt.Fprintf(&b, " Last backup %s (%s, %s).", str(bk["id"]), backupWord(str(bk["kind"])), str(bk["created_at"]))
	}
	switch st {
	case "starting", "stopping", "moving", "resuming":
		b.WriteString(" Call wait_for_persistent_server to see it settle.")
	case "failed":
		if gs := str(p["game_server"]); gs != "" {
			fmt.Fprintf(&b, " diagnose_game_server %s shows why.", gs)
		}
	}
	return b.String()
}

// previewRollback asks the API for a rollback's preview: one server's restore, or a rollback of many.
func (s *srv) previewRollback(ctx context.Context, rp rollbackPlan) (map[string]any, error) {
	path := "/v0/bulk-actions/rollbacks"
	if rp.Server != "" {
		path = persistentServerPath(rp.Server) + "/restore"
	}
	var res map[string]any
	if err := s.client.JSON(ctx, api.Call{Method: http.MethodPost, Path: path, Body: rp.Body}, &res); err != nil {
		return nil, err
	}
	plan := obj(res["plan"])
	if plan == nil {
		return nil, errors.New("the API answered no preview")
	}
	return plan, nil
}

// restorePoints is a preview's servers, each with the backup it goes back to, as one string to tell whether they changed.
func restorePoints(plan map[string]any) string {
	var out []string
	for _, t := range list(plan["targets"]) {
		r := obj(t["restore"])
		out = append(out, str(t["id"])+"="+str(r["backup"])+"@"+str(r["version"]))
	}
	slices.Sort(out)
	return strings.Join(out, ",")
}

func orKeep(v string) string {
	if v == "" {
		return "keep"
	}
	return v
}

// rollbackTable is a preview as the assistant shows it to the developer.
func rollbackTable(plan map[string]any) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Preview: %v servers, %v players online now.\n\n", plan["total"], plan["players"])
	b.WriteString("| Server | Region | Players | Back to | Play lost | Version then |\n|---|---|---|---|---|---|\n")
	for _, t := range list(plan["targets"]) {
		r, _ := t["restore"].(map[string]any)
		fmt.Fprintf(&b, "| %s | %s | %v | %s | %s | %s |\n", str(t["name"]), str(t["region"]), t["players"], str(r["saved_at"]), str(r["play_lost"]), str(r["version"]))
	}
	for _, t := range list(plan["skipped"]) {
		fmt.Fprintf(&b, "\nLeft alone: %s: %s", str(t["name"]), str(t["detail"]))
	}
	return b.String()
}
