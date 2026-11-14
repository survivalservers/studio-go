package cli

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	api "github.com/survivalservers/studio-go"
)

// ---- persistent servers ----

// backupKinds are the words for a backup's kind.
var backupKinds = map[string]string{"scheduled": "Scheduled", "stop": "At stop", "manual": "By hand", "recovered": "Recovered", "continuous": "Continuous",
	"before_restore": "Before restore", "before_update": "Before update", "imported": "Imported"}

func backupKind(b map[string]any) string { return firstOf(backupKinds[str(b["kind"])], str(b["kind"])) }

func cmdPersistentList(ctx context.Context, c *ctxt, args []string) error {
	app := c.fs.String("app", "", "only this app's persistent servers")
	status := c.fs.String("status", "", "only these states, comma-separated")
	if _, err := c.parse(args, "[--app A] [--status S,S]", 0, 0); err != nil {
		return err
	}
	q := map[string][]string{}
	if *app != "" {
		q["app"] = []string{*app}
	}
	if *status != "" {
		q["status"] = []string{*status}
	}
	page, err := c.readAll(ctx, "/v0/persistent-servers", q, 0, "")
	if err != nil {
		return err
	}
	var rows [][]string
	for _, p := range list(page["data"]) {
		backup := ""
		if b := obj(p["last_backup"]); b != nil {
			backup = str(b["created_at"])
		}
		rows = append(rows, []string{str(p["id"]), str(p["name"]), str(p["status"]), str(p["version"]), str(p["region"]), address(p), str(obj(p["players"])["count"]), backup})
	}
	return c.table(page["data"], []string{"PERSISTENT SERVER", "NAME", "STATUS", "VERSION", "REGION", "ADDRESS", "PLAYERS", "LAST BACKUP"}, rows)
}

func (c *ctxt) showPersistent(p map[string]any) error {
	lines := []string{fmt.Sprintf("Persistent server %s (%s): %s in %s, version %s", str(p["name"]), str(p["id"]), str(p["status"]), str(p["region"]), str(p["version"]))}
	if a := address(p); a != "" {
		lines = append(lines, "Address: "+a)
	}
	if s := str(p["paused_since"]); s != "" {
		lines = append(lines, "Paused since "+s+"; the first player resumes it")
	}
	if b := obj(p["last_backup"]); b != nil {
		lines = append(lines, fmt.Sprintf("Last backup: %s (%s, %s)", str(b["id"]), backupKind(b), str(b["created_at"])))
	}
	return c.show(p, lines...)
}

// settlePersistent waits for a persistent server to settle. again is "" for a get, or the get command after a
// create, start, stop or restart, which a second run would repeat.
func (c *ctxt) settlePersistent(ctx context.Context, id, again string, timeout time.Duration) (map[string]any, error) {
	p, err := c.wait(ctx, waitOn{"persistent server " + id, again, c.showPersistent}, timeout, func(ctx context.Context) (map[string]any, error) {
		return c.get(ctx, "/v0/persistent-servers/"+esc(id), nil)
	}, api.PersistentServerSettled)
	if err != nil {
		return nil, err
	}
	if err := c.showPersistent(p); err != nil {
		return nil, err
	}
	if str(p["status"]) == "failed" {
		gs := str(p["game_server"])
		return p, failedError{fmt.Sprintf("persistent server %s failed; survivalservers logs %s shows why", id, gs)}
	}
	return p, nil
}

func cmdPersistentGet(ctx context.Context, c *ctxt, args []string) error {
	wait := c.fs.Bool("wait", false, "wait until it settles (running, stopped, paused or failed)")
	timeout := c.fs.Duration("timeout", 15*time.Minute, "how long --wait waits")
	pos, err := c.parse(args, "ID [--wait]", 1, 1)
	if err != nil {
		return err
	}
	if *wait {
		_, err := c.settlePersistent(ctx, pos[0], "", *timeout)
		return err
	}
	p, err := c.get(ctx, "/v0/persistent-servers/"+esc(pos[0]), nil)
	if err != nil {
		return err
	}
	return c.showPersistent(p)
}

func cmdPersistentCreate(ctx context.Context, c *ctxt, args []string) error {
	wait := c.fs.Bool("wait", false, "wait until it is running")
	timeout := c.fs.Duration("timeout", 15*time.Minute, "how long --wait waits")
	pos, err := c.parse(args, "SETTINGS.json [--wait]", 1, 1)
	if err != nil {
		return err
	}
	body, err := readJSONFile(pos[0])
	if err != nil {
		return err
	}
	p, err := c.call(ctx, http.MethodPost, "/v0/persistent-servers", nil, body)
	if err != nil {
		return err
	}
	if *wait && body["start"] != false {
		id := str(p["id"])
		_, err := c.settlePersistent(ctx, id, "survivalservers persistent-servers get "+id+" --wait", *timeout)
		return err
	}
	return c.showPersistent(p)
}

// cmdPersistentAction is start, stop or restart.
func cmdPersistentAction(action string) command {
	return func(ctx context.Context, c *ctxt, args []string) error {
		grace := ""
		if action != "start" {
			c.fs.StringVar(&grace, "shutdown-time", "", "time to save and exit before it is ended, for example 60s")
		}
		wait := c.fs.Bool("wait", false, "wait until it settles")
		timeout := c.fs.Duration("timeout", 15*time.Minute, "how long --wait waits")
		pos, err := c.parse(args, "ID [--wait]", 1, 1)
		if err != nil {
			return err
		}
		var body any
		if grace != "" {
			body = map[string]any{"shutdown_time": grace}
		}
		p, err := c.call(ctx, http.MethodPost, "/v0/persistent-servers/"+esc(pos[0])+"/"+action, nil, body)
		if err != nil {
			return err
		}
		if *wait {
			_, err := c.settlePersistent(ctx, pos[0], "survivalservers persistent-servers get "+pos[0]+" --wait", *timeout)
			return err
		}
		return c.showPersistent(p)
	}
}

func cmdPersistentUpdate(ctx context.Context, c *ctxt, args []string) error {
	version := c.fs.String("version", "", "run this version from now on")
	branch := c.fs.String("branch", "", "follow this branch from now on")
	apply := c.fs.String("apply", "", "next_restart (default) or now")
	file := c.fs.String("file", "", "a JSON file with any other changes (env, args, labels, size, backup, restart, when_empty)")
	pos, err := c.parse(args, "ID [--version V | --branch B] [--apply now] [--file CHANGES.json]", 1, 1)
	if err != nil {
		return err
	}
	body := map[string]any{}
	if *file != "" {
		if body, err = readJSONFile(*file); err != nil {
			return err
		}
	}
	for k, v := range map[string]string{"version": *version, "branch": *branch, "apply": *apply} {
		if v != "" {
			body[k] = v
		}
	}
	if len(body) == 0 || (len(body) == 1 && body["apply"] != nil) {
		return usagef("nothing to change")
	}
	p, err := c.call(ctx, http.MethodPatch, "/v0/persistent-servers/"+esc(pos[0]), nil, body)
	if err != nil {
		return err
	}
	return c.showPersistent(p)
}

func cmdPersistentBackup(ctx context.Context, c *ctxt, args []string) error {
	pos, err := c.parse(args, "ID (it must be running)", 1, 1)
	if err != nil {
		return err
	}
	b, err := c.call(ctx, http.MethodPost, "/v0/persistent-servers/"+esc(pos[0])+"/backups", nil, nil)
	if err != nil {
		return err
	}
	return c.show(b, fmt.Sprintf("Backup %s of %s: %s", str(b["id"]), pos[0], str(b["status"])))
}

func cmdPersistentBackups(ctx context.Context, c *ctxt, args []string) error {
	pos, err := c.parse(args, "ID", 1, 1)
	if err != nil {
		return err
	}
	page, err := c.readAll(ctx, "/v0/persistent-servers/"+esc(pos[0])+"/backups", nil, 0, "")
	if err != nil {
		return err
	}
	var rows [][]string
	continuous := false
	for _, b := range list(page["data"]) {
		continuous = continuous || str(b["kind"]) == "continuous"
		rows = append(rows, []string{str(b["id"]), backupKind(b), str(b["status"]), sizeText(int64(num(b["bytes"]))), str(b["created_at"]), str(b["expires_at"])})
	}
	if err := c.table(page["data"], []string{"BACKUP", "KIND", "STATUS", "SIZE", "CREATED", "EXPIRES"}, rows); err != nil || c.json || !continuous {
		return err
	}
	fmt.Fprintln(c.env.Stdout, "\nContinuous: copied within seconds of each game save; the newest 3 are kept, apart from your other backups.")
	return nil
}

// ---- pools (ready servers) ----

func cmdPoolsList(ctx context.Context, c *ctxt, args []string) error {
	pos, err := c.parse(args, "APP", 1, 1)
	if err != nil {
		return err
	}
	page, err := c.readAll(ctx, "/v0/apps/"+esc(pos[0])+"/pools", nil, 0, "")
	if err != nil {
		return err
	}
	var rows [][]string
	for _, p := range list(page["data"]) {
		st, size := obj(p["status"]), obj(p["size"])
		rows = append(rows, []string{str(p["region"]), firstOf(str(p["branch"]), str(p["version"])), str(size["cpu"]) + " CPU / " + str(size["memory_gb"]) + " GB",
			str(p["mode"]), str(p["min"]) + "-" + firstOf(str(p["max"]), str(p["min"])), str(st["ready"]) + "/" + str(st["target"])})
	}
	return c.table(page["data"], []string{"REGION", "RUNS", "SIZE", "MODE", "MIN-MAX", "READY/TARGET"}, rows)
}

func cmdPoolsSet(ctx context.Context, c *ctxt, args []string) error {
	version := c.fs.String("version", "", "the version they run")
	branch := c.fs.String("branch", "", "follow this branch's version")
	cpu := c.fs.Float64("cpu", 0, "CPUs per ready server")
	mem := c.fs.Float64("memory-gb", 0, "memory in GB per ready server")
	mode := c.fs.String("mode", "fixed", "fixed (always min ready) or auto (grows with recent starts up to max)")
	minN := c.fs.Int("min", 1, "ready servers kept waiting for players")
	maxN := c.fs.Int("max", 0, "auto: the most it grows to")
	pos, err := c.parse(args, "APP REGION (--version V | --branch B) --cpu N --memory-gb N [--mode auto] --min N [--max N]", 2, 2)
	if err != nil {
		return err
	}
	if (*version == "") == (*branch == "") || *cpu <= 0 || *mem <= 0 {
		return usagef("give --version or --branch, and --cpu and --memory-gb")
	}
	body := map[string]any{"size": map[string]any{"cpu": *cpu, "memory_gb": *mem}, "mode": *mode, "min": *minN}
	if *version != "" {
		body["version"] = *version
	} else {
		body["branch"] = *branch
	}
	if *maxN > 0 {
		body["max"] = *maxN
	}
	p, err := c.call(ctx, http.MethodPut, "/v0/apps/"+esc(pos[0])+"/pools/"+esc(pos[1]), nil, body)
	if err != nil {
		return err
	}
	st := obj(p["status"])
	return c.show(p, fmt.Sprintf("Ready servers for %s in %s: target %s, %s ready now. Ready servers are billed while they wait; that price is not final.", pos[0], pos[1], str(st["target"]), str(st["ready"])))
}

func cmdPoolsRemove(ctx context.Context, c *ctxt, args []string) error {
	pos, err := c.parse(args, "APP REGION", 2, 2)
	if err != nil {
		return err
	}
	cl, err := c.api()
	if err != nil {
		return err
	}
	if _, err := cl.Do(ctx, api.Call{Method: http.MethodDelete, Path: "/v0/apps/" + esc(pos[0]) + "/pools/" + esc(pos[1])}); err != nil {
		return err
	}
	return c.show(map[string]any{"app": pos[0], "region": pos[1], "removed": true}, fmt.Sprintf("Ready servers for %s in %s removed; any already given to players keep running.", pos[0], pos[1]))
}

// ---- usage and prices ----

func cmdUsage(ctx context.Context, c *ctxt, args []string) error {
	now := time.Now().UTC()
	from := c.fs.String("from", time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC).Format("2006-01-02"), "start, a date or a time")
	to := c.fs.String("to", now.AddDate(0, 0, 1).Format("2006-01-02"), "end (not included), a date or a time")
	group := c.fs.String("group-by", "", "split the rows by app, region, rate_class or version (comma-separated)")
	gran := c.fs.String("granularity", "day", "hour, day or month")
	if _, err := c.parse(args, "[--from D] [--to D] [--group-by app,region] [--granularity day]", 0, 0); err != nil {
		return err
	}
	q := map[string][]string{"from": {*from}, "to": {*to}, "granularity": {*gran}}
	if *group != "" {
		q["group_by"] = []string{*group}
	}
	u, err := c.get(ctx, "/v0/usage", q)
	if err != nil {
		return err
	}
	if c.json {
		return c.printJSON(u)
	}
	t := obj(u["totals"])
	fmt.Fprintf(c.env.Stdout, "%s to %s: %s vCPU-hours, $%s (free vCPU-hours used %s, credits $%s).",
		*from, *to, str(t["cpu_hours"]), str(t["amount_usd"]), str(t["free_cpu_hours_used"]), str(t["credits_usd"]))
	// A projection only when there is one: the API gives the spend so far until a month is 3 days old, and a past
	// month's own amount (the panel's rule, usage/shared.tsx hasForecast).
	if money(t["projected_month_usd"]) > money(t["amount_usd"]) {
		fmt.Fprintf(c.env.Stdout, " Projected to the end of this month: $%s.", str(t["projected_month_usd"]))
	}
	fmt.Fprint(c.env.Stdout, "\n\n")
	headers := []string{"DATE"}
	groups := strings.Split(*group, ",")
	if *group == "" {
		groups = nil
	}
	for _, g := range groups {
		headers = append(headers, strings.ToUpper(g))
	}
	headers = append(headers, "VCPU-HOURS", "USD")
	var rows [][]string
	for _, r := range list(u["rows"]) {
		row := []string{str(r["date"])}
		for _, g := range groups {
			row = append(row, str(r[g]))
		}
		rows = append(rows, append(row, str(r["cpu_hours"]), str(r["amount_usd"])))
	}
	c.json = false
	return c.table(nil, headers, rows)
}

func cmdPrices(ctx context.Context, c *ctxt, args []string) error {
	if _, err := c.parse(args, "", 0, 0); err != nil {
		return err
	}
	p, err := c.get(ctx, "/v0/prices", nil)
	if err != nil {
		return err
	}
	if c.json {
		return c.printJSON(p)
	}
	free := []string{str(p["free_cpu_hours_per_month"]) + " free vCPU-hours a month"}
	if m := str(p["paused_free_minutes"]); m != "" {
		free = append(free, "the first "+m+" minutes of each pause")
	}
	if gb := str(p["free_backup_gb_months"]); gb != "" {
		free = append(free, gb+" GB of backup storage a month")
	}
	fmt.Fprintf(c.env.Stdout, "Prices in %s, locked until %s. Free: %s. Bandwidth %s.\n\n",
		str(p["currency"]), str(p["locked_until"]), strings.Join(free, ", "), str(p["bandwidth"]))
	var rows [][]string
	for _, r := range priceRates {
		if v := str(obj(p["rates"])[r.field]); v != "" {
			rows = append(rows, []string{r.what, "$" + v, r.per, r.field})
		}
	}
	return c.table(nil, []string{"RATE", "USD", "PER", "FIELD"}, rows)
}

// priceRates are the rates GET /v0/prices gives (the Prices schema in the API file), in the pricing page's words and
// order, each with its unit. warm_cpu_hour comes only when ready servers have a price of their own (otherwise they
// bill at the on-demand rate for their size); a rate the answer leaves out is not listed.
var priceRates = []struct{ field, what, per string }{
	{"on_demand_cpu_hour_2gb", "On demand, up to 2 GB per vCPU", "vCPU-hour"},
	{"on_demand_cpu_hour_4gb", "On demand, up to 4 GB per vCPU", "vCPU-hour"},
	{"persistent_server_cpu_hour", "Persistent server, running", "vCPU-hour"},
	{"paused_cpu_hour", "Persistent server, paused past the free minutes", "vCPU-hour"},
	{"reserved_cpu_hour", "Reserved capacity", "vCPU-hour"},
	{"warm_cpu_hour", "Ready servers waiting in a pool", "vCPU-hour"},
	{"own_machine_cpu_month", "Your own machines", "vCPU a month"},
	{"backup_gb_month", "Backup storage past the free GB", "GB a month"},
}

// money reads a Money value (a decimal string, "12.3456"); 0 when it is missing or not a number.
func money(v any) float64 {
	f, _ := strconv.ParseFloat(str(v), 64)
	return f
}
