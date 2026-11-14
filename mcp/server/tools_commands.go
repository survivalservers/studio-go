package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	api "github.com/survivalservers/studio-go"
)

// Commands, game profiles and bulk actions. Anything on many servers takes two steps: plan_bulk_action asks the API
// for a dry run and hands back a plan ID, and run_bulk_action runs that plan only while it still reaches the same
// servers. The plan ID carries the request itself, so the hosted server stays stateless; it is a guard against
// acting blind, not a permission: the key's own scopes decide what may run.

type profileRefIn struct {
	App     string `json:"app" jsonschema:"the app's slug or ID"`
	Version string `json:"version" jsonschema:"the version's name or ID"`
}

type setProfileIn struct {
	App     string         `json:"app" jsonschema:"the app's slug or ID"`
	Version string         `json:"version" jsonschema:"the version's name or ID"`
	Profile map[string]any `json:"profile" jsonschema:"the whole game profile: connections and commands, as the game profiles docs page shows. It replaces the one there"`
}

type sendCommandIn struct {
	ID        string            `json:"id" jsonschema:"the persistent server's or game server's ID"`
	CommandID string            `json:"command_id,omitempty" jsonschema:"a game command in its version's game profile (see get_game_profile)"`
	Args      map[string]string `json:"args,omitempty" jsonschema:"the game command's arguments by name, values as strings"`
	Line      string            `json:"line,omitempty" jsonschema:"instead of command_id, a line to send as it is"`
	Via       string            `json:"via,omitempty" jsonschema:"with line: a connection in the game profile; default console"`
	Approval  string            `json:"approval,omitempty" jsonschema:"only after approval_required: the request's ID (apr_...), once a member said yes in the panel"`
}

type serverIn struct {
	ID string `json:"id" jsonschema:"the persistent server's ID"`
}

type setSettingsIn struct {
	ID     string         `json:"id" jsonschema:"the persistent server's ID"`
	Values map[string]any `json:"values" jsonschema:"values by setting ID, each its setting's type; null takes a value off"`
	Live   bool           `json:"live,omitempty" jsonschema:"also send the settings that can change in the running game"`
}

type selectorIn struct {
	Target     string            `json:"target,omitempty" jsonschema:"persistent_servers (default) or game_servers"`
	App        string            `json:"app,omitempty" jsonschema:"only this app"`
	Versions   []string          `json:"versions,omitempty" jsonschema:"only these versions (with app)"`
	Branch     string            `json:"branch,omitempty" jsonschema:"only those on this branch"`
	Regions    []string          `json:"regions,omitempty" jsonschema:"only these regions"`
	Continents []string          `json:"continents,omitempty" jsonschema:"only these continents, for example europe"`
	Labels     map[string]string `json:"labels,omitempty" jsonschema:"only those with all these labels"`
	IDs        []string          `json:"ids,omitempty" jsonschema:"only these persistent servers or game servers"`
}

type planBulkIn struct {
	Action    string            `json:"action" jsonschema:"command, restart or rollout"`
	Selector  selectorIn        `json:"selector" jsonschema:"which servers; give at least one filter"`
	CommandID string            `json:"command_id,omitempty" jsonschema:"command: a game command, read from each server's own version"`
	Args      map[string]string `json:"args,omitempty" jsonschema:"command: the game command's arguments by name"`
	Line      string            `json:"line,omitempty" jsonschema:"command: instead of command_id, a line to send as it is"`
	Via       string            `json:"via,omitempty" jsonschema:"command: with line, a connection in the game profile; default console"`
	Version   string            `json:"version,omitempty" jsonschema:"rollout: the version to move to (or branch)"`
	Branch    string            `json:"branch,omitempty" jsonschema:"rollout: the branch to follow (or version)"`
	Apply     string            `json:"apply,omitempty" jsonschema:"rollout: next_restart (default) or now, in rolling batches"`
	BatchSize int               `json:"batch_size,omitempty" jsonschema:"how many at a time; default 50 for a command, 10 percent otherwise"`
	Note      string            `json:"note,omitempty" jsonschema:"why, kept with the bulk action and in the audit log"`
}

type runBulkIn struct {
	PlanID   string `json:"plan_id" jsonschema:"the plan_id from plan_bulk_action"`
	Approval string `json:"approval,omitempty" jsonschema:"only after approval_required: the request's ID (apr_...), once a member said yes in the panel"`
}

type bulkActionIn struct {
	ID string `json:"id" jsonschema:"the bulk action's ID"`
}

var bulkPaths = map[string]string{"command": "/v0/bulk-actions/commands", "restart": "/v0/bulk-actions/restarts", "rollout": "/v0/bulk-actions/rollouts"}

// bulkPlan is what a plan ID carries.
type bulkPlan struct {
	Action  string         `json:"action"`
	Body    map[string]any `json:"body"`
	Targets string         `json:"targets"` // the planned servers' IDs, in order
}

// serverPath is where a persistent server's or game server's calls live, told apart by its ID.
func serverPath(id string) (string, error) {
	switch {
	case strings.HasPrefix(id, "ps_"):
		return "/v0/persistent-servers/" + api.PathEscape(id), nil
	case strings.HasPrefix(id, "gs_"):
		return "/v0/game-servers/" + api.PathEscape(id), nil
	}
	return "", fmt.Errorf("%q is not a persistent server or game server ID", id)
}

func (in *planBulkIn) body() map[string]any {
	sel := map[string]any{"target": firstOf(in.Selector.Target, "persistent_servers")}
	for k, v := range map[string]string{"app": in.Selector.App, "branch": in.Selector.Branch} {
		if v != "" {
			sel[k] = v
		}
	}
	for k, v := range map[string][]string{"versions": in.Selector.Versions, "regions": in.Selector.Regions, "continents": in.Selector.Continents, "ids": in.Selector.IDs} {
		if len(v) > 0 {
			sel[k] = v
		}
	}
	if len(in.Selector.Labels) > 0 {
		sel["labels"] = in.Selector.Labels
	}
	body := map[string]any{"selector": sel}
	for k, v := range map[string]string{"command_id": in.CommandID, "line": in.Line, "via": in.Via, "version": in.Version, "branch": in.Branch, "apply": in.Apply, "note": in.Note} {
		if v != "" {
			body[k] = v
		}
	}
	if len(in.Args) > 0 {
		body["args"] = in.Args
	}
	if in.BatchSize > 0 {
		body["rollout"] = map[string]any{"batch_size": in.BatchSize}
	}
	return body
}

// targetIDs is a plan's servers as one string, to tell whether they changed.
func targetIDs(plan map[string]any) string {
	var ids []string
	for _, t := range list(plan["targets"]) {
		ids = append(ids, str(t["id"]))
	}
	slices.Sort(ids)
	return strings.Join(ids, ",")
}

func (s *srv) dryRun(ctx context.Context, action string, body map[string]any) (map[string]any, error) {
	sent := map[string]any{"dry_run": true}
	for k, v := range body {
		sent[k] = v
	}
	var out map[string]any
	err := s.client.JSON(ctx, api.Call{Method: http.MethodPost, Path: bulkPaths[action], Body: sent, IdempotencyKey: api.NewIdempotencyKey()}, &out)
	return obj(out["plan"]), err
}

func planText(plan map[string]any) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Reaches %s servers in %d batches, with %s players on them now.", str(plan["total"]), len(list(plan["batches"])), num(plan["players"]))
	for _, t := range list(plan["skipped"]) {
		fmt.Fprintf(&b, "\nSkips %s: %s", firstOf(str(t["name"]), str(t["id"])), firstOf(str(t["detail"]), str(t["reason"])))
	}
	return b.String()
}

func (s *srv) addCommandTools(server *mcp.Server) {
	tool(s, server, "get_game_profile", "Get a game profile", "A version's game profile: how to reach the game (connections) and its game commands, with their arguments. An empty profile means none was written.", reads, nil,
		func(ctx context.Context, _ *mcp.CallToolRequest, in profileRefIn) (*mcp.CallToolResult, error) {
			if err := s.allowedApp(ctx, in.App); err != nil {
				return nil, err
			}
			r, err := s.client.Get(ctx, "/v0/apps/"+api.PathEscape(in.App)+"/versions/"+api.PathEscape(in.Version)+"/profile", nil)
			if err != nil {
				return nil, err
			}
			return textResult(fmt.Sprintf("Revision %s.", num(r["revision"])), r["profile"]), nil
		})

	tool(s, server, "set_game_profile", "Set a game profile", "Replaces a version's whole game profile: connections and game commands. Passwords stay in the version's env values; a profile only names them. Every save is in the audit log.", removes, nil,
		func(ctx context.Context, _ *mcp.CallToolRequest, in setProfileIn) (*mcp.CallToolResult, error) {
			if err := s.allowedApp(ctx, in.App); err != nil {
				return nil, err
			}
			var out map[string]any
			err := s.client.JSON(ctx, api.Call{Method: http.MethodPut, Path: "/v0/apps/" + api.PathEscape(in.App) + "/versions/" + api.PathEscape(in.Version) + "/profile", Body: in.Profile}, &out)
			if err != nil {
				return nil, err
			}
			return plainResult(fmt.Sprintf("Saved, revision %s.", num(out["revision"]))), nil
		})

	tool(s, server, "send_command", "Send a command", "Runs a game command, or sends a line, on one running persistent server or game server, and returns what the game answered. Use get_game_profile to see the game commands. For many servers use plan_bulk_action.", removes, nil,
		func(ctx context.Context, _ *mcp.CallToolRequest, in sendCommandIn) (*mcp.CallToolResult, error) {
			path, err := serverPath(in.ID)
			if err != nil {
				return nil, err
			}
			target, err := s.client.Get(ctx, path, nil)
			if err != nil {
				return nil, err
			}
			if err := s.objectAllowed(ctx, target, "server"); err != nil {
				return nil, err
			}
			body := map[string]any{}
			for k, v := range map[string]string{"command_id": in.CommandID, "line": in.Line, "via": in.Via} {
				if v != "" {
					body[k] = v
				}
			}
			if len(in.Args) > 0 {
				body["args"] = in.Args
			}
			var out map[string]any
			if err := s.client.JSON(withApproval(ctx, in.Approval), api.Call{Method: http.MethodPost, Path: path + "/command", Body: body, IdempotencyKey: api.NewIdempotencyKey()}, &out); err != nil {
				return nil, err
			}
			if d, _ := out["delivered"].(bool); !d {
				if e := str(out["error"]); e != "" {
					return nil, fmt.Errorf("not delivered.\n\n%s", gameBlock("Why, as the machine or the game answered", e))
				}
				return nil, errors.New("not delivered: the game did not take it")
			}
			if o := str(out["output"]); strings.TrimSpace(o) != "" {
				return plainResult(gameBlock("Delivered. What the game answered", o)), nil
			}
			return plainResult("Delivered; the game answered nothing."), nil
		})

	tool(s, server, "get_server_settings", "Get a server's settings", "A persistent server's game settings: each one's value, where it came from (the game's default, the server itself or a config set) and whether it waits for a restart. get_game_profile says what each one is.", reads, nil,
		func(ctx context.Context, _ *mcp.CallToolRequest, in serverIn) (*mcp.CallToolResult, error) {
			target, err := s.client.Get(ctx, "/v0/persistent-servers/"+api.PathEscape(in.ID), nil)
			if err != nil {
				return nil, err
			}
			if err := s.objectAllowed(ctx, target, "persistent server"); err != nil {
				return nil, err
			}
			r, err := s.client.Get(ctx, "/v0/persistent-servers/"+api.PathEscape(in.ID)+"/config", nil)
			if err != nil {
				return nil, err
			}
			summary := fmt.Sprintf("%d settings.", len(list(r["settings"])))
			if n := str(r["not_applied"]); n != "" {
				summary = n + ". " + summary
			}
			return textResult(summary, r["settings"]), nil
		})

	tool(s, server, "set_server_settings", "Change a server's settings", "Sets a persistent server's own game settings, by the IDs in its game profile; null takes a value off. They are written at its next run; live also sends the ones that can change in the running game. A config set's value wins over these.", changes, nil,
		func(ctx context.Context, _ *mcp.CallToolRequest, in setSettingsIn) (*mcp.CallToolResult, error) {
			target, err := s.client.Get(ctx, "/v0/persistent-servers/"+api.PathEscape(in.ID), nil)
			if err != nil {
				return nil, err
			}
			if err := s.objectAllowed(ctx, target, "persistent server"); err != nil {
				return nil, err
			}
			var out map[string]any
			err = s.client.JSON(ctx, api.Call{Method: http.MethodPatch, Path: "/v0/persistent-servers/" + api.PathEscape(in.ID) + "/config",
				Body: map[string]any{"values": in.Values, "live": in.Live}, IdempotencyKey: api.NewIdempotencyKey()}, &out)
			if err != nil {
				return nil, err
			}
			return textResult("Saved.", pick(out, "not_applied", "results", "settings")), nil
		})

	tool(s, server, "list_players", "List players", "Who is on a running persistent server or game server now, asked from the game itself. What shows depends on what the game shares: a name or ID at least, and team, ping or a position in the game's units when it has them. Nothing is stored.", reads, nil,
		func(ctx context.Context, _ *mcp.CallToolRequest, in sendCommandIn) (*mcp.CallToolResult, error) {
			path, err := serverPath(in.ID)
			if err != nil {
				return nil, err
			}
			target, err := s.client.Get(ctx, path, nil)
			if err != nil {
				return nil, err
			}
			if err := s.objectAllowed(ctx, target, "server"); err != nil {
				return nil, err
			}
			r, err := s.client.Get(ctx, path+"/players", nil)
			if err != nil {
				return nil, err
			}
			if e := str(r["error"]); e != "" {
				return nil, errors.New(e)
			}
			// Players pick their own names.
			return plainResult(fmt.Sprintf("%d players.\n\n%s", len(list(r["players"])), gameJSON("The players, as the game shared them", r["players"]))), nil
		})

	tool(s, server, "plan_bulk_action", "Plan a bulk action", "Works out a command, restart or rollout on many persistent servers or game servers without doing anything: who it reaches, who it skips and why. Show the plan to the developer, then call run_bulk_action with its plan_id.", reads,
		map[string][]string{"action": {"command", "restart", "rollout"}, "apply": {"next_restart", "now"}, "selector.target": {"persistent_servers", "game_servers"}},
		func(ctx context.Context, _ *mcp.CallToolRequest, in planBulkIn) (*mcp.CallToolResult, error) {
			if bulkPaths[in.Action] == "" {
				return nil, errors.New("action is command, restart or rollout")
			}
			if s.limited() && in.Selector.App == "" {
				return nil, s.needApp("selector.app")
			}
			if in.Selector.App != "" {
				if err := s.allowedApp(ctx, in.Selector.App); err != nil {
					return nil, err
				}
			}
			body := in.body()
			plan, err := s.dryRun(ctx, in.Action, body)
			if err != nil {
				return nil, err
			}
			if err := s.targetsAllowed(ctx, plan); err != nil {
				return nil, err
			}
			id, err := json.Marshal(bulkPlan{Action: in.Action, Body: body, Targets: targetIDs(plan)})
			if err != nil {
				return nil, err
			}
			return textResult(planText(plan)+"\n\nplan_id: "+base64.RawURLEncoding.EncodeToString(id), pick(plan, "total", "players", "skipped", "batches")), nil
		})

	tool(s, server, "run_bulk_action", "Run a bulk action", "Runs a plan from plan_bulk_action, in batches. It is refused if the plan no longer reaches the same servers: plan again and show the new one.", removes, nil,
		func(ctx context.Context, _ *mcp.CallToolRequest, in runBulkIn) (*mcp.CallToolResult, error) {
			raw, err := base64.RawURLEncoding.DecodeString(in.PlanID)
			var p bulkPlan
			if err == nil {
				err = json.Unmarshal(raw, &p)
			}
			if err != nil || bulkPaths[p.Action] == "" {
				return nil, errors.New("that is not a plan_id from plan_bulk_action")
			}
			if s.limited() {
				app := str(obj(p.Body["selector"])["app"])
				if app == "" {
					return nil, s.needApp("selector.app")
				}
				if err := s.allowedApp(ctx, app); err != nil {
					return nil, err
				}
			}
			plan, err := s.dryRun(ctx, p.Action, p.Body)
			if err != nil {
				return nil, err
			}
			if err := s.targetsAllowed(ctx, plan); err != nil {
				return nil, err
			}
			if targetIDs(plan) != p.Targets {
				return nil, fmt.Errorf("the servers it reaches changed since the plan; call plan_bulk_action again. Now: %s", planText(plan))
			}
			var out map[string]any
			if err := s.client.JSON(withApproval(ctx, in.Approval), api.Call{Method: http.MethodPost, Path: bulkPaths[p.Action], Body: p.Body, IdempotencyKey: api.NewIdempotencyKey()}, &out); err != nil {
				return nil, err
			}
			op := obj(out["bulk_action"])
			return plainResult(fmt.Sprintf("Bulk action %s is %s. get_bulk_action shows how it went.", str(op["id"]), str(op["status"]))), nil
		})

	tool(s, server, "get_bulk_action", "Get a bulk action", "How a bulk action is going: its state and counts, why it stopped if it did, and each different reply with how many servers gave it.", reads, nil,
		func(ctx context.Context, _ *mcp.CallToolRequest, in bulkActionIn) (*mcp.CallToolResult, error) {
			op, err := s.bulkActionAllowed(ctx, in.ID, true)
			if err != nil {
				return nil, err
			}
			view := pick(op, "id", "type", "status", "counts", "stop_reason", "note", "selector")
			res := textResult("Bulk action "+str(op["status"])+".", view)
			if str(op["type"]) == "command" {
				replies, err := s.client.Get(ctx, "/v0/bulk-actions/"+api.PathEscape(in.ID)+"/replies", nil)
				if err != nil {
					return nil, err
				}
				// What the game answered on each server.
				res.Content[0].(*mcp.TextContent).Text += "\n\n" + gameJSON("The replies, each with how many servers gave it", replies["data"])
			}
			return res, nil
		})

	tool(s, server, "cancel_bulk_action", "Cancel a bulk action", "Stops a bulk action before its next batch. What already ran stays done.", removes, nil,
		func(ctx context.Context, _ *mcp.CallToolRequest, in bulkActionIn) (*mcp.CallToolResult, error) {
			if _, err := s.bulkActionAllowed(ctx, in.ID, false); err != nil {
				return nil, err
			}
			var op map[string]any
			if err := s.client.JSON(ctx, api.Call{Method: http.MethodPost, Path: "/v0/bulk-actions/" + api.PathEscape(in.ID) + "/cancel", IdempotencyKey: api.NewIdempotencyKey()}, &op); err != nil {
				return nil, err
			}
			return plainResult("Bulk action " + str(op["status"]) + "."), nil
		})
}
