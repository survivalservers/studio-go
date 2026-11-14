package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ---- commands to one server ----

// serverPath is where a persistent server's or game server's calls live, told apart by its ID.
func serverPath(id string) (string, error) {
	switch {
	case strings.HasPrefix(id, "ps_"):
		return "/v0/persistent-servers/" + esc(id), nil
	case strings.HasPrefix(id, "gs_"):
		return "/v0/game-servers/" + esc(id), nil
	}
	return "", usagef("%q is not a persistent server or game server ID", id)
}

// commandBody is what to send: a game command with its arguments, or a line through a connection.
func commandBody(saved []string, line, via string) (map[string]any, error) {
	body := map[string]any{}
	if line != "" {
		if len(saved) > 0 {
			return nil, usagef("give a game command or --line, not both")
		}
		body["line"] = line
		if via != "" {
			body["via"] = via
		}
		return body, nil
	}
	if via != "" {
		return nil, usagef("--via goes with --line; a game command names its own connection")
	}
	if len(saved) == 0 {
		return nil, usagef("give a game command or --line")
	}
	body["command_id"] = saved[0]
	args, err := pairs(saved[1:], "an argument")
	if err != nil {
		return nil, err
	}
	if args != nil {
		body["args"] = args
	}
	return body, nil
}

func cmdCommand(ctx context.Context, c *ctxt, args []string) error {
	line := c.fs.String("line", "", "send this line as it is, instead of a game command")
	via := c.fs.String("via", "", "with --line: a connection in the game profile (default console)")
	capture := c.fs.String("capture", "", "console only: how long to collect its output, up to 30s (default 2s)")
	pos, err := c.parse(args, `SERVER COMMAND [name=value]... | SERVER --line "status" [--via rcon]`, 1, -1)
	if err != nil {
		return err
	}
	path, err := serverPath(pos[0])
	if err != nil {
		return err
	}
	body, err := commandBody(pos[1:], *line, *via)
	if err != nil {
		return err
	}
	if *capture != "" {
		body["capture"] = *capture
	}
	r, err := c.call(ctx, http.MethodPost, path+"/command", nil, body)
	if err != nil {
		return err
	}
	if d, _ := r["delivered"].(bool); !d {
		if c.json {
			_ = c.printJSON(r)
		}
		return failedError{"not delivered: " + firstOf(str(r["error"]), "the game did not take it")}
	}
	return c.show(r, strings.TrimRight(firstOf(str(r["output"]), "Delivered; the game answered nothing."), "\r\n"))
}

// ---- game profiles ----

func cmdProfileGet(ctx context.Context, c *ctxt, args []string) error {
	pos, err := c.parse(args, "APP VERSION", 2, 2)
	if err != nil {
		return err
	}
	r, err := c.get(ctx, "/v0/apps/"+esc(pos[0])+"/versions/"+esc(pos[1])+"/profile", nil)
	if err != nil {
		return err
	}
	if c.json {
		return c.printJSON(r)
	}
	return c.printJSON(r["profile"]) // ready to edit and send back with profile set
}

func cmdProfileTemplates(ctx context.Context, c *ctxt, args []string) error {
	if _, err := c.parse(args, "", 0, 0); err != nil {
		return err
	}
	page, err := c.get(ctx, "/v0/profile-templates", nil)
	if err != nil {
		return err
	}
	var rows [][]string
	for _, t := range list(page["data"]) {
		rows = append(rows, []string{str(t["id"]), str(t["name"]), str(t["summary"])})
	}
	return c.table(page["data"], []string{"TEMPLATE", "GAME", "COVERS"}, rows)
}

func cmdProfileSet(ctx context.Context, c *ctxt, args []string) error {
	from := c.fs.String("from", "", "start from a ready profile (profile templates lists them) instead of a file")
	pos, err := c.parse(args, "APP VERSION game-profile.json | APP VERSION --from palworld", 2, 3)
	if err != nil {
		return err
	}
	if (*from != "") == (len(pos) == 3) {
		return usagef("give a file or --from, one of them")
	}
	var profile map[string]any
	if *from != "" {
		t, err := c.get(ctx, "/v0/profile-templates/"+esc(*from), nil)
		if err != nil {
			return err
		}
		profile = obj(t["profile"])
		for _, s := range anyList(t["setup"]) {
			fmt.Fprintln(c.env.Stderr, "Setup: "+str(s))
		}
		for _, s := range anyList(t["not_proven"]) {
			fmt.Fprintln(c.env.Stderr, "Not proven: "+str(s))
		}
	} else if profile, err = readJSONFile(pos[2]); err != nil {
		return err
	}
	r, err := c.call(ctx, http.MethodPut, "/v0/apps/"+esc(pos[0])+"/versions/"+esc(pos[1])+"/profile", nil, profile)
	if err != nil {
		return err
	}
	return c.show(r, fmt.Sprintf("Game profile saved: %s %s, revision %s", pos[0], pos[1], str(r["revision"])))
}

// ---- a persistent server's settings ----

func configRows(r map[string]any) [][]string {
	var rows [][]string
	for _, st := range list(r["settings"]) {
		pending := ""
		if st["pending"] == true {
			pending = "at the next restart"
		}
		if st["not_applied"] == true {
			pending = "not applied"
		}
		rows = append(rows, []string{str(st["id"]), str(st["value"]), str(st["source"]), pending})
	}
	return rows
}

func cmdConfigGet(ctx context.Context, c *ctxt, args []string) error {
	pos, err := c.parse(args, "SERVER", 1, 1)
	if err != nil {
		return err
	}
	r, err := c.get(ctx, "/v0/persistent-servers/"+esc(pos[0])+"/config", nil)
	if err != nil {
		return err
	}
	return c.showConfig(r)
}

// settingValue sends a value as its setting's type in the game profile: a text or choice setting takes what was
// typed as it is, so a password of 12345 or a name of true stays text (they once went
// out as a number and a yes/no, and the API refused them). With no type to go on (the profile could not be read, or
// the setting is not in it), a value is read as JSON when it is a number, true/false or a quoted "text", and as text
// otherwise.
func settingValue(s, typ string) any {
	switch typ {
	case "text", "choice":
		return s
	case "bool":
		if b, err := strconv.ParseBool(strings.TrimSpace(s)); err == nil {
			return b
		}
		return s // the API says it takes true or false
	case "int", "float":
		if n, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
			return n
		}
		return s
	}
	var v any
	if err := json.Unmarshal([]byte(s), &v); err == nil {
		switch v.(type) {
		case float64, bool, string:
			return v
		}
	}
	return s
}

// settingTypes reads each setting's type from the persistent server's game profile, or nil when it cannot.
func (c *ctxt) settingTypes(ctx context.Context, id string) map[string]string {
	p, err := c.get(ctx, "/v0/persistent-servers/"+esc(id), nil)
	if err != nil || str(p["app"]) == "" || str(p["version"]) == "" {
		return nil
	}
	r, err := c.get(ctx, "/v0/apps/"+esc(str(p["app"]))+"/versions/"+esc(str(p["version"]))+"/profile", nil)
	if err != nil {
		return nil
	}
	types := map[string]string{}
	for _, st := range list(obj(obj(r["profile"])["config"])["settings"]) {
		types[str(st["id"])] = str(st["type"])
	}
	return types
}

func cmdConfigSet(ctx context.Context, c *ctxt, args []string) error {
	live := c.fs.Bool("live", false, "also change what can change in the running game")
	var unset multi
	c.fs.Var(&unset, "unset", "take this setting's value off (repeatable)")
	pos, err := c.parse(args, "SERVER name=value... [--unset name]... [--live]", 1, -1)
	if err != nil {
		return err
	}
	pairsIn, err := pairs(pos[1:], "a setting")
	if err != nil {
		return err
	}
	values := map[string]any{}
	var types map[string]string
	if len(pairsIn) > 0 {
		types = c.settingTypes(ctx, pos[0])
	}
	for k, v := range pairsIn {
		values[k] = settingValue(v, types[k])
	}
	for _, k := range unset {
		values[k] = nil
	}
	if len(values) == 0 {
		return usagef("give at least one name=value or --unset")
	}
	r, err := c.call(ctx, http.MethodPatch, "/v0/persistent-servers/"+esc(pos[0])+"/config", nil, map[string]any{"values": values, "live": *live})
	if err != nil {
		return err
	}
	if c.json {
		return c.printJSON(r)
	}
	for _, x := range list(r["results"]) {
		if x["delivered"] == true {
			fmt.Fprintf(c.env.Stdout, "%s changed now\n", str(x["id"]))
		} else {
			fmt.Fprintf(c.env.Stdout, "%s did not change in the running game: %s\n", str(x["id"]), str(x["error"]))
		}
	}
	return c.showConfig(r)
}

func (c *ctxt) showConfig(r map[string]any) error {
	if s := str(r["not_applied"]); s != "" && !c.json {
		fmt.Fprintln(c.env.Stderr, s)
	}
	return c.table(r, []string{"SETTING", "VALUE", "FROM", "WAITING"}, configRows(r))
}

// ---- players ----

func cmdPlayers(ctx context.Context, c *ctxt, args []string) error {
	pos, err := c.parse(args, "SERVER", 1, 1)
	if err != nil {
		return err
	}
	path, err := serverPath(pos[0])
	if err != nil {
		return err
	}
	r, err := c.get(ctx, path+"/players", nil)
	if err != nil {
		return err
	}
	if e := str(r["error"]); e != "" {
		if c.json {
			_ = c.printJSON(r)
		}
		return failedError{e}
	}
	var rows [][]string
	for _, p := range list(r["players"]) {
		rows = append(rows, []string{str(p["name"]), str(p["id"]), str(p["team"]), str(p["ping_ms"])})
	}
	return c.table(r, []string{"PLAYER", "ID", "TEAM", "PING"}, rows)
}

// ---- bulk actions ----

// selectorFlags are the filters a bulk action picks its targets with.
type selectorFlags struct {
	target, app, branch                *string
	versions, regions, continents, ids multi
	labels, statuses                   multi
	empty                              *bool
}

func (c *ctxt) selectorFlags() *selectorFlags {
	s := &selectorFlags{target: c.fs.String("target", "persistent_servers", "persistent_servers or game_servers"),
		app: c.fs.String("app", "", "only this app"), branch: c.fs.String("branch", "", "only those on this branch")}
	c.fs.Var(&s.versions, "version", "only this version (repeatable)")
	c.fs.Var(&s.regions, "region", "only this region (repeatable)")
	c.fs.Var(&s.continents, "continent", "only this continent (repeatable)")
	c.fs.Var(&s.labels, "label", "only those with this label, key:value (repeatable)")
	c.fs.Var(&s.statuses, "status", "only this state (repeatable)")
	c.fs.Var(&s.ids, "id", "only this persistent server or game server (repeatable)")
	s.empty = c.fs.Bool("empty", false, "only those with no players on them")
	return s
}

func (s *selectorFlags) body() (map[string]any, error) {
	out := map[string]any{"target": *s.target}
	if *s.app != "" {
		out["app"] = *s.app
	}
	if *s.branch != "" {
		out["branch"] = *s.branch
	}
	for k, v := range map[string]multi{"versions": s.versions, "regions": s.regions, "continents": s.continents, "ids": s.ids, "status": s.statuses} {
		if len(v) > 0 {
			out[k] = []string(v)
		}
	}
	if len(s.labels) > 0 {
		labels := map[string]string{}
		for _, kv := range s.labels {
			k, v, ok := strings.Cut(kv, ":")
			if !ok || k == "" {
				return nil, usagef("--label %q is not key:value", kv)
			}
			labels[k] = v
		}
		out["labels"] = labels
	}
	if len(out) == 1 {
		return nil, usagef("say which persistent servers or game servers: --app, --region, --label or --id")
	}
	if *s.empty {
		out["players"] = map[string]any{"max": 0}
	}
	return out, nil
}

// planLines is a plan in words.
func planLines(plan map[string]any) []string {
	lines := []string{fmt.Sprintf("Would reach %s in %d batches, %s players on them now", str(plan["total"]), len(list(plan["batches"])), str(plan["players"]))}
	for _, t := range list(plan["skipped"]) {
		lines = append(lines, fmt.Sprintf("Skips %s: %s", firstOf(str(t["name"]), str(t["id"])), firstOf(str(t["detail"]), str(t["reason"]))))
	}
	return lines
}

func cmdBulkCommand(ctx context.Context, c *ctxt, args []string) error {
	sel := c.selectorFlags()
	line := c.fs.String("line", "", "send this line as it is, instead of a game command")
	via := c.fs.String("via", "", "with --line: a connection in the game profile (default console)")
	dry := c.fs.Bool("dry-run", false, "show who it would reach and send nothing")
	batch := c.fs.Int("batch-size", 0, "how many at a time (default 50)")
	pause := c.fs.String("pause", "", "wait this long between batches, for example 30s")
	note := c.fs.String("note", "", "why, kept with the bulk action and in the audit log")
	wait := c.fs.Bool("wait", false, "wait until it ends, then print the replies")
	timeout := c.fs.Duration("timeout", 10*time.Minute, "how long --wait waits")
	pos, err := c.parse(args, `(--app A | --region R | --label k:v | --id ID)... COMMAND [name=value]... | ... --line "status" [--via rcon] [--dry-run] [--wait]`, 0, -1)
	if err != nil {
		return err
	}
	body, err := commandBody(pos, *line, *via)
	if err != nil {
		return err
	}
	if body["selector"], err = sel.body(); err != nil {
		return err
	}
	rollout := map[string]any{}
	if *batch > 0 {
		rollout["batch_size"] = *batch
	}
	if *pause != "" {
		rollout["pause"] = *pause
	}
	if len(rollout) > 0 {
		body["rollout"] = rollout
	}
	if *note != "" {
		body["note"] = *note
	}
	if *dry {
		body["dry_run"] = true
	}
	r, err := c.call(ctx, http.MethodPost, "/v0/bulk-actions/commands", nil, body)
	if err != nil {
		return err
	}
	if *dry {
		return c.show(r, planLines(obj(r["plan"]))...)
	}
	op := obj(r["bulk_action"])
	id := str(op["id"])
	if !*wait {
		return c.show(r, fmt.Sprintf("Bulk action %s: %s", id, str(op["status"])))
	}
	op, err = c.wait(ctx, waitOn{"bulk action " + id, "survivalservers bulk-actions watch " + id, c.showBulk}, *timeout, func(ctx context.Context) (map[string]any, error) {
		return c.get(ctx, "/v0/bulk-actions/"+esc(id), nil)
	}, bulkSettled)
	if err != nil {
		return err
	}
	if err := c.showBulk(op); err != nil {
		return err
	}
	if c.json {
		return nil
	}
	return c.showReplies(ctx, id)
}

func bulkSettled(op map[string]any) (bool, string) {
	s := str(op["status"])
	return s == "succeeded" || s == "failed" || s == "cancelled" || s == "stopped", s
}

func (c *ctxt) showBulk(op map[string]any) error {
	n := obj(op["counts"])
	return c.show(op, fmt.Sprintf("Bulk action %s: %s. %s of %s done, %s failed, %s skipped", str(op["id"]), str(op["status"]), str(n["succeeded"]),
		str(n["total"]), str(n["failed"]), str(n["skipped"])), str(op["stop_reason"]))
}

func (c *ctxt) showReplies(ctx context.Context, id string) error {
	page, err := c.get(ctx, "/v0/bulk-actions/"+esc(id)+"/replies", nil)
	if err != nil {
		return err
	}
	var rows [][]string
	for _, g := range list(page["data"]) {
		reply := strings.ReplaceAll(strings.TrimSpace(str(g["reply"])), "\n", " / ")
		rows = append(rows, []string{str(g["count"]), str(g["status"]), firstOf(reply, "(no answer)")})
	}
	return c.table(page["data"], []string{"COUNT", "RESULT", "REPLY"}, rows)
}

func cmdBulkGet(ctx context.Context, c *ctxt, args []string) error {
	pos, err := c.parse(args, "BULK_ACTION", 1, 1)
	if err != nil {
		return err
	}
	op, err := c.get(ctx, "/v0/bulk-actions/"+esc(pos[0]), nil)
	if err != nil {
		return err
	}
	return c.showBulk(op)
}

func cmdBulkList(ctx context.Context, c *ctxt, args []string) error {
	if _, err := c.parse(args, "", 0, 0); err != nil {
		return err
	}
	page, err := c.readAll(ctx, "/v0/bulk-actions", nil, 0, "")
	if err != nil {
		return err
	}
	var rows [][]string
	for _, op := range list(page["data"]) {
		n := obj(op["counts"])
		rows = append(rows, []string{str(op["id"]), str(op["type"]), str(op["status"]), str(n["succeeded"]) + "/" + str(n["total"]), str(op["created_at"])})
	}
	return c.table(page["data"], []string{"BULK ACTION", "TYPE", "STATUS", "DONE", "CREATED"}, rows)
}

func cmdBulkReplies(ctx context.Context, c *ctxt, args []string) error {
	pos, err := c.parse(args, "BULK_ACTION", 1, 1)
	if err != nil {
		return err
	}
	return c.showReplies(ctx, pos[0])
}

func cmdBulkCancel(ctx context.Context, c *ctxt, args []string) error {
	pos, err := c.parse(args, "BULK_ACTION", 1, 1)
	if err != nil {
		return err
	}
	op, err := c.call(ctx, http.MethodPost, "/v0/bulk-actions/"+esc(pos[0])+"/cancel", nil, nil)
	if err != nil {
		return err
	}
	return c.showBulk(op)
}

// ---- saved actions ----

func cmdSavedList(ctx context.Context, c *ctxt, args []string) error {
	if _, err := c.parse(args, "", 0, 0); err != nil {
		return err
	}
	page, err := c.get(ctx, "/v0/saved-actions", nil)
	if err != nil {
		return err
	}
	var rows [][]string
	for _, a := range list(page["data"]) {
		runs := "by hand"
		if sc := obj(a["schedule"]); sc != nil {
			runs = fmt.Sprintf("%s (%s)", str(sc["cron"]), str(sc["time_zone"]))
		}
		next := str(a["next_run_at"])
		if a["paused"] == true {
			next = "paused"
		}
		rows = append(rows, []string{str(a["id"]), str(a["name"]), str(a["action"]), runs, next, str(a["last_error"])})
	}
	return c.table(page["data"], []string{"SAVED ACTION", "NAME", "ACTION", "RUNS", "NEXT RUN", "LAST ERROR"}, rows)
}

func cmdSavedCreate(ctx context.Context, c *ctxt, args []string) error {
	cron := c.fs.String("cron", "", "run on this schedule, for example \"0 18 * * 5\"")
	zone := c.fs.String("time-zone", "", "the schedule's time zone (default UTC)")
	set := c.fs.String("config-set", "", "with the config action: the set")
	pos, err := c.parse(args, `NAME command|restart|rollout|config request.json [--cron "0 18 * * 5"] [--time-zone Z] [--config-set C]`, 3, 3)
	if err != nil {
		return err
	}
	request, err := readJSONFile(pos[2])
	if err != nil {
		return err
	}
	body := map[string]any{"name": pos[0], "action": pos[1], "request": request}
	if *set != "" {
		body["config_set"] = *set
	}
	if *cron != "" {
		sc := map[string]any{"cron": *cron}
		if *zone != "" {
			sc["time_zone"] = *zone
		}
		body["schedule"] = sc
	} else if *zone != "" {
		return usagef("--time-zone goes with --cron")
	}
	a, err := c.call(ctx, http.MethodPost, "/v0/saved-actions", nil, body)
	if err != nil {
		return err
	}
	return c.show(a, fmt.Sprintf("Saved action %s (%s)", str(a["name"]), str(a["id"])))
}

func cmdSavedRun(ctx context.Context, c *ctxt, args []string) error {
	dry := c.fs.Bool("dry-run", false, "show who it would reach and do nothing")
	pos, err := c.parse(args, "ID [--dry-run]", 1, 1)
	if err != nil {
		return err
	}
	r, err := c.call(ctx, http.MethodPost, "/v0/saved-actions/"+esc(pos[0])+"/run", nil, map[string]any{"dry_run": *dry})
	if err != nil {
		return err
	}
	if *dry {
		return c.show(r, planLines(obj(r["plan"]))...)
	}
	op := obj(r["bulk_action"])
	return c.show(r, fmt.Sprintf("Bulk action %s: %s", str(op["id"]), str(op["status"])))
}

func cmdSavedPause(paused bool) command {
	return func(ctx context.Context, c *ctxt, args []string) error {
		pos, err := c.parse(args, "ID", 1, 1)
		if err != nil {
			return err
		}
		a, err := c.call(ctx, http.MethodPatch, "/v0/saved-actions/"+esc(pos[0]), nil, map[string]any{"paused": paused})
		if err != nil {
			return err
		}
		return c.show(a, fmt.Sprintf("Saved action %s: paused %v", str(a["name"]), paused))
	}
}

func cmdSavedDelete(ctx context.Context, c *ctxt, args []string) error {
	pos, err := c.parse(args, "ID", 1, 1)
	if err != nil {
		return err
	}
	if _, err := c.call(ctx, http.MethodDelete, "/v0/saved-actions/"+esc(pos[0]), nil, nil); err != nil {
		return err
	}
	return c.show(map[string]any{"deleted": pos[0]}, "Deleted "+pos[0])
}
