package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	api "github.com/survivalservers/studio-go"
)

func cmdServersStart(ctx context.Context, c *ctxt, args []string) error {
	version := c.fs.String("version", "", "the version to run (default the live branch)")
	branch := c.fs.String("branch", "", "run whatever version this branch points at")
	mode := c.fs.String("mode", "", "auto (default), warm (a ready game server only) or cold (always a fresh start)")
	ttl := c.fs.String("ttl", "", "stop it after this long, for example 30m or 2h")
	external := c.fs.String("external-id", "", "your own ID for it, for example a match ID")
	wait := c.fs.Bool("wait", false, "wait until players can join (or it failed)")
	timeout := c.fs.Duration("timeout", 10*time.Minute, "how long --wait waits")
	var regions, continents, envs, argv, labels multi
	c.fs.Var(&regions, "region", "a preferred region, in order (repeatable)")
	c.fs.Var(&continents, "continent", "any region in this continent, after --region (repeatable)")
	c.fs.Var(&envs, "env", "NAME=value for this game server (repeatable)")
	c.fs.Var(&argv, "arg", "an extra launch argument (repeatable)")
	c.fs.Var(&labels, "label", "name=value label to find it by (repeatable)")
	pos, err := c.parse(args, "APP [--version V | --branch B] [--region R]... [--continent C]... [--env K=V]... [--label k=v]... [--ttl 2h] [--wait]", 1, 1)
	if err != nil {
		return err
	}
	if *version != "" && *branch != "" {
		return usagef("give --version or --branch, not both")
	}
	body := map[string]any{"app": pos[0]}
	for k, v := range map[string]string{"version": *version, "branch": *branch, "start_mode": *mode, "ttl": *ttl, "external_id": *external} {
		if v != "" {
			body[k] = v
		}
	}
	if len(regions) > 0 || len(continents) > 0 {
		p := map[string]any{}
		if len(regions) > 0 {
			p["regions"] = []string(regions)
		}
		if len(continents) > 0 {
			p["continents"] = []string(continents)
		}
		body["placement"] = p
	}
	env, err := pairs(envs, "--env")
	if err != nil {
		return err
	}
	if env != nil {
		body["env"] = env
	}
	lab, err := pairs(labels, "--label")
	if err != nil {
		return err
	}
	if lab != nil {
		body["labels"] = lab
	}
	if len(argv) > 0 {
		body["args"] = []string(argv)
	}
	gs, err := c.call(ctx, http.MethodPost, "/v0/game-servers", nil, body)
	if err != nil {
		return err
	}
	if id := str(gs["id"]); *wait && str(gs["status"]) == "starting" {
		gs, err = c.wait(ctx, waitOn{"game server " + id, "survivalservers game-servers get " + id + " --wait", c.showServer}, *timeout, func(ctx context.Context) (map[string]any, error) {
			return c.get(ctx, "/v0/game-servers/"+esc(id), nil)
		}, api.GameServerSettled)
		if err != nil {
			return err
		}
	}
	if err := c.showServer(gs); err != nil {
		return err
	}
	if st := str(gs["status"]); st == "failed" || st == "stopped" {
		return failedError{fmt.Sprintf("game server %s %s; survivalservers logs %s shows why", str(gs["id"]), st, str(gs["id"]))}
	}
	return nil
}

func (c *ctxt) showServer(gs map[string]any) error {
	id, st := str(gs["id"]), str(gs["status"])
	lines := []string{fmt.Sprintf("Game server %s: %s in %s (%s %s, version %s)", id, st, str(gs["region"]), str(gs["start_mode"]), str(gs["os"]), str(gs["version"]))}
	if a := address(gs); a != "" {
		lines = append(lines, "Address: "+a)
	}
	if p := obj(gs["players"]); p != nil {
		lines = append(lines, "Players: "+str(p["count"]))
	}
	if e := str(gs["expires_at"]); e != "" {
		lines = append(lines, "Stops on its own at "+e)
	}
	if ex := obj(gs["exit"]); ex != nil {
		lines = append(lines, fmt.Sprintf("Exit: %s, code %s, crashed %v", str(ex["reason"]), str(ex["code"]), ex["crashed"]))
	}
	return c.show(gs, lines...)
}

func cmdServersList(ctx context.Context, c *ctxt, args []string) error {
	app := c.fs.String("app", "", "only this app's game servers")
	version := c.fs.String("version", "", "only this version")
	region := c.fs.String("region", "", "only this region")
	status := c.fs.String("status", "starting,running", "only these states, comma-separated")
	limit := c.fs.Int("limit", 50, "at most this many, newest first (0: all)")
	var labels multi
	c.fs.Var(&labels, "label", "only game servers with this label, key:value (repeatable)")
	if _, err := c.parse(args, "[--app A] [--region R] [--status S,S] [--label k:v]...", 0, 0); err != nil {
		return err
	}
	q := map[string][]string{}
	for k, v := range map[string]string{"app": *app, "version": *version, "region": *region, "status": *status} {
		if v != "" {
			q[k] = []string{v}
		}
	}
	if len(labels) > 0 {
		q["label"] = labels
	}
	page, err := c.readAll(ctx, "/v0/game-servers", q, *limit, fmt.Sprintf("raise --limit (now %d), or 0 for all", *limit))
	if err != nil {
		return err
	}
	var rows [][]string
	for _, g := range list(page["data"]) {
		rows = append(rows, []string{str(g["id"]), str(g["status"]), str(g["version"]), str(g["region"]), address(g), str(obj(g["players"])["count"]), str(g["created_at"])})
	}
	return c.table(page["data"], []string{"GAME SERVER", "STATUS", "VERSION", "REGION", "ADDRESS", "PLAYERS", "CREATED"}, rows)
}

func cmdServersGet(ctx context.Context, c *ctxt, args []string) error {
	wait := c.fs.Bool("wait", false, "wait until it is ready for players (or has ended)")
	timeout := c.fs.Duration("timeout", 10*time.Minute, "how long --wait waits")
	pos, err := c.parse(args, "ID [--wait]", 1, 1)
	if err != nil {
		return err
	}
	get := func(ctx context.Context) (map[string]any, error) {
		return c.get(ctx, "/v0/game-servers/"+esc(pos[0]), nil)
	}
	var gs map[string]any
	if *wait {
		gs, err = c.wait(ctx, waitOn{"game server " + pos[0], "", c.showServer}, *timeout, get, api.GameServerSettled)
	} else {
		gs, err = get(ctx)
	}
	if err != nil {
		return err
	}
	return c.showServer(gs)
}

func cmdServersStop(ctx context.Context, c *ctxt, args []string) error {
	grace := c.fs.String("shutdown-time", "", "time to save and exit before it is ended, for example 30s (default the version's)")
	reason := c.fs.String("reason", "", "why, kept on its timeline")
	pos, err := c.parse(args, "ID [--shutdown-time 30s] [--reason R]", 1, 1)
	if err != nil {
		return err
	}
	body := map[string]any{}
	if *grace != "" {
		body["shutdown_time"] = *grace
	}
	if *reason != "" {
		body["reason"] = *reason
	}
	gs, err := c.call(ctx, http.MethodPost, "/v0/game-servers/"+esc(pos[0])+"/stop", nil, body)
	if err != nil {
		return err
	}
	return c.show(gs, fmt.Sprintf("Game server %s is %s.", pos[0], str(gs["status"])))
}

func cmdServersEvents(ctx context.Context, c *ctxt, args []string) error {
	pos, err := c.parse(args, "ID", 1, 1)
	if err != nil {
		return err
	}
	page, err := c.readAll(ctx, "/v0/game-servers/"+esc(pos[0])+"/events", nil, 0, "")
	if err != nil {
		return err
	}
	var rows [][]string
	for _, e := range list(page["data"]) {
		d := ""
		if data := obj(e["data"]); len(data) > 0 {
			d = jsonText(data)
		}
		rows = append(rows, []string{str(e["at"]), str(e["type"]), d})
	}
	return c.table(page["data"], []string{"AT", "EVENT", "DATA"}, rows)
}

// ---- logs and files ----

func cmdLogs(ctx context.Context, c *ctxt, args []string) error {
	source := c.fs.String("source", "console", "console, stdout, stderr, or a log file's source name (survivalservers logs list ID)")
	tail := c.fs.Int("tail", 200, "start this many lines from the end (0: the whole log)")
	follow := c.fs.Bool("follow", false, "keep printing new lines while the game server runs")
	pos, err := c.parse(args, "ID [--source S] [--tail N] [--follow]", 1, 1)
	if err != nil {
		return err
	}
	cl, err := c.api()
	if err != nil {
		return err
	}
	q := map[string][]string{"source": {*source}}
	if *tail > 0 {
		q["tail"] = []string{api.Itoa(*tail)}
	}
	if *follow {
		q["follow"] = []string{"true"}
	}
	body, err := cl.Stream(ctx, api.Call{Method: http.MethodGet, Path: "/v0/game-servers/" + esc(pos[0]) + "/logs/stream", Query: q, Accept: "text/plain"})
	if err != nil {
		return err
	}
	defer body.Close()
	_, err = io.Copy(c.env.Stdout, body)
	if ctx.Err() != nil {
		return nil // stopped with Ctrl+C while following
	}
	return err
}

func cmdLogsList(ctx context.Context, c *ctxt, args []string) error {
	pos, err := c.parse(args, "ID", 1, 1)
	if err != nil {
		return err
	}
	logs, err := c.get(ctx, "/v0/game-servers/"+esc(pos[0])+"/logs", nil)
	if err != nil {
		return err
	}
	var rows [][]string
	for _, s := range list(logs["sources"]) {
		// A running console has no size until its first part is sent (about every 15 minutes): it may already
		// have lines, so it never reads as 0 bytes.
		size := "not sent yet"
		if s["bytes"] != nil {
			size = sizeText(int64(num(s["bytes"])))
		}
		rows = append(rows, []string{str(s["name"]), size, fmt.Sprint(s["live"])})
	}
	return c.table(logs, []string{"SOURCE", "SIZE", "LIVE"}, rows)
}

func cmdFiles(ctx context.Context, c *ctxt, args []string) error {
	pos, err := c.parse(args, "ID [PATH]", 1, 2)
	if err != nil {
		return err
	}
	q := map[string][]string{}
	if len(pos) == 2 {
		q["path"] = []string{pos[1]}
	}
	page, err := c.readAll(ctx, "/v0/game-servers/"+esc(pos[0])+"/files", q, 0, "")
	if err != nil {
		return err
	}
	var rows [][]string
	for _, f := range list(page["data"]) {
		size := ""
		if f["bytes"] != nil {
			size = sizeText(int64(num(f["bytes"])))
		}
		rows = append(rows, []string{str(f["path"]), str(f["type"]), size, str(f["modified_at"])})
	}
	return c.table(page, []string{"PATH", "TYPE", "SIZE", "MODIFIED"}, rows)
}

func cmdFilesGet(ctx context.Context, c *ctxt, args []string) error {
	out := c.fs.String("o", "", "write it to this file (default: a file of the same name here; - for standard output)")
	pos, err := c.parse(args, "ID PATH [-o FILE]", 2, 2)
	if err != nil {
		return err
	}
	cl, err := c.api()
	if err != nil {
		return err
	}
	link, err := cl.DownloadLink(ctx, "/v0/game-servers/"+esc(pos[0])+"/files/download", map[string][]string{"path": {pos[1]}})
	if err != nil {
		return err
	}
	hc := c.env.Storage
	if hc == nil {
		hc = &http.Client{}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return err
	}
	resp, err := hc.Do(req) // the link carries its own permission: no API key goes with it
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("the download link answered %d", resp.StatusCode)
	}
	dest := firstOf(*out, filepath.Base(strings.ReplaceAll(pos[1], `\`, "/")))
	if dest == "-" {
		_, err = io.Copy(c.env.Stdout, resp.Body)
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".survivalservers-*")
	if err != nil {
		return err
	}
	n, err := io.Copy(tmp, resp.Body)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp.Name())
		return err
	}
	if err := os.Rename(tmp.Name(), dest); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	fmt.Fprintf(c.env.Stdout, "%s: %s\n", dest, sizeText(n))
	return nil
}

func num(v any) float64 {
	f, _ := v.(float64)
	return f
}
