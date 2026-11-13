package client

import (
	"fmt"
	"math"
	"path"
	"regexp"
	"slices"
	"strings"
)

var (
	portName     = regexp.MustCompile(`^[a-z][a-z0-9_]{0,30}$`)
	portTemplate = regexp.MustCompile(`\{\{port\.([a-z0-9_]+)(\.internal)?\}\}`)
	// Stop methods each system understands.
	StopMethods = map[string][]string{
		"windows": {"ctrl_break", "ctrl_c", "wm_close", "local_api", "kill"},
		"linux":   {"sigterm", "sigint", "local_api", "kill"},
	}
)

// CheckVersion looks at a version body (as POST /v0/apps/{app}/versions takes it) before it is sent, with the app
// and the build it names as the API returned them, and lists what the API would refuse or what would not run, in
// words a person or an agent can act on. Nothing is listed when it looks right.
func CheckVersion(body, app, build map[string]any) []string {
	var problems []string
	add := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }
	osName := str(app["os"])
	tier := firstNonEmpty(str(body["tier"]), str(app["tier"]), "standard")
	buildID := str(body["build"])

	if buildID == "" {
		add("give the build: the ID of a ready build of this app")
	} else if build != nil {
		if b := str(build["app"]); b != "" && b != str(app["id"]) {
			add("build %s belongs to another app; use a build of %s", buildID, str(app["slug"]))
		}
		switch st := str(build["status"]); st {
		case "ready":
		case "awaiting_upload", "processing":
			add("build %s is still %s: wait for it to be ready first", buildID, st)
		default:
			add("build %s is %s; use a ready build", buildID, st)
		}
	}

	// Size: standard in quarter vCPUs, high-frequency whole vCPUs, memory 0.5 to 4 GB per vCPU in quarter GBs.
	size := asMap(body["size"])
	cpu, mem := num(size["cpu"]), num(size["memory_gb"])
	switch {
	case cpu <= 0:
		add("size.cpu must be more than 0")
	case tier == "high-frequency" && cpu != math.Trunc(cpu):
		add("high-frequency sizes are whole vCPUs; %g is not", cpu)
	case math.Mod(cpu*4, 1) != 0:
		add("standard sizes go in 0.25 vCPU steps; %g is not one", cpu)
	}
	if mem <= 0 || math.Mod(mem*4, 1) != 0 {
		add("size.memory_gb must be a positive number in 0.25 GB steps")
	} else if cpu > 0 && (mem < 0.5*cpu || mem > 4*cpu) {
		add("memory must be 0.5 to 4 GB per vCPU: %g to %g GB for %g vCPUs", 0.5*cpu, 4*cpu, cpu)
	}

	launch := asMap(body["launch"])
	// Ports, and the launch arguments that name them.
	names := map[string]bool{}
	ports := asList(launch["ports"])
	if len(ports) == 0 {
		add(`add at least one port, for example {"name": "game", "protocol": "udp"}`)
	}
	for _, p := range ports {
		name := str(p["name"])
		if !portName.MatchString(name) {
			add("port name %q must start with a lowercase letter and use only a-z, 0-9 and _", name)
		}
		if names[name] {
			add("port %q is named twice", name)
		}
		names[name] = true
		if proto := str(p["protocol"]); proto != "udp" && proto != "tcp" {
			add("port %q: protocol must be udp or tcp", name)
		}
		pp := str(p["port_policy"])
		switch pp {
		case "", "dynamic", "none":
		case "passthrough":
			if osName == "windows" {
				add("port %q: port_policy passthrough is for Linux images; a Windows server already listens on its public port, GS_PORT_%s or {{port.%s}}", name, strings.ToUpper(name), name)
			}
		default:
			add("port %q: port_policy must be dynamic (a public port), none (an admin port, never open to the internet) or passthrough (Linux: the game listens on its public port number itself)", name)
		}
		if num(p["container_port"]) > 0 && osName == "windows" {
			add("port %q: container_port is for Linux images; a Windows server reads its port from GS_PORT_%s or {{port.%s}}", name, strings.ToUpper(name), name)
		}
		if num(p["container_port"]) > 0 && pp == "passthrough" && osName != "windows" {
			add("port %q: a passthrough port has no container_port: the game listens on its public number, GS_PORT_%s or {{port.%s}}", name, strings.ToUpper(name), name)
		}
	}
	for _, a := range asStrings(launch["args"]) {
		for _, m := range portTemplate.FindAllStringSubmatch(a, -1) {
			if !names[m[1]] {
				add("argument %q names port %q, which is not in ports", a, m[1])
			}
			if m[2] != "" && osName == "windows" {
				add("argument %q: .internal ports are for Linux images", a)
			}
		}
	}
	for k := range asMap(launch["env"]) {
		if strings.HasPrefix(k, "GS_") {
			add("environment variable %s: names starting with GS_ are ours", k)
		}
	}

	// The program to start: a path inside the zip on Windows, matched as the API matches it: backslashes read as
	// slashes, letter case exact. Before, a path written the Windows way (Binaries\Win64\Server.exe) was refused
	// here though the API takes it, and one in the wrong case passed here and was refused there.
	// Then the API's three refusals, in its own words and its order: a name that is not a
	// plain path inside the build, a build that lists no program (checked only when the build is known and ready), and
	// anything but an .exe (a script never starts).
	if osName == "windows" {
		exe := strings.ReplaceAll(str(launch["executable"]), `\`, "/")
		exes := asStrings(build["executables"])
		switch {
		case exe == "":
			add("Windows versions need launch.executable: the program's path inside the zip%s", listed(exes))
		case !localProgramName(exe):
			add("launch.executable: %s", programNotLocal)
		case build != nil && str(build["status"]) == "ready" && len(exes) == 0:
			add("launch.executable: %s", programNoneInBuild)
		case !strings.EqualFold(path.Ext(exe), ".exe"):
			add("launch.executable: %s", programNotExe)
		case len(exes) > 0 && !slices.Contains(exes, exe):
			if i := slices.IndexFunc(exes, func(e string) bool { return strings.EqualFold(e, exe) }); i >= 0 {
				add("executable %q is not in the zip: letter case counts, and the build has %q", str(launch["executable"]), exes[i])
			} else {
				add("executable %q is not in the zip%s", str(launch["executable"]), listed(exes))
			}
		}
	}

	// Ready check.
	ready := asMap(launch["ready_check"])
	switch t := str(ready["type"]); t {
	case "log":
		if p := str(ready["pattern"]); p == "" {
			add("a log ready check needs pattern: the log text that means the server is ready")
		} else if _, err := regexp.Compile(p); err != nil {
			add("the ready check's pattern is not a valid regular expression: %v", err)
		}
	case "port":
		if !names[str(ready["port"])] {
			add("a port ready check needs port: one of the ports' names, not %q", str(ready["port"]))
		}
	case "delay":
		if ready["after"] == nil {
			add("a delay ready check needs after, for example 20s")
		}
	case "local_api", "none", "":
	default:
		add("ready_check.type %q is not one of local_api, log, port, delay, none", t)
	}

	// Stop method for the system.
	if m := str(asMap(launch["stop"])["method"]); m != "" && StopMethods[osName] != nil && !slices.Contains(StopMethods[osName], m) {
		other := "Windows"
		if osName == "windows" {
			other = "Linux"
		}
		add("stop method %q is for %s; %s uses one of %s", m, other, osTitle(osName), strings.Join(StopMethods[osName], ", "))
	}
	return problems
}

// The API's words for a Windows program it refuses, word for word, so a studio reads one message whether the
// command-line tool, the AI tool or the API says it. A SECOND COPY: the first is in the API's own check of a
// version; a change there must be made here too.
const (
	programNotLocal    = "a path inside the build as the zip names it, such as WindowsServer/Game.exe: no drive letter, no leading slash, no . or .. part"
	programNoneInBuild = "this build has no program to start; upload a build that has the game server's own .exe"
	programNotExe      = "name the game server's own .exe: a Windows version starts an .exe only, never a script (a .bat, .cmd, .ps1 " +
		"or other script runs through a host such as cmd.exe, which a stop leaves waiting at its own prompt and a crash leaves without a dump)"
)

// localProgramName says whether p (forward slashes) is a plain relative path that stays inside the build, as the
// API's localName does (control/internal/api/versions.go, #1055; the second copy of it).
func localProgramName(p string) bool {
	switch {
	case p == "" || p == "." || p == ".." || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "../"):
		return false
	case path.Clean(p) != p:
		return false
	case strings.ContainsRune(p, ':'):
		return false
	case strings.IndexFunc(p, func(r rune) bool { return r < 0x20 }) >= 0:
		return false
	}
	return true
}

func osTitle(os string) string {
	if os == "windows" {
		return "Windows"
	}
	return "Linux"
}

func listed(exes []string) string {
	if len(exes) == 0 {
		return ""
	}
	if len(exes) > 10 {
		exes = exes[:10]
	}
	return " (programs in this build: " + strings.Join(exes, ", ") + ")"
}

func str(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

func num(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	}
	return 0
}

func asMap(v any) map[string]any {
	switch m := v.(type) {
	case map[string]any:
		return m
	case map[string]string:
		out := make(map[string]any, len(m))
		for k, v := range m {
			out[k] = v
		}
		return out
	}
	return map[string]any{}
}

func asList(v any) []map[string]any {
	switch l := v.(type) {
	case []map[string]any:
		return l
	case []any:
		out := make([]map[string]any, 0, len(l))
		for _, it := range l {
			if m, ok := it.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

func asStrings(v any) []string {
	switch l := v.(type) {
	case []string:
		return l
	case []any:
		out := make([]string, 0, len(l))
		for _, it := range l {
			out = append(out, str(it))
		}
		return out
	}
	return nil
}
