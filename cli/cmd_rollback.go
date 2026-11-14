package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Restore and roll back: one server goes back to a backup or a time, or many go back to before a time. A rollback
// always shows its preview first and runs exactly that preview.

// when reads a moment as people type it: RFC 3339, "2026-10-30 14:00", "14:00" (today), with a time zone from the
// value itself ("2026-10-30 14:00 Europe/Berlin") or from tz, else this computer's.
func when(s, tz string, now time.Time) (time.Time, error) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	loc := time.Local
	if tz != "" {
		l, err := time.LoadLocation(tz)
		if err != nil {
			return time.Time{}, usagef("no time zone %q", tz)
		}
		loc = l
	}
	if i := strings.LastIndex(s, " "); i > 0 {
		if l, err := time.LoadLocation(s[i+1:]); err == nil && strings.Contains(s[i+1:], "/") {
			loc, s = l, s[:i]
		}
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02T15:04"} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, nil
		}
	}
	for _, layout := range []string{"15:04:05", "15:04"} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			y, m, d := now.In(loc).Date()
			return time.Date(y, m, d, t.Hour(), t.Minute(), t.Second(), 0, loc), nil
		}
	}
	return time.Time{}, usagef(`not a time: %q (try "2026-10-30 14:00 Europe/Berlin" or "14:00")`, s)
}

func cmdRestore(ctx context.Context, c *ctxt, args []string) error {
	at := c.fs.String("at", "", `go back to its newest backup from before this time, for example "14:00"`)
	tz := c.fs.String("tz", "", "the time zone of --at, for example Europe/Berlin")
	backup := c.fs.String("backup", "", "go back to this backup")
	version := c.fs.String("version", "keep", "keep, or then: also put back the version it ran then")
	noStart := c.fs.Bool("no-start", false, "leave it stopped; it restores at its next start")
	pos, err := c.parse(args, `ID (--backup BACKUP | --at "14:00") [--tz Z] [--version then] [--no-start]`, 1, 2)
	if err != nil {
		return err
	}
	if len(pos) == 2 && *backup == "" {
		*backup = pos[1]
	}
	if (*at == "") == (*backup == "") {
		return usagef("give --backup or --at")
	}
	body := map[string]any{}
	if *backup != "" {
		body["backup"] = *backup
	}
	if *version != "keep" {
		body["version"] = *version
	}
	if *at != "" {
		t, err := when(*at, *tz, time.Now())
		if err != nil {
			return err
		}
		body["at"] = t.UTC().Format(time.RFC3339)
	}
	if *noStart {
		body["start"] = false
	}
	res, err := c.call(ctx, http.MethodPost, "/v0/persistent-servers/"+esc(pos[0])+"/restore", nil, body)
	if err != nil {
		return err
	}
	op := obj(res["bulk_action"])
	return c.show(res, fmt.Sprintf("Restoring persistent server %s. It is backed up as it is now first, so you can undo: survivalservers bulk-actions undo %s", pos[0], str(op["id"])),
		"Watch it: survivalservers bulk-actions watch "+str(op["id"]))
}

func cmdRollback(ctx context.Context, c *ctxt, args []string) error {
	app := c.fs.String("app", "", "only this app's persistent servers")
	continents := c.fs.String("continent", "", "only these continents, comma separated")
	regions := c.fs.String("region", "", "only these regions, comma separated")
	versions := c.fs.String("on-version", "", "only persistent servers on these versions, comma separated (needs --app)")
	ids := c.fs.String("ids", "", "only these persistent servers, comma separated")
	var labels multi
	c.fs.Var(&labels, "label", "only persistent servers with this label, key=value (repeatable)")
	before := c.fs.String("before", "", `go back to before this time, for example "2026-10-30 14:00 Europe/Berlin"`)
	tz := c.fs.String("tz", "", "the time zone of --before")
	update := c.fs.String("before-update", "", "in place of --before, a rollout's ID (bulk_...): each persistent server goes back to its last save before that rollout's update")
	version := c.fs.String("version", "keep", "keep, or then: also put back the version each persistent server ran then")
	first := c.fs.Bool("try-one-first", false, "do one persistent server, then wait for survivalservers bulk-actions resume")
	warn := c.fs.String("warn", "", "warn players this long before, comma separated, for example 15m,5m,1m (needs --warn-command)")
	warnCmd := c.fs.String("warn-command", "", `the console line that tells players, for example 'say {{message}}'`)
	yes := c.fs.Bool("yes", false, "do not ask; run the preview it printed")
	if _, err := c.parse(args, `(--before TIME | --before-update ROLLOUT) [--app A] [--continent C] [--region R] [--label k=v]... [--version then] [--try-one-first] [--warn 15m,5m,1m --warn-command LINE] [--yes]`, 0, 0); err != nil {
		return err
	}
	if (*before == "") == (*update == "") {
		return usagef("give --before or --before-update")
	}
	sel := map[string]any{"target": "persistent_servers"}
	if *app != "" {
		sel["app"] = *app
	}
	for name, v := range map[string]string{"continents": *continents, "regions": *regions, "versions": *versions, "ids": *ids} {
		if v != "" {
			sel[name] = strings.Split(v, ",")
		}
	}
	if len(labels) > 0 {
		lab, err := pairs(labels, "--label")
		if err != nil {
			return err
		}
		sel["labels"] = lab
	}
	body := map[string]any{"selector": sel}
	if *update != "" {
		body["before_update"] = *update
	} else {
		at, err := when(*before, *tz, time.Now())
		if err != nil {
			return err
		}
		body["at"] = at.UTC().Format(time.RFC3339)
	}
	if *version != "keep" {
		body["version"] = *version
	}
	if *first {
		body["rollout"] = map[string]any{"first": map[string]any{"count": 1}}
	}
	if *warn != "" {
		var warnings []map[string]any
		for _, w := range strings.Split(*warn, ",") {
			warnings = append(warnings, map[string]any{"before": strings.TrimSpace(w), "message": "This server rolls back in " + strings.TrimSpace(w) + "."})
		}
		body["warnings"] = warnings
		body["warn_command"] = map[string]any{"type": "console", "line": *warnCmd}
	}
	res, err := c.call(ctx, http.MethodPost, "/v0/bulk-actions/rollbacks", nil, body)
	if err != nil {
		return err
	}
	plan := obj(res["plan"])
	if err := c.showRollbackPlan(plan); err != nil {
		return err
	}
	n := int(num(plan["total"]))
	if n == 0 {
		return errors.New("no persistent server would go back")
	}
	if !*yes {
		if c.json {
			return nil // the plan is printed; a script runs it with --yes
		}
		fmt.Fprintf(c.env.Stdout, "\nRoll back %d %s? [y/N] ", n, plural(n, "persistent server", "persistent servers"))
		line, _ := bufio.NewReader(c.env.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			fmt.Fprintln(c.env.Stdout, "Nothing was changed.")
			return nil
		}
	}
	res, err = c.call(ctx, http.MethodPost, "/v0/bulk-actions/rollbacks", nil, map[string]any{"plan": plan["id"]})
	if err != nil {
		return err
	}
	op := obj(res["bulk_action"])
	return c.show(res, "Rolling back: "+str(op["id"]), "Watch it: survivalservers bulk-actions watch "+str(op["id"]),
		"Put every persistent server back: survivalservers bulk-actions undo "+str(op["id"]))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func (c *ctxt) showRollbackPlan(plan map[string]any) error {
	if c.json {
		return c.printJSON(plan)
	}
	var rows [][]string
	for _, t := range list(plan["targets"]) {
		r := obj(t["restore"])
		rows = append(rows, []string{str(t["name"]), str(t["region"]), fmt.Sprint(int(num(t["players"]))), str(r["saved_at"]), str(r["play_lost"]), str(t["version"]), str(r["version"])})
	}
	fmt.Fprintf(c.env.Stdout, "%d %s, %d players online now.\n\n", int(num(plan["total"])), plural(int(num(plan["total"])), "persistent server", "persistent servers"), int(num(plan["players"])))
	if err := c.table(plan["targets"], []string{"PERSISTENT SERVER", "REGION", "PLAYERS", "BACK TO", "PLAY LOST", "VERSION NOW", "VERSION THEN"}, rows); err != nil {
		return err
	}
	skipped := list(plan["skipped"])
	if len(skipped) > 0 {
		fmt.Fprintln(c.env.Stdout, "\nLeft alone:")
		for _, t := range skipped {
			fmt.Fprintf(c.env.Stdout, "  %s: %s\n", str(t["name"]), str(t["detail"]))
		}
	}
	return nil
}

func cmdBulkWatch(ctx context.Context, c *ctxt, args []string) error {
	pos, err := c.parse(args, "ID", 1, 1)
	if err != nil {
		return err
	}
	last := ""
	op, err := c.wait(ctx, waitOn{"bulk action " + pos[0], "", nil}, 6*time.Hour, func(ctx context.Context) (map[string]any, error) {
		return c.get(ctx, "/v0/bulk-actions/"+esc(pos[0]), nil)
	}, func(o map[string]any) (bool, string) {
		cs := obj(o["counts"])
		line := fmt.Sprintf("%s: %d of %d done, %d failed", str(o["status"]), int(num(cs["succeeded"])), int(num(cs["total"])), int(num(cs["failed"])))
		if line != last && !c.json {
			fmt.Fprintln(c.env.Stdout, line)
			last = line
		}
		switch str(o["status"]) {
		case "succeeded", "failed", "cancelled", "stopped", "paused":
			return true, ""
		}
		return false, ""
	})
	if err != nil {
		return err
	}
	return c.show(op, str(op["stop_reason"]))
}

// cmdBulkFollow is undo, retry or resume of a bulk action.
func cmdBulkFollow(verb string) func(context.Context, *ctxt, []string) error {
	return func(ctx context.Context, c *ctxt, args []string) error {
		pos, err := c.parse(args, "ID", 1, 1)
		if err != nil {
			return err
		}
		res, err := c.call(ctx, http.MethodPost, "/v0/bulk-actions/"+esc(pos[0])+"/"+verb, nil, map[string]any{})
		if err != nil {
			return err
		}
		return c.show(res, followedLine(verb, pos[0], res), "Watch it: survivalservers bulk-actions watch "+followedID(pos[0], res))
	}
}

// followedID is the bulk action that runs after an undo, retry or resume: undo and retry make a new one (their answer
// carries it as bulk_action), resume goes on with the same one (its answer is the bulk action itself).
func followedID(id string, res map[string]any) string {
	if op := obj(res["bulk_action"]); op != nil {
		return str(op["id"])
	}
	return id
}

// followedLine says what an undo, retry or resume did, with the bulk action that now runs and how many targets it has.
func followedLine(verb, id string, res map[string]any) string {
	op := obj(res["bulk_action"])
	if op == nil {
		op = res
	}
	total := int(num(obj(op["counts"])["total"]))
	what := plural(total, "persistent server", "persistent servers")
	if obj(op["selector"])["target"] == "game_servers" {
		what = plural(total, "game server", "game servers")
	}
	switch verb {
	case "undo":
		return fmt.Sprintf("Bulk action %s undoes %s on %d %s.", str(op["id"]), id, total, what)
	case "retry":
		return fmt.Sprintf("Bulk action %s retries %s on its %d failed %s.", str(op["id"]), id, total, what)
	}
	return fmt.Sprintf("Bulk action %s resumed: %d of %d %s still to do.", id, int(num(obj(op["counts"])["pending"])), total, what)
}
