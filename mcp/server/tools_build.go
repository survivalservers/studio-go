package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"regexp"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	api "github.com/survivalservers/studio-go"
)

var (
	osValues    = []string{"windows", "linux"}
	tierValues  = []string{"standard", "high-frequency"}
	slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)
)

type accountIn struct{}

type regionsIn struct {
	OS        string `json:"os,omitempty" jsonschema:"only regions that offer this system"`
	Tier      string `json:"tier,omitempty" jsonschema:"only regions that offer this tier"`
	Continent string `json:"continent,omitempty" jsonschema:"only regions in this continent, for example north-america"`
	App       string `json:"app,omitempty" jsonschema:"with version: show whether each region has that version ready"`
	Version   string `json:"version,omitempty" jsonschema:"a version name of app"`
}

type createAppIn struct {
	Name       string   `json:"name" jsonschema:"the game's name as people know it"`
	Slug       string   `json:"slug,omitempty" jsonschema:"short name of lowercase letters, digits and dashes, used in every other call; made from name when left out"`
	OS         string   `json:"os" jsonschema:"windows for a zip of a Windows server build, linux for a Linux image"`
	Tier       string   `json:"tier,omitempty" jsonschema:"standard (default) or high-frequency for 64 to 128 Hz servers that need the fastest cores"`
	Regions    []string `json:"regions,omitempty" jsonschema:"default regions for its game servers, in order of preference (from list_regions)"`
	Continents []string `json:"continents,omitempty" jsonschema:"default continents: any region in them, after regions; for example north-america or europe"`
}

type appIn struct {
	App string `json:"app" jsonschema:"the app's slug or ID"`
}

type uploadIn struct {
	App     string `json:"app" jsonschema:"the app's slug or ID (a windows app)"`
	ZipPath string `json:"zip_path" jsonschema:"absolute path of the zip of the server build on this computer; the zip's top level is the build's folder"`
}

type beginUploadIn struct {
	App      string `json:"app" jsonschema:"the app's slug or ID (a windows app)"`
	Filename string `json:"filename" jsonschema:"the zip's file name"`
	Bytes    int64  `json:"bytes" jsonschema:"the zip's exact size in bytes"`
	SHA256   string `json:"sha256" jsonschema:"the zip's SHA-256 in hex (sha256sum or shasum -a 256)"`
	ZipPath  string `json:"zip_path,omitempty" jsonschema:"where the zip is on the developer's computer, written into the upload script"`
}

type completeUploadIn struct {
	Build string             `json:"build" jsonschema:"the build ID from begin_windows_upload"`
	Parts []api.UploadedPart `json:"parts" jsonschema:"every part's number and the ETag storage answered, as the upload script printed them"`
}

type linuxBuildIn struct {
	App                string `json:"app" jsonschema:"the app's slug or ID (a linux app)"`
	Image              string `json:"image" jsonschema:"full image reference with a tag or digest, for example registry.example.com/studio/server:1.4.2; never latest"`
	RegistryCredential string `json:"registry_credential,omitempty" jsonschema:"ID of a saved registry login for a private registry (made in the panel)"`
}

type waitBuildIn struct {
	Build       string `json:"build" jsonschema:"the build ID"`
	WaitSeconds *int   `json:"wait_seconds,omitempty" jsonschema:"how long to wait for ready or failed: 0 reads it now; default 300, at most 900"`
}

type portIn struct {
	Name          string `json:"name" jsonschema:"lowercase name; call the main one game"`
	Protocol      string `json:"protocol" jsonschema:"udp or tcp"`
	ContainerPort int    `json:"container_port,omitempty" jsonschema:"Linux images: the port the server listens on inside the image (none for a passthrough port)"`
	PortPolicy    string `json:"port_policy,omitempty" jsonschema:"none for an admin port (RCON, a web admin page): never open to the internet, reached only from its machine; passthrough (Linux images) for a public port the game listens on by its public number, {{port.<name>}}, with no container_port: use it for a game that tells Steam's server list its own port; empty or dynamic is a public port"`
}

type readyIn struct {
	Type    string `json:"type" jsonschema:"local_api: the server calls POST /v0/self/ready (best); log: a line in its log; port: the port is bound; delay: after a fixed time; none: at once"`
	Pattern string `json:"pattern,omitempty" jsonschema:"log: the text (a regular expression) that means ready, for example Server ready for players"`
	Source  string `json:"source,omitempty" jsonschema:"log: which log to watch (console by default)"`
	Port    string `json:"port,omitempty" jsonschema:"port: the port's name"`
	After   string `json:"after,omitempty" jsonschema:"delay: for example 20s"`
}

type versionIn struct {
	App           string            `json:"app" jsonschema:"the app's slug or ID"`
	Name          string            `json:"name" jsonschema:"version name, unique in the app, for example 1.4.2"`
	Build         string            `json:"build" jsonschema:"a ready build of this app"`
	Tier          string            `json:"tier,omitempty" jsonschema:"default: the app's tier"`
	CPU           float64           `json:"cpu" jsonschema:"vCPUs (hardware threads) per game server: standard in 0.25 steps, high-frequency whole vCPUs"`
	MemoryGB      float64           `json:"memory_gb" jsonschema:"memory per game server in GB: 0.5 to 4 per CPU"`
	Executable    string            `json:"executable,omitempty" jsonschema:"Windows: the program's path inside the zip, for example Binaries/Win64/GameServer.exe. Linux: optional, replaces the image's entrypoint"`
	Args          []string          `json:"args,omitempty" jsonschema:"launch arguments; may use {{port.game}}, {{port.game.internal}} (Linux), {{public_ip}}, {{id}}, {{region}}, {{cpu}} and {{memory_mb}}"`
	Env           map[string]string `json:"env,omitempty" jsonschema:"environment variables for every game server of this version"`
	Ports         []portIn          `json:"ports" jsonschema:"named ports; a fresh public port is assigned on every start"`
	ReadyCheck    readyIn           `json:"ready_check" jsonschema:"when a game server counts as joinable"`
	StopMethod    string            `json:"stop_method,omitempty" jsonschema:"how it is asked to stop. Windows: ctrl_break (default), ctrl_c, wm_close, local_api, kill. Linux: sigterm (default), sigint, local_api, kill. local_api for engines that ignore signals (Godot)"`
	StopGrace     string            `json:"stop_shutdown_time,omitempty" jsonschema:"time to save and exit before the process tree is killed (default 30s)"`
	BootTimeout   string            `json:"boot_timeout,omitempty" jsonschema:"how long it may take to become ready"`
	LogPaths      []string          `json:"log_paths,omitempty" jsonschema:"log files inside the server's folder to keep, besides the console"`
	ArtifactPaths []string          `json:"artifact_paths,omitempty" jsonschema:"other files to keep after it exits (saves, results)"`
	CrashDump     string            `json:"crash_dump,omitempty" jsonschema:"Windows crash dumps: mini (default), full or off"`
	Compat        []string          `json:"compat,omitempty" jsonschema:"for a build made for another host: agones (it uses the Agones SDK) or playfab (it uses PlayFab's GSDK); the machine answers as that SDK, so the build runs as it is. Use ready_check local_api with it"`
	Restart       string            `json:"restart,omitempty" jsonschema:"never (default) or on_failure"`
	MaxDuration   string            `json:"max_duration,omitempty" jsonschema:"hard limit for one match, for example 2h"`
	Regions       []string          `json:"regions,omitempty" jsonschema:"where to stage it ahead of time; default every region that offers the app's tier and system"`
	DryRun        bool              `json:"dry_run,omitempty" jsonschema:"check everything and show the request without making the version"`
}

type waitVersionIn struct {
	App         string `json:"app" jsonschema:"the app's slug or ID"`
	Version     string `json:"version" jsonschema:"the version's name"`
	WaitSeconds *int   `json:"wait_seconds,omitempty" jsonschema:"how long to wait for the first region to have it: 0 reads it now; default 300, at most 900"`
}

type branchIn struct {
	App     string `json:"app" jsonschema:"the app's slug or ID"`
	Branch  string `json:"branch" jsonschema:"branch name, for example live or beta"`
	Version string `json:"version" jsonschema:"the version name the branch runs from now on"`
}

func (s *srv) addBuildTools(server *mcp.Server) {
	tool(s, server, "get_account", "Studio account", "The studio this key belongs to: its limits, free vCPU-hours used this month, whether a card is on file, and until when its prices are locked.", reads, nil,
		func(ctx context.Context, _ *mcp.CallToolRequest, _ accountIn) (*mcp.CallToolResult, error) {
			studio, err := s.client.Get(ctx, "/v0/studio", nil)
			if err != nil {
				return nil, err
			}
			return textResult(fmt.Sprintf("Studio %s (%s).", str(studio["name"]), str(studio["status"])), studio), nil
		})

	tool(s, server, "list_regions", "List regions", "Regions game servers can run in, with their continent, what they offer (tier and system) and, given app and version, whether that version is ready there.", reads,
		map[string][]string{"os": osValues, "tier": tierValues},
		func(ctx context.Context, _ *mcp.CallToolRequest, in regionsIn) (*mcp.CallToolResult, error) {
			if in.App != "" {
				if err := s.allowedApp(ctx, in.App); err != nil {
					return nil, err
				}
			}
			q := url.Values{"limit": {"500"}}
			for k, v := range map[string]string{"os": in.OS, "tier": in.Tier, "continent": in.Continent, "app": in.App, "version": in.Version} {
				if v != "" {
					q.Set(k, v)
				}
			}
			page, err := s.client.Get(ctx, "/v0/regions", q)
			if err != nil {
				return nil, err
			}
			rows := []map[string]any{}
			for _, r := range list(page["data"]) {
				rows = append(rows, pick(r, "id", "name", "country", "continent", "status", "offers", "version_ready"))
			}
			return textResult(fmt.Sprintf("%d regions.", len(rows)), rows), nil
		})

	tool(s, server, "list_apps", "List apps", "The studio's apps (one per game), with their system, tier, default placement and branches.", reads, nil,
		func(ctx context.Context, _ *mcp.CallToolRequest, _ noInput) (*mcp.CallToolResult, error) {
			page, err := s.client.Get(ctx, "/v0/apps", url.Values{"limit": {"500"}})
			if err != nil {
				return nil, err
			}
			apps, err := s.filterAllowed(ctx, list(page["data"]), "id")
			if err != nil {
				return nil, err
			}
			rows := []map[string]any{}
			for _, a := range apps {
				rows = append(rows, pick(a, "id", "slug", "name", "os", "tier", "default_placement", "branches"))
			}
			return textResult(fmt.Sprintf("%d apps.", len(rows)), rows), nil
		})

	tool(s, server, "create_app", "Create an app", "Makes an app: one per game, for one system. Windows apps take zips of a Windows server build; Linux apps take images.", changes,
		map[string][]string{"os": osValues, "tier": tierValues},
		func(ctx context.Context, _ *mcp.CallToolRequest, in createAppIn) (*mcp.CallToolResult, error) {
			slug := in.Slug
			if slug == "" {
				slug = slugify(in.Name)
			}
			if !slugPattern.MatchString(slug) {
				return nil, fmt.Errorf("slug %q must be 2 to 63 lowercase letters, digits and dashes, starting with a letter or digit", slug)
			}
			if err := s.allowedApp(ctx, slug); err != nil {
				return nil, err
			}
			body := map[string]any{"name": in.Name, "slug": slug, "os": in.OS}
			if in.Tier != "" {
				body["tier"] = in.Tier
			}
			if len(in.Regions) > 0 || len(in.Continents) > 0 {
				p := map[string]any{}
				if len(in.Regions) > 0 {
					p["regions"] = in.Regions
				}
				if len(in.Continents) > 0 {
					p["continents"] = in.Continents
				}
				body["default_placement"] = p
			}
			var app map[string]any
			if err := s.client.JSON(ctx, api.Call{Method: http.MethodPost, Path: "/v0/apps", Body: body}, &app); err != nil {
				return nil, err
			}
			s.forgetApps()
			return textResult(fmt.Sprintf("App %s made (%s, %s).", str(app["slug"]), str(app["os"]), str(app["tier"])), app), nil
		})

	if s.cfg.Local {
		tool(s, server, "upload_windows_build", "Upload a Windows build", "Uploads a zip of a Windows server build from this computer as a new build of a windows app: in parts, straight to storage. Then wait_for_build until it is ready (the zip is checked and its programs listed).", changes, nil,
			func(ctx context.Context, req *mcp.CallToolRequest, in uploadIn) (*mcp.CallToolResult, error) {
				if err := s.allowedApp(ctx, in.App); err != nil {
					return nil, err
				}
				build, err := s.client.UploadZip(ctx, in.App, in.ZipPath, api.UploadOptions{Storage: s.cfg.Storage, Progress: func(done, total int) {
					if tok := req.Params.GetProgressToken(); tok != nil && req.Session != nil {
						_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{ProgressToken: tok, Progress: float64(done), Total: float64(total), Message: fmt.Sprintf("part %d of %d uploaded", done, total)})
					}
				}})
				if err != nil {
					return nil, err
				}
				return textResult(fmt.Sprintf("Build %s uploaded (%s). Next: wait_for_build.", str(build["id"]), str(build["status"])), build), nil
			})
	} else {
		tool(s, server, "begin_windows_upload", "Begin a Windows build upload", "Starts a zip upload of a Windows server build and returns a script that sends the zip's parts straight to storage from the developer's computer (bash: run it in a terminal, Git Bash or WSL). Run it, then complete_windows_upload with the parts it prints.", changes, nil,
			func(ctx context.Context, _ *mcp.CallToolRequest, in beginUploadIn) (*mcp.CallToolResult, error) {
				if err := s.allowedApp(ctx, in.App); err != nil {
					return nil, err
				}
				if !regexp.MustCompile(`^[0-9a-fA-F]{64}$`).MatchString(in.SHA256) {
					return nil, fmt.Errorf("sha256 must be 64 hex characters")
				}
				var build map[string]any
				err := s.client.JSON(ctx, api.Call{Method: http.MethodPost, Path: "/v0/apps/" + api.PathEscape(in.App) + "/builds", Body: map[string]any{
					"os": "windows", "source": map[string]any{"type": "upload", "filename": in.Filename, "bytes": in.Bytes, "sha256": strings.ToLower(in.SHA256)},
				}}, &build)
				if err != nil {
					return nil, err
				}
				script, err := uploadScript(build, firstOf(in.ZipPath, in.Filename))
				if err != nil {
					return nil, err
				}
				return plainResult(fmt.Sprintf("Build %s is waiting for its upload. Run this script where the zip is; it prints the parts for complete_windows_upload. The links expire at %s.\n\n```bash\n%s```", str(build["id"]), str(obj(build["upload"])["expires_at"]), script)), nil
			})

		tool(s, server, "complete_windows_upload", "Complete a Windows build upload", "Finishes a zip upload started with begin_windows_upload, with each part's number and ETag. Then wait_for_build.", changes, nil,
			func(ctx context.Context, _ *mcp.CallToolRequest, in completeUploadIn) (*mcp.CallToolResult, error) {
				if _, err := s.build(ctx, in.Build); err != nil {
					return nil, err
				}
				var build map[string]any
				if err := s.client.JSON(ctx, api.Call{Method: http.MethodPost, Path: "/v0/builds/" + api.PathEscape(in.Build) + "/complete", Body: map[string]any{"parts": in.Parts}}, &build); err != nil {
					return nil, err
				}
				return textResult(fmt.Sprintf("Build %s uploaded (%s). Next: wait_for_build.", str(build["id"]), str(build["status"])), build), nil
			})
	}

	tool(s, server, "create_linux_build", "Create a Linux build", "Makes a build of a linux app from a container image in any registry we can pull from (public, a private one with a saved login, or ours via get_registry_login). The image is copied by digest; then wait_for_build.", changes, nil,
		func(ctx context.Context, _ *mcp.CallToolRequest, in linuxBuildIn) (*mcp.CallToolResult, error) {
			if err := s.allowedApp(ctx, in.App); err != nil {
				return nil, err
			}
			if err := checkImage(in.Image); err != nil {
				return nil, err
			}
			src := map[string]any{"type": "image", "image": in.Image}
			if in.RegistryCredential != "" {
				src["registry_credential"] = in.RegistryCredential
			}
			var build map[string]any
			if err := s.client.JSON(ctx, api.Call{Method: http.MethodPost, Path: "/v0/apps/" + api.PathEscape(in.App) + "/builds", Body: map[string]any{"os": "linux", "source": src}}, &build); err != nil {
				return nil, err
			}
			return textResult(fmt.Sprintf("Build %s from %s (%s). Next: wait_for_build.", str(build["id"]), in.Image, str(build["status"])), build), nil
		})

	if s.cfg.Local {
		tool(s, server, "get_registry_login", "Log Docker in to our registry", "Logs Docker on this computer in to our registry for pushing Linux images, for an hour, and says where to push. The password goes from the API straight to docker login on its standard input: it is never in this answer, the chat or a command line.", changes, nil,
			func(ctx context.Context, _ *mcp.CallToolRequest, _ noInput) (*mcp.CallToolResult, error) {
				var tok map[string]any
				if err := s.client.JSON(ctx, api.Call{Method: http.MethodPost, Path: "/v0/registry/token"}, &tok); err != nil {
					return nil, err
				}
				registry, repo := str(tok["registry"]), str(tok["repository"])
				out, err := s.docker(ctx, strings.NewReader(str(tok["password"])), "login", registry, "--username", str(tok["username"]), "--password-stdin")
				if err != nil {
					return nil, fmt.Errorf("docker login to %s failed (%v)%s. In a terminal, survivalservers registry login does the same", registry, err, dockerSays(out))
				}
				return plainResult(fmt.Sprintf(`Docker on this computer is logged in to %s until %s. The password stayed between this tool and Docker.

Push under %s:

`+"```bash"+`
docker build --platform linux/amd64 -t %s/<server>:<version> .
docker push %s/<server>:<version>
`+"```"+`

Then create_linux_build with image %s/<server>:<version>.`, registry, str(tok["expires_at"]), repo, repo, repo, repo)), nil
			})
	} else {
		tool(s, server, "get_registry_login", "Log Docker in to our registry", "Says how to log Docker in to our registry for pushing Linux images. This hosted server never hands out the registry password, because the answer would land in the chat: the developer runs survivalservers registry login in a terminal.", changes, nil,
			func(ctx context.Context, _ *mcp.CallToolRequest, _ noInput) (*mcp.CallToolResult, error) {
				return plainResult("This hosted server does not hand out the registry password: it would land in the chat. Ask the developer to run this in a terminal on their computer, where Docker is:\n\n```bash\nsurvivalservers registry login\n```\n\nIt logs Docker in for an hour without showing the password and prints where to push. After the push, call create_linux_build with the pushed image's name."), nil
			})
	}

	tool(s, server, "wait_for_build", "Wait for a build", "Reads a build and waits until it is ready or failed. A ready Windows build lists the programs found in the zip (use one as the version's executable); a failed one says why.", reads, nil,
		func(ctx context.Context, req *mcp.CallToolRequest, in waitBuildIn) (*mcp.CallToolResult, error) {
			if _, err := s.build(ctx, in.Build); err != nil {
				return nil, err
			}
			b, done, waited, err := s.wait(ctx, req, waitSeconds(in.WaitSeconds, 300), func(ctx context.Context) (map[string]any, error) {
				return s.client.Get(ctx, "/v0/builds/"+api.PathEscape(in.Build), nil)
			}, api.BuildSettled)
			if err != nil {
				return nil, err
			}
			st := str(b["status"])
			summary := fmt.Sprintf("Build %s is %s.", in.Build, st)
			switch {
			case st == "ready":
				summary += " Next: create_version."
			case st == "failed":
				f := obj(b["failure"])
				summary += fmt.Sprintf(" %s: %s", str(f["code"]), str(f["message"]))
			case !done:
				summary += fmt.Sprintf(" Still %s after %s; call wait_for_build again.", st, waited.Round(1e9))
			}
			return textResult(summary, b), nil
		})

	tool(s, server, "list_versions", "List versions", "An app's versions, newest first: build, size, launch settings and which regions have each one ready.", reads, nil,
		func(ctx context.Context, _ *mcp.CallToolRequest, in appIn) (*mcp.CallToolResult, error) {
			if err := s.allowedApp(ctx, in.App); err != nil {
				return nil, err
			}
			page, err := s.client.Get(ctx, "/v0/apps/"+api.PathEscape(in.App)+"/versions", url.Values{"limit": {"100"}})
			if err != nil {
				return nil, err
			}
			rows := []map[string]any{}
			for _, v := range list(page["data"]) {
				rows = append(rows, pick(v, "name", "build", "tier", "os", "size", "launch", "regions", "deprecated", "created_at"))
			}
			return textResult(fmt.Sprintf("%d versions of %s.", len(rows), in.App), rows), nil
		})

	tool(s, server, "create_version", "Create a version", "Makes a version: a ready build plus how to launch it. Checks the settings first (sizes, ports, ready check, stop method for the app's system, that the executable is in the zip) and says what to fix. dry_run shows the request without making it. Then wait_for_version.", changes,
		map[string][]string{"tier": tierValues, "ports.protocol": {"udp", "tcp"}, "ready_check.type": {"local_api", "log", "port", "delay", "none"},
			"stop_method": {"ctrl_break", "ctrl_c", "wm_close", "local_api", "kill", "sigterm", "sigint"}, "crash_dump": {"mini", "full", "off"}, "restart": {"never", "on_failure"},
			"compat": {"agones", "playfab"}},
		func(ctx context.Context, _ *mcp.CallToolRequest, in versionIn) (*mcp.CallToolResult, error) {
			if err := s.allowedApp(ctx, in.App); err != nil {
				return nil, err
			}
			app, err := s.client.Get(ctx, "/v0/apps/"+api.PathEscape(in.App), nil)
			if err != nil {
				return nil, err
			}
			build, err := s.build(ctx, in.Build)
			if err != nil {
				return nil, err
			}
			body := versionBody(in)
			if problems := api.CheckVersion(body, app, build); len(problems) > 0 {
				return nil, fmt.Errorf("the version settings need changes before they are sent:\n- %s", strings.Join(problems, "\n- "))
			}
			if in.DryRun {
				return textResult("The settings pass the checks. This is the request create_version would send:", body), nil
			}
			var v map[string]any
			if err := s.client.JSON(ctx, api.Call{Method: http.MethodPost, Path: "/v0/apps/" + api.PathEscape(in.App) + "/versions", Body: body}, &v); err != nil {
				return nil, err
			}
			return textResult(fmt.Sprintf("Version %s of %s made; copying to %d regions. Next: wait_for_version.", str(v["name"]), in.App, len(list(v["regions"]))), v), nil
		})

	tool(s, server, "wait_for_version", "Wait for a version", "Reads a version and waits until at least one region has it ready (or every region failed). Game servers start only in regions where it is ready.", reads, nil,
		func(ctx context.Context, req *mcp.CallToolRequest, in waitVersionIn) (*mcp.CallToolResult, error) {
			if err := s.allowedApp(ctx, in.App); err != nil {
				return nil, err
			}
			path := "/v0/apps/" + api.PathEscape(in.App) + "/versions/" + api.PathEscape(in.Version)
			v, done, waited, err := s.wait(ctx, req, waitSeconds(in.WaitSeconds, 300), func(ctx context.Context) (map[string]any, error) {
				return s.client.Get(ctx, path, nil)
			}, api.VersionSettled)
			if err != nil {
				return nil, err
			}
			ready, failed, all := api.VersionRegions(v)
			summary := fmt.Sprintf("Version %s: ready in %d of %d regions", in.Version, ready, all)
			if failed > 0 {
				summary += fmt.Sprintf(", failed in %d", failed)
			}
			if removed := api.VersionRemoved(v); removed > 0 {
				summary += fmt.Sprintf(", removed from the machines in %d", removed)
			}
			summary += "."
			if ready > 0 {
				summary += " Next: start_game_server or create_persistent_server."
			} else if api.VersionRemoved(v) > 0 && done {
				summary += " Put it on a branch (set_branch) to have it copied again."
			} else if !done {
				summary += fmt.Sprintf(" Still copying after %s; call wait_for_version again.", waited.Round(1e9))
			}
			return textResult(summary, pick(v, "name", "build", "os", "tier", "size", "regions", "deprecated")), nil
		})

	tool(s, server, "set_branch", "Set a branch's version", "Sets the version a branch (for example live or beta) runs, making the branch if it is new. New game servers and persistent servers that start from the branch get that version; running ones keep theirs. To roll back, set the branch to the version it had before.", removes, nil,
		func(ctx context.Context, _ *mcp.CallToolRequest, in branchIn) (*mcp.CallToolResult, error) {
			if err := s.allowedApp(ctx, in.App); err != nil {
				return nil, err
			}
			var out map[string]any
			err := s.client.JSON(ctx, api.Call{Method: http.MethodPut, Path: "/v0/apps/" + api.PathEscape(in.App) + "/branches/" + api.PathEscape(in.Branch), Body: map[string]any{"version": in.Version}}, &out)
			if err != nil {
				return nil, err
			}
			return textResult(fmt.Sprintf("Branch %s of %s now points at %s.", in.Branch, in.App, in.Version), out), nil
		})
}

// docker runs docker with stdin and returns what it printed (Config.Docker in tests). Its output is kept, never
// passed through: on the local server, standard output is the MCP connection itself.
func (s *srv) docker(ctx context.Context, stdin io.Reader, args ...string) ([]byte, error) {
	if s.cfg.Docker != nil {
		return s.cfg.Docker(ctx, stdin, args...)
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdin = stdin
	return cmd.CombinedOutput()
}

// dockerSays is the end of what docker printed, for an error.
func dockerSays(out []byte) string {
	text := strings.TrimSpace(string(out))
	if text == "" {
		return ""
	}
	if len(text) > 500 {
		text = "..." + text[len(text)-500:]
	}
	return ": " + text
}

// build reads a build and checks it belongs to an allowed app.
func (s *srv) build(ctx context.Context, id string) (map[string]any, error) {
	b, err := s.client.Get(ctx, "/v0/builds/"+api.PathEscape(id), nil)
	if err != nil {
		return nil, err
	}
	return b, s.objectAllowed(ctx, b, "build")
}

// waitSeconds is how long a wait tool waits: the default when not asked, 0 to read at once, at most 15 minutes.
func waitSeconds(asked *int, def int) int {
	switch {
	case asked == nil:
		return def
	case *asked < 0:
		return 0
	case *asked > 900:
		return 900
	}
	return *asked
}

func slugify(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.TrimRight(b.String(), "-")
	if len(s) > 63 {
		s = strings.TrimRight(s[:63], "-")
	}
	return s
}

func checkImage(image string) error {
	if image == "" || strings.ContainsAny(image, " \t\n") {
		return fmt.Errorf("image must be a full image reference such as registry.example.com/studio/server:1.4.2")
	}
	if strings.Contains(image, "@sha256:") {
		return nil
	}
	last := image[strings.LastIndex(image, "/")+1:]
	i := strings.LastIndex(last, ":")
	if i < 0 {
		return fmt.Errorf("image %q has no tag: add a version tag (for example :1.4.2) so the build always means the same image", image)
	}
	if last[i+1:] == "latest" {
		return fmt.Errorf("image %q uses the latest tag: use a version tag so the build always means the same image", image)
	}
	return nil
}

// versionBody turns the flat tool input into the API's version body; client.CheckVersion then checks it.
func versionBody(in versionIn) map[string]any {
	ports := []map[string]any{}
	for _, p := range in.Ports {
		port := map[string]any{"name": p.Name, "protocol": p.Protocol}
		if p.ContainerPort > 0 {
			port["container_port"] = p.ContainerPort
		}
		if p.PortPolicy != "" {
			port["port_policy"] = p.PortPolicy
		}
		ports = append(ports, port)
	}
	ready := map[string]any{"type": in.ReadyCheck.Type}
	switch in.ReadyCheck.Type {
	case "log":
		ready["pattern"] = in.ReadyCheck.Pattern
		if in.ReadyCheck.Source != "" {
			ready["source"] = in.ReadyCheck.Source
		}
	case "port":
		ready["port"] = in.ReadyCheck.Port
	case "delay":
		if in.ReadyCheck.After != "" {
			ready["after"] = in.ReadyCheck.After
		}
	}
	stop := map[string]any{}
	setIf(stop, "method", in.StopMethod)
	setIf(stop, "shutdown_time", in.StopGrace)

	launch := map[string]any{"ports": ports, "ready_check": ready}
	setIf(launch, "executable", in.Executable)
	if len(in.Args) > 0 {
		launch["args"] = in.Args
	}
	if len(in.Env) > 0 {
		launch["env"] = in.Env
	}
	if len(stop) > 0 {
		launch["stop"] = stop
	}
	setIf(launch, "boot_timeout", in.BootTimeout)
	if len(in.LogPaths) > 0 {
		launch["log_paths"] = in.LogPaths
	}
	if len(in.ArtifactPaths) > 0 {
		launch["artifact_paths"] = in.ArtifactPaths
	}
	setIf(launch, "crash_dump", in.CrashDump)
	if len(in.Compat) > 0 {
		launch["compat"] = in.Compat
	}
	body := map[string]any{
		"name": in.Name, "build": in.Build,
		"size":   map[string]any{"cpu": in.CPU, "memory_gb": in.MemoryGB},
		"launch": launch,
	}
	setIf(body, "tier", in.Tier)
	if in.Restart != "" {
		body["restart"] = map[string]any{"policy": in.Restart}
	}
	setIf(body, "max_duration", in.MaxDuration)
	if len(in.Regions) > 0 {
		body["regions"] = in.Regions
	}
	return body
}

// uploadScript is a bash script that sends each part with its exact length (storage refuses a streamed upload)
// and prints the parts, with the ETags storage answered, as JSON for complete_windows_upload.
func uploadScript(build map[string]any, zipPath string) (string, error) {
	up := obj(build["upload"])
	partBytes, _ := up["part_bytes"].(float64)
	parts := list(up["parts"])
	if partBytes <= 0 || len(parts) == 0 {
		return "", fmt.Errorf("the API sent no upload links for build %s", str(build["id"]))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "set -euo pipefail\nzip=%s\npart_bytes=%d\n", shellQuote(zipPath), int64(partBytes))
	b.WriteString(`put() { # part number, link
  etag=$(dd if="$zip" bs="$part_bytes" skip=$(($1 - 1)) count=1 status=none |
    curl -sS --fail -X PUT --data-binary @- -H 'Content-Type:' -H 'Expect:' -D - -o /dev/null "$2" |
    tr -d '\r' | sed -n 's/^[Ee][Tt][Aa][Gg]: *//p' | sed 's/"/\\"/g')
  [ -n "$etag" ] || { echo "part $1: storage sent no ETag" >&2; exit 1; }
  printf '{"number": %d, "etag": "%s"}' "$1" "$etag"
}
echo '['
`)
	for i, p := range parts {
		n, _ := p["number"].(float64)
		sep := ","
		if i == len(parts)-1 {
			sep = ""
		}
		fmt.Fprintf(&b, "put %d %s; echo '%s'\n", int(n), shellQuote(str(p["url"])), sep)
	}
	b.WriteString("echo ']'\n")
	return b.String(), nil
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
