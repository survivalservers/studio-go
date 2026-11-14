package cli

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	api "github.com/survivalservers/studio-go"
)

var commands = map[string]map[string]command{
	"account":      {"": cmdAccount},
	"regions":      {"": cmdRegions},
	"continents":   {"": cmdContinents},
	"apps":         {"list": cmdAppsList, "get": cmdAppsGet, "create": cmdAppsCreate, "icon": cmdPicture("icon"), "banner": cmdPicture("banner"), "color": cmdAppColor},
	"builds":       {"upload": cmdBuildsUpload, "image": cmdBuildsImage, "get": cmdBuildsGet, "list": cmdBuildsList, "delete": cmdBuildsDelete},
	"registry":     {"login": cmdRegistryLogin},
	"versions":     {"create": cmdVersionsCreate, "list": cmdVersionsList, "get": cmdVersionsGet, "delete": cmdVersionsDelete, "deprecate": cmdVersionsDeprecate},
	"branches":     {"set": cmdBranchesSet, "list": cmdBranchesList},
	"deploy":       {"": cmdDeploy},
	"game-servers": {"start": cmdServersStart, "list": cmdServersList, "get": cmdServersGet, "stop": cmdServersStop, "events": cmdServersEvents},
	"logs":         {"": cmdLogs, "list": cmdLogsList},
	"files":        {"": cmdFiles, "get": cmdFilesGet},
	"persistent-servers": {"list": cmdPersistentList, "get": cmdPersistentGet, "create": cmdPersistentCreate,
		"start": cmdPersistentAction("start"), "stop": cmdPersistentAction("stop"), "restart": cmdPersistentAction("restart"),
		"update": cmdPersistentUpdate, "backup": cmdPersistentBackup, "backups": cmdPersistentBackups, "restore": cmdRestore},
	"backups": {"list": cmdPersistentBackups, "take": cmdBackupsTake, "lock": cmdBackupsLock(true), "unlock": cmdBackupsLock(false), "delete": cmdBackupsDelete,
		"download": cmdBackupsDownload, "upload": cmdBackupsUpload},
	"restore":  {"": cmdRestore},
	"rollback": {"": cmdRollback},
	"bulk-actions": {"command": cmdBulkCommand, "get": cmdBulkGet, "list": cmdBulkList, "replies": cmdBulkReplies, "cancel": cmdBulkCancel,
		"watch": cmdBulkWatch, "undo": cmdBulkFollow("undo"), "retry": cmdBulkFollow("retry"), "resume": cmdBulkFollow("resume")},
	"pools":   {"list": cmdPoolsList, "set": cmdPoolsSet, "remove": cmdPoolsRemove},
	"usage":   {"": cmdUsage},
	"prices":  {"": cmdPrices},
	"studio":  {"rename": cmdStudioRename, "icon": cmdPicture("studio"), "accent": cmdStudioAccent},
	"ai":      {"setup": cmdAISetup},
	"command": {"": cmdCommand},
	"profile": {"get": cmdProfileGet, "set": cmdProfileSet, "templates": cmdProfileTemplates},
	"config":  {"get": cmdConfigGet, "set": cmdConfigSet},
	"players": {"": cmdPlayers},
	"saved":   {"list": cmdSavedList, "create": cmdSavedCreate, "run": cmdSavedRun, "pause": cmdSavedPause(true), "resume": cmdSavedPause(false), "delete": cmdSavedDelete},
}

func cmdAccount(ctx context.Context, c *ctxt, args []string) error {
	if _, err := c.parse(args, "", 0, 0); err != nil {
		return err
	}
	s, err := c.get(ctx, "/v0/studio", nil)
	if err != nil {
		return err
	}
	free, lim := obj(s["free_tier"]), obj(s["limits"])
	return c.show(s,
		fmt.Sprintf("Studio %s (%s), %s", str(s["name"]), str(s["slug"]), str(s["status"])),
		fmt.Sprintf("Free vCPU-hours this month: %s of %s used", str(free["cpu_hours_used"]), str(free["cpu_hours_per_month"])),
		fmt.Sprintf("Card on file: %v. Prices locked until %s", s["card_on_file"], str(s["price_locked_until"])),
		fmt.Sprintf("Limits: %s running CPUs, %s CPUs and %s GB per game server", str(lim["running_cpus"]), str(lim["cpus_per_game_server"]), str(lim["memory_gb_per_game_server"])))
}

func cmdRegions(ctx context.Context, c *ctxt, args []string) error {
	osName := c.fs.String("os", "", "only regions that offer windows or linux")
	tier := c.fs.String("tier", "", "only regions that offer standard or high-frequency")
	continent := c.fs.String("continent", "", "only regions in this continent, for example europe")
	if _, err := c.parse(args, "[--os windows|linux] [--tier T] [--continent C]", 0, 0); err != nil {
		return err
	}
	q := map[string][]string{}
	for k, v := range map[string]string{"os": *osName, "tier": *tier, "continent": *continent} {
		if v != "" {
			q[k] = []string{v}
		}
	}
	page, err := c.readAll(ctx, "/v0/regions", q, 0, "")
	if err != nil {
		return err
	}
	var rows [][]string
	for _, rg := range list(page["data"]) {
		var offers []string
		for _, o := range list(rg["offers"]) {
			var oses []string
			xs, _ := o["os"].([]any) // an offer without its os list is shown without one, not a crash
			for _, x := range xs {
				oses = append(oses, str(x))
			}
			offers = append(offers, str(o["tier"])+" ("+strings.Join(oses, ", ")+")")
		}
		rows = append(rows, []string{str(rg["id"]), str(rg["name"]), str(rg["continent"]), str(rg["status"]), strings.Join(offers, "; ")})
	}
	return c.table(page["data"], []string{"REGION", "NAME", "CONTINENT", "STATUS", "OFFERS"}, rows)
}

func cmdContinents(ctx context.Context, c *ctxt, args []string) error {
	if _, err := c.parse(args, "", 0, 0); err != nil {
		return err
	}
	page, err := c.readAll(ctx, "/v0/continents", nil, 0, "")
	if err != nil {
		return err
	}
	var rows [][]string
	for _, ct := range list(page["data"]) {
		var regions []string
		for _, r := range anyList(ct["regions"]) {
			regions = append(regions, str(r))
		}
		rows = append(rows, []string{str(ct["id"]), str(ct["name"]), strings.Join(regions, ", ")})
	}
	return c.table(page["data"], []string{"CONTINENT", "NAME", "REGIONS"}, rows)
}

// ---- apps ----

func cmdAppsList(ctx context.Context, c *ctxt, args []string) error {
	if _, err := c.parse(args, "", 0, 0); err != nil {
		return err
	}
	page, err := c.readAll(ctx, "/v0/apps", nil, 0, "")
	if err != nil {
		return err
	}
	var rows [][]string
	for _, a := range list(page["data"]) {
		var ch []string
		for name, v := range obj(a["branches"]) {
			ch = append(ch, name+"="+str(v))
		}
		rows = append(rows, []string{str(a["slug"]), str(a["name"]), str(a["os"]), str(a["tier"]), strings.Join(ch, " ")})
	}
	return c.table(page["data"], []string{"APP", "NAME", "OS", "TIER", "BRANCHES"}, rows)
}

func cmdAppsGet(ctx context.Context, c *ctxt, args []string) error {
	pos, err := c.parse(args, "APP", 1, 1)
	if err != nil {
		return err
	}
	a, err := c.get(ctx, "/v0/apps/"+esc(pos[0]), nil)
	if err != nil {
		return err
	}
	return c.show(a, fmt.Sprintf("%s (%s): %s, %s, %s", str(a["slug"]), str(a["id"]), str(a["name"]), str(a["os"]), str(a["tier"])),
		fmt.Sprintf("Branches: %s", jsonText(a["branches"])), fmt.Sprintf("Default placement: %s", jsonText(a["default_placement"])))
}

func cmdAppsCreate(ctx context.Context, c *ctxt, args []string) error {
	name := c.fs.String("name", "", "the game's name (default the slug)")
	osName := c.fs.String("os", "", "windows (a zip of a Windows server build) or linux (an image)")
	tier := c.fs.String("tier", "", "standard (default) or high-frequency")
	var regions, continents multi
	c.fs.Var(&regions, "region", "a default region for its game servers, in order of preference (repeatable)")
	c.fs.Var(&continents, "continent", "any region in this continent, after --region (repeatable)")
	pos, err := c.parse(args, "SLUG --os windows|linux [--name N] [--tier T] [--region R]... [--continent C]...", 1, 1)
	if err != nil {
		return err
	}
	if *osName != "windows" && *osName != "linux" {
		return usagef("--os must be windows or linux")
	}
	body := map[string]any{"slug": pos[0], "name": firstOf(*name, pos[0]), "os": *osName}
	if *tier != "" {
		body["tier"] = *tier
	}
	if len(regions) > 0 || len(continents) > 0 {
		p := map[string]any{}
		if len(regions) > 0 {
			p["regions"] = []string(regions)
		}
		if len(continents) > 0 {
			p["continents"] = []string(continents)
		}
		body["default_placement"] = p
	}
	a, err := c.call(ctx, http.MethodPost, "/v0/apps", nil, body)
	if err != nil {
		return err
	}
	return c.show(a, fmt.Sprintf("App %s made (%s, %s, %s).", str(a["slug"]), str(a["id"]), str(a["os"]), str(a["tier"])))
}

// ---- builds ----

func cmdBuildsUpload(ctx context.Context, c *ctxt, args []string) error {
	wait := c.fs.Bool("wait", false, "wait until the build is ready (or failed)")
	timeout := c.fs.Duration("timeout", 30*time.Minute, "how long --wait waits")
	pos, err := c.parse(args, "APP SERVER.zip [--wait]", 2, 2)
	if err != nil {
		return err
	}
	b, err := c.upload(ctx, pos[0], pos[1])
	if err != nil {
		return err
	}
	if *wait {
		return c.waitBuild(ctx, str(b["id"]), "survivalservers builds get "+str(b["id"])+" --wait", *timeout)
	}
	return c.show(b, fmt.Sprintf("Build %s uploaded (%s).", str(b["id"]), str(b["status"])))
}

func (c *ctxt) upload(ctx context.Context, app, zip string) (map[string]any, error) {
	cl, err := c.api()
	if err != nil {
		return nil, err
	}
	info, err := api.InspectZip(zip)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(c.env.Stderr, "uploading %s (%s)\n", info.Filename, sizeText(info.Bytes))
	first := true
	return cl.UploadZip(ctx, app, zip, api.UploadOptions{Storage: c.env.Storage, IdempotencyKey: c.idemFor(http.MethodPost),
		ResumeDir: c.uploadRecords(),
		OnPart: func(p api.UploadProgress) {
			if first && p.Resumed > 0 {
				fmt.Fprintf(c.env.Stderr, "  carrying on: %s was sent by an earlier run\n", sizeText(p.Resumed))
			}
			first = false
			fmt.Fprintf(c.env.Stderr, "  part %d of %d%s\n", p.Parts, p.TotalParts, speedText(p))
		}})
}

// uploadRecords is where the tool keeps what each zip upload has sent, so a re-run carries on: the user's cache
// folder (XDG_CACHE_HOME, ~/.cache, or %LocalAppData% on Windows); "" when none is known.
func (c *ctxt) uploadRecords() string {
	base := c.env.Getenv("XDG_CACHE_HOME")
	if base == "" && runtime.GOOS == "windows" {
		base = c.env.Getenv("LocalAppData")
	}
	if base == "" {
		if home := c.env.Getenv("HOME"); home != "" {
			base = filepath.Join(home, ".cache")
		}
	}
	if base == "" {
		return ""
	}
	return filepath.Join(base, "survivalservers", "uploads")
}

// speedText is ", 320.0 MB of 768.0 MB, 9.8 MB/s, about 45s left" for a part's progress line: the speed counts only
// what this run sent.
func speedText(p api.UploadProgress) string {
	line := fmt.Sprintf(", %s of %s", sizeText(p.Bytes), sizeText(p.TotalBytes))
	sent := p.Bytes - p.Resumed
	secs := p.Elapsed.Seconds()
	if sent <= 0 || secs < 1 {
		return line
	}
	rate := float64(sent) / secs
	line += fmt.Sprintf(", %s/s", sizeText(int64(rate)))
	if left := p.TotalBytes - p.Bytes; left > 0 {
		line += fmt.Sprintf(", about %s left", time.Duration(float64(left)/rate*float64(time.Second)).Round(time.Second))
	}
	return line
}

func cmdBuildsImage(ctx context.Context, c *ctxt, args []string) error {
	cred := c.fs.String("registry-credential", "", "a saved registry login (rcr_...) for a private registry")
	wait := c.fs.Bool("wait", false, "wait until the build is ready (or failed)")
	timeout := c.fs.Duration("timeout", 30*time.Minute, "how long --wait waits")
	pos, err := c.parse(args, "APP IMAGE [--registry-credential ID] [--wait]", 2, 2)
	if err != nil {
		return err
	}
	b, err := c.image(ctx, pos[0], pos[1], *cred)
	if err != nil {
		return err
	}
	if *wait {
		return c.waitBuild(ctx, str(b["id"]), "survivalservers builds get "+str(b["id"])+" --wait", *timeout)
	}
	return c.show(b, fmt.Sprintf("Build %s from %s (%s).", str(b["id"]), pos[1], str(b["status"])))
}

func (c *ctxt) image(ctx context.Context, app, image, cred string) (map[string]any, error) {
	last := image[strings.LastIndex(image, "/")+1:]
	if !strings.Contains(image, "@sha256:") && (!strings.Contains(last, ":") || strings.HasSuffix(last, ":latest")) {
		return nil, usagef("image %q needs a version tag (not latest) or a digest, so the build always means the same image", image)
	}
	src := map[string]any{"type": "image", "image": image}
	if cred != "" {
		src["registry_credential"] = cred
	}
	return c.call(ctx, http.MethodPost, "/v0/apps/"+esc(app)+"/builds", nil, map[string]any{"os": "linux", "source": src})
}

// waitBuild waits for a build. again is "" for a get, or the get command after an upload, which a second run would
// repeat.
func (c *ctxt) waitBuild(ctx context.Context, id, again string, timeout time.Duration) error {
	b, err := c.wait(ctx, waitOn{"build " + id, again, c.showBuild}, timeout, func(ctx context.Context) (map[string]any, error) {
		return c.get(ctx, "/v0/builds/"+esc(id), nil)
	}, api.BuildSettled)
	if err != nil {
		return err
	}
	if err := c.showBuild(b); err != nil {
		return err
	}
	if st := str(b["status"]); st != "ready" {
		f := obj(b["failure"])
		return failedError{fmt.Sprintf("build %s %s: %s %s", id, st, str(f["code"]), str(f["message"]))}
	}
	return nil
}

func (c *ctxt) showBuild(b map[string]any) error {
	lines := []string{fmt.Sprintf("Build %s: %s (%s)", str(b["id"]), str(b["status"]), str(b["os"]))}
	if exes, ok := b["executables"].([]any); ok && len(exes) > 0 {
		var e []string
		for _, x := range exes {
			e = append(e, str(x))
		}
		lines = append(lines, "Programs in the zip: "+strings.Join(e, ", "))
	}
	if d := str(b["image_digest"]); d != "" {
		lines = append(lines, "Image digest: "+d)
	}
	if f := obj(b["failure"]); f != nil {
		lines = append(lines, fmt.Sprintf("Failed: %s %s", str(f["code"]), str(f["message"])))
	}
	return c.show(b, lines...)
}

func cmdBuildsGet(ctx context.Context, c *ctxt, args []string) error {
	wait := c.fs.Bool("wait", false, "wait until it is ready (or failed)")
	timeout := c.fs.Duration("timeout", 30*time.Minute, "how long --wait waits")
	pos, err := c.parse(args, "BUILD [--wait]", 1, 1)
	if err != nil {
		return err
	}
	if *wait {
		return c.waitBuild(ctx, pos[0], "", *timeout)
	}
	b, err := c.get(ctx, "/v0/builds/"+esc(pos[0]), nil)
	if err != nil {
		return err
	}
	return c.showBuild(b)
}

func cmdBuildsList(ctx context.Context, c *ctxt, args []string) error {
	pos, err := c.parse(args, "APP", 1, 1)
	if err != nil {
		return err
	}
	page, err := c.readAll(ctx, "/v0/apps/"+esc(pos[0])+"/builds", nil, 0, "")
	if err != nil {
		return err
	}
	var rows [][]string
	for _, b := range list(page["data"]) {
		src := obj(b["source"])
		rows = append(rows, []string{str(b["id"]), str(b["status"]), str(b["os"]), firstOf(str(src["filename"]), str(src["image"])), str(b["created_at"])})
	}
	return c.table(page["data"], []string{"BUILD", "STATUS", "OS", "SOURCE", "CREATED"}, rows)
}

// cmdRegistryLogin logs docker in to our registry: the password goes to docker on its standard input, never onto a
// command line, into a file or into the output (--json included: an AI agent running this would put it in the chat).
func cmdRegistryLogin(ctx context.Context, c *ctxt, args []string) error {
	if _, err := c.parse(args, "", 0, 0); err != nil {
		return err
	}
	tok, err := c.call(ctx, http.MethodPost, "/v0/registry/token", nil, nil)
	if err != nil {
		return err
	}
	docker := c.env.Docker
	if docker == nil {
		docker = func(ctx context.Context, stdin io.Reader, args ...string) error {
			cmd := exec.CommandContext(ctx, "docker", args...)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, os.Stderr, os.Stderr
			return cmd.Run()
		}
	}
	if err := docker(ctx, strings.NewReader(str(tok["password"])), "login", str(tok["registry"]), "--username", str(tok["username"]), "--password-stdin"); err != nil {
		return fmt.Errorf("docker login: %w", err)
	}
	repo := str(tok["repository"])
	if c.json {
		return c.printJSON(map[string]any{"logged_in": true, "registry": tok["registry"], "repository": repo, "username": tok["username"], "expires_at": tok["expires_at"]})
	}
	fmt.Fprintf(c.env.Stdout, "Logged in to %s until %s.\nPush to %s/<server>:<version>, then: survivalservers builds image APP %s/<server>:<version>\n",
		str(tok["registry"]), str(tok["expires_at"]), repo, repo)
	return nil
}

// ---- versions and branches ----

func cmdVersionsCreate(ctx context.Context, c *ctxt, args []string) error {
	name := c.fs.String("name", "", "the version's name (overrides the file's)")
	build := c.fs.String("build", "", "the build (overrides the file's)")
	dry := c.fs.Bool("dry-run", false, "check the settings and print the request without making the version")
	wait := c.fs.Bool("wait", false, "wait until a region has it ready")
	timeout := c.fs.Duration("timeout", 30*time.Minute, "how long --wait waits")
	pos, err := c.parse(args, "APP SETTINGS.json [--name N] [--build B] [--dry-run] [--wait]", 2, 2)
	if err != nil {
		return err
	}
	body, err := readJSONFile(pos[1])
	if err != nil {
		return err
	}
	if *name != "" {
		body["name"] = *name
	}
	if *build != "" {
		body["build"] = *build
	}
	v, err := c.createVersion(ctx, pos[0], body, *dry)
	if err != nil || *dry {
		return err
	}
	if *wait {
		return c.waitVersion(ctx, pos[0], str(v["name"]), "survivalservers versions get "+pos[0]+" "+str(v["name"])+" --wait", *timeout)
	}
	return c.show(v, fmt.Sprintf("Version %s of %s made; copying to %d regions.", str(v["name"]), pos[0], len(list(v["regions"]))))
}

// createVersion checks a version body (client.CheckVersion, the same checks as the MCP server's) and makes it.
func (c *ctxt) createVersion(ctx context.Context, app string, body map[string]any, dry bool) (map[string]any, error) {
	a, err := c.get(ctx, "/v0/apps/"+esc(app), nil)
	if err != nil {
		return nil, err
	}
	var b map[string]any
	if id := str(body["build"]); id != "" && id != "BUILD_ID" {
		if b, err = c.get(ctx, "/v0/builds/"+esc(id), nil); err != nil {
			return nil, err
		}
	} else {
		body["build"] = ""
	}
	if problems := api.CheckVersion(body, a, b); len(problems) > 0 {
		return nil, failedError{"the version settings need changes before they are sent:\n  - " + strings.Join(problems, "\n  - ")}
	}
	if dry {
		fmt.Fprintln(c.env.Stderr, "The settings pass the checks. The request:")
		return nil, c.printJSON(body)
	}
	return c.call(ctx, http.MethodPost, "/v0/apps/"+esc(app)+"/versions", nil, body)
}

// waitVersion waits for a version to be ready in a region. again is "" for a get, or the get command after a
// create, which a second run would repeat.
func (c *ctxt) waitVersion(ctx context.Context, app, version, again string, timeout time.Duration) error {
	v, err := c.wait(ctx, waitOn{"version " + version, again, c.showVersion}, timeout, func(ctx context.Context) (map[string]any, error) {
		return c.get(ctx, "/v0/apps/"+esc(app)+"/versions/"+esc(version), nil)
	}, api.VersionSettled)
	if err != nil {
		return err
	}
	if err := c.showVersion(v); err != nil {
		return err
	}
	if ready, _, _ := api.VersionRegions(v); ready == 0 {
		if api.VersionRemoved(v) > 0 {
			return failedError{fmt.Sprintf("version %s is ready in no region: the machines removed it (put it on a branch to have it copied again)", version)}
		}
		return failedError{fmt.Sprintf("version %s failed in every region", version)}
	}
	return nil
}

func (c *ctxt) showVersion(v map[string]any) error {
	var regions []string
	for _, r := range list(v["regions"]) {
		regions = append(regions, str(r["region"])+" "+str(r["status"]))
	}
	ready, _, all := api.VersionRegions(v)
	return c.show(v, fmt.Sprintf("Version %s (build %s): ready in %d of %d regions", str(v["name"]), str(v["build"]), ready, all),
		"  "+strings.Join(regions, ", "))
}

func cmdVersionsList(ctx context.Context, c *ctxt, args []string) error {
	pos, err := c.parse(args, "APP", 1, 1)
	if err != nil {
		return err
	}
	page, err := c.readAll(ctx, "/v0/apps/"+esc(pos[0])+"/versions", nil, 0, "")
	if err != nil {
		return err
	}
	var rows [][]string
	for _, v := range list(page["data"]) {
		ready, _, all := api.VersionRegions(v)
		size := obj(v["size"])
		rows = append(rows, []string{str(v["name"]), str(v["build"]), str(size["cpu"]) + " CPU / " + str(size["memory_gb"]) + " GB",
			fmt.Sprintf("%d/%d", ready, all), fmt.Sprint(v["deprecated"]), str(v["created_at"])})
	}
	return c.table(page["data"], []string{"VERSION", "BUILD", "SIZE", "REGIONS READY", "DEPRECATED", "CREATED"}, rows)
}

func cmdVersionsGet(ctx context.Context, c *ctxt, args []string) error {
	wait := c.fs.Bool("wait", false, "wait until a region has it ready")
	timeout := c.fs.Duration("timeout", 30*time.Minute, "how long --wait waits")
	pos, err := c.parse(args, "APP VERSION [--wait]", 2, 2)
	if err != nil {
		return err
	}
	if *wait {
		return c.waitVersion(ctx, pos[0], pos[1], "", *timeout)
	}
	v, err := c.get(ctx, "/v0/apps/"+esc(pos[0])+"/versions/"+esc(pos[1]), nil)
	if err != nil {
		return err
	}
	return c.showVersion(v)
}

// cmdBuildsDelete deletes a build no version uses (the API answers 409 in_use while one does). A pipeline that makes a
// build on every commit deletes the old ones with it; an app holds 200 versions.
func cmdBuildsDelete(ctx context.Context, c *ctxt, args []string) error {
	pos, err := c.parse(args, "BUILD", 1, 1)
	if err != nil {
		return err
	}
	if _, err := c.call(ctx, http.MethodDelete, "/v0/builds/"+esc(pos[0]), nil, nil); err != nil {
		return err
	}
	return c.show(map[string]any{"id": pos[0], "deleted": true}, "Build "+pos[0]+" deleted.")
}

// cmdVersionsDelete deletes a version nothing uses (409 in_use while a game server runs it, a branch points at it or
// a persistent server uses it). Its build stays until deleted with builds delete.
func cmdVersionsDelete(ctx context.Context, c *ctxt, args []string) error {
	pos, err := c.parse(args, "APP VERSION", 2, 2)
	if err != nil {
		return err
	}
	if _, err := c.call(ctx, http.MethodDelete, "/v0/apps/"+esc(pos[0])+"/versions/"+esc(pos[1]), nil, nil); err != nil {
		return err
	}
	return c.show(map[string]any{"app": pos[0], "version": pos[1], "deleted": true}, "Version "+pos[1]+" of "+pos[0]+" deleted.")
}

// cmdVersionsDeprecate stops new starts of a version (running game servers carry on); --undo allows them again.
func cmdVersionsDeprecate(ctx context.Context, c *ctxt, args []string) error {
	undo := c.fs.Bool("undo", false, "allow new starts again")
	pos, err := c.parse(args, "APP VERSION [--undo]", 2, 2)
	if err != nil {
		return err
	}
	v, err := c.call(ctx, http.MethodPatch, "/v0/apps/"+esc(pos[0])+"/versions/"+esc(pos[1]), nil, map[string]any{"deprecated": !*undo})
	if err != nil {
		return err
	}
	if *undo {
		return c.show(v, "Version "+pos[1]+" of "+pos[0]+" takes new starts again.")
	}
	return c.show(v, "Version "+pos[1]+" of "+pos[0]+" is deprecated: no new starts; running game servers carry on.")
}

func cmdBranchesSet(ctx context.Context, c *ctxt, args []string) error {
	pos, err := c.parse(args, "APP BRANCH VERSION", 3, 3)
	if err != nil {
		return err
	}
	out, err := c.call(ctx, http.MethodPut, "/v0/apps/"+esc(pos[0])+"/branches/"+esc(pos[1]), nil, map[string]any{"version": pos[2]})
	if err != nil {
		return err
	}
	return c.show(out, fmt.Sprintf("Branch %s of %s now points at %s.", pos[1], pos[0], pos[2]))
}

func cmdBranchesList(ctx context.Context, c *ctxt, args []string) error {
	pos, err := c.parse(args, "APP", 1, 1)
	if err != nil {
		return err
	}
	branches, err := c.get(ctx, "/v0/apps/"+esc(pos[0])+"/branches", nil) // branch name to version name
	if err != nil {
		return err
	}
	var rows [][]string
	for name, v := range branches {
		rows = append(rows, []string{name, str(v)})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i][0] < rows[j][0] })
	return c.table(branches, []string{"BRANCH", "VERSION"}, rows)
}

// cmdDeploy is the CI step: a build from a zip or an image, a version from a settings file, waiting for each, and
// a branch pointed at it.
func cmdDeploy(ctx context.Context, c *ctxt, args []string) error {
	zip := c.fs.String("zip", "", "a zip of the Windows server build to upload")
	image := c.fs.String("image", "", "an image for a Linux build")
	cred := c.fs.String("registry-credential", "", "with --image: a saved registry login for a private registry")
	settings := c.fs.String("settings", "", "the version settings (a version without its build and name)")
	name := c.fs.String("name", "", "the new version's name, for example 1.4.3")
	branch := c.fs.String("branch", "", "set this branch to the new version once a region has it")
	timeout := c.fs.Duration("timeout", 45*time.Minute, "how long to wait for the build and the version")
	pos, err := c.parse(args, "APP (--zip FILE | --image REF) --settings SETTINGS.json --name N [--branch B]", 1, 1)
	if err != nil {
		return err
	}
	app := pos[0]
	if (*zip == "") == (*image == "") || *settings == "" || *name == "" {
		return usagef("give --zip or --image, and --settings and --name")
	}
	body, err := readJSONFile(*settings)
	if err != nil {
		return err
	}
	var b map[string]any
	if *zip != "" {
		b, err = c.upload(ctx, app, *zip)
	} else {
		b, err = c.image(ctx, app, *image, *cred)
	}
	if err != nil {
		return err
	}
	build := str(b["id"])
	fmt.Fprintf(c.env.Stderr, "build %s: %s\n", build, str(b["status"]))
	if err := c.quiet(func() error {
		return c.waitBuild(ctx, build, "survivalservers builds get "+build+" --wait, then survivalservers versions create with --build "+build, *timeout)
	}); err != nil {
		return err
	}
	body["name"], body["build"] = *name, build
	v, err := c.createVersion(ctx, app, body, false)
	if err != nil {
		return err
	}
	fmt.Fprintf(c.env.Stderr, "version %s made\n", str(v["name"]))
	again := "survivalservers versions get " + app + " " + *name + " --wait"
	if *branch != "" {
		again += ", then survivalservers branches set " + app + " " + *branch + " " + *name
	}
	if err := c.quiet(func() error { return c.waitVersion(ctx, app, *name, again, *timeout) }); err != nil {
		return err
	}
	if *branch != "" {
		if _, err := c.call(ctx, http.MethodPut, "/v0/apps/"+esc(app)+"/branches/"+esc(*branch), nil, map[string]any{"version": *name}); err != nil {
			return err
		}
	}
	v, err = c.get(ctx, "/v0/apps/"+esc(app)+"/versions/"+esc(*name), nil)
	if err != nil {
		return err
	}
	ready, _, all := api.VersionRegions(v)
	line := fmt.Sprintf("Deployed %s %s (build %s): ready in %d of %d regions", app, *name, build, ready, all)
	if *branch != "" {
		line += "; branch " + *branch + " points at it"
	}
	return c.show(v, line+".")
}

// quiet runs f with stdout held back, so a multi-step command prints one summary.
func (c *ctxt) quiet(f func() error) error {
	out, js := c.env.Stdout, c.json
	c.env.Stdout, c.json = io.Discard, false
	defer func() { c.env.Stdout, c.json = out, js }()
	return f()
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
