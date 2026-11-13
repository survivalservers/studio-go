// Package dev runs a game server on your own computer exactly as a hosting machine would: the same GS_* environment
// variables, the allocation file, and the local API on 127.0.0.1, so you can try your integration before uploading a
// build. It prints every call your server makes. The command-line tool runs it:
//
//	survivalservers dev [flags] -- ./MyServer [args...]
//
// A fresh start (the default) is allocated before your server starts: the allocation file is there and
// GET /v0/self/allocation answers at once. With -warm your server starts first, says it is ready, and is allocated when
// you press Enter (or after -allocate-after). Ctrl+C, your server's own POST /v0/self/stop, or -ttl stop it: the
// shutdown notice goes out first, and after -shutdown-time the program is ended.
package dev

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/survivalservers/studio-go/localapi"
)

// Interrupts is what Run takes as stop for a run from a terminal: Ctrl+C.
func Interrupts() <-chan os.Signal {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	return ch
}

type multi []string

func (m *multi) String() string     { return strings.Join(*m, ",") }
func (m *multi) Set(v string) error { *m = append(*m, v); return nil }

func pairs(list []string) (map[string]string, error) {
	out := map[string]string{}
	for _, kv := range list {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("%q is not NAME=value", kv)
		}
		out[k] = v
	}
	return out, nil
}

// Run runs one game server as a hosting machine would, from the command line's arguments (after "dev"), until it
// exits; a signal on stop stops it as an API stop would. It returns the exit code for the command.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer, stop <-chan os.Signal) int {
	fs := flag.NewFlagSet("survivalservers dev", flag.ContinueOnError)
	fs.SetOutput(stderr)
	port := fs.Int("port", 7600, "port of the local API on 127.0.0.1 (0 picks a free one)")
	kind := fs.String("kind", "match", "match or persistent_server (world, its older name, is passed on as world to try an older build)")
	warm := fs.Bool("warm", false, "start first, allocate after the server is ready (as a ready server)")
	allocAfter := fs.Duration("allocate-after", 0, "with -warm: allocate this long after ready instead of waiting for Enter")
	ready := fs.String("ready", "local_api", "when the server counts as ready, as the version's ready check: local_api (it calls POST /v0/self/ready), log:PATTERN (a console line matches the regular expression), port:NAME (the program has opened that named port), delay:20s, or none")
	cpu := fs.Float64("cpu", 1, "vCPUs, as the version's size (GS_CPU)")
	memoryMB := fs.Int("memory-mb", 2048, "memory in MB, as the version's size (GS_MEMORY_MB)")
	grace := fs.Duration("shutdown-time", 30*time.Second, "shutdown time: from the shutdown notice to ending the program")
	stopMethod := fs.String("stop", defaultStop, "how a stop reaches the server besides the shutdown notice: "+stopMethods)
	ttl := fs.Duration("ttl", 0, "stop after this long (0: no limit)")
	dir := fs.String("dir", ".", "the server's folder (the allocation file goes in <dir>/.gs)")
	externalID := fs.String("external-id", "", "your own ID for the match (allocation data)")
	var envs, startArgs, labels, ports multi
	fs.Var(&envs, "env", "NAME=value from the start request (repeatable); in the allocation data, and in the environment on a fresh start")
	fs.Var(&startArgs, "arg", "an argument from the start request (repeatable); in the allocation data, and on the command line on a fresh start")
	fs.Var(&labels, "label", "NAME=value label (repeatable)")
	fs.Var(&ports, "port-map", "NAME=PORT/PROTOCOL for a named port (repeatable; default game=7777/udp)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	program := fs.Args()
	if len(program) == 0 {
		fmt.Fprintln(stderr, "usage: survivalservers dev [flags] -- ./MyServer [args...]")
		return 2
	}
	env, err := pairs(envs)
	if err == nil {
		_, err = pairs(labels)
	}
	if err != nil {
		fmt.Fprintln(stderr, "survivalservers dev:", err)
		return 2
	}
	labelMap, _ := pairs(labels)
	for name := range env {
		if localapi.IsReserved(name) {
			fmt.Fprintf(stderr, "survivalservers dev: %s: names starting with GS_ are reserved\n", name)
			return 2
		}
	}
	if !validStop(*stopMethod) {
		fmt.Fprintf(stderr, "survivalservers dev: -stop is one of %s\n", stopMethods)
		return 2
	}
	if !localapi.ValidKind(*kind) {
		fmt.Fprintln(stderr, "survivalservers dev: -kind is match or persistent_server (or world)")
		return 2
	}
	portMap := map[string]localapi.Port{"game": {Port: 7777, Protocol: "udp"}}
	if len(ports) > 0 {
		portMap = map[string]localapi.Port{}
		for _, p := range ports {
			name, spec, ok := strings.Cut(p, "=")
			num, proto, _ := strings.Cut(spec, "/")
			n, err := strconv.Atoi(num)
			if proto == "" {
				proto = "udp"
			}
			if !ok || err != nil || n < 1 || n > 65535 || (proto != "udp" && proto != "tcp") {
				fmt.Fprintf(stderr, "survivalservers dev: -port-map %q is not NAME=PORT/udp or NAME=PORT/tcp\n", p)
				return 2
			}
			portMap[name] = localapi.Port{Port: n, Protocol: proto}
		}
	}

	if *cpu <= 0 || *memoryMB <= 0 {
		fmt.Fprintln(stderr, "survivalservers dev: -cpu and -memory-mb are above 0")
		return 2
	}
	check, err := parseReady(*ready, portMap)
	if err != nil {
		fmt.Fprintln(stderr, "survivalservers dev: -ready:", err)
		return 2
	}

	say := newPrinter(stdout)
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		fmt.Fprintln(stderr, "survivalservers dev: local API:", err)
		return 1
	}
	events := make(chan localapi.Event, 64)
	srv := localapi.NewServer(localapi.Options{OnEvent: func(e localapi.Event) { events <- e }})
	hs := srv.NewHTTPServer(ln.Addr().String())
	go hs.Serve(ln)
	defer hs.Close()

	id := "gs_dev" + random(10)
	token := random(32)
	self := localapi.Self{ID: id, Kind: *kind, App: "dev", Version: "dev", Region: "local", Continent: "local",
		PublicIP: "127.0.0.1", Ports: portMap, Labels: labelMap}
	inst, err := srv.Register(localapi.Registration{Token: token, Self: self, Warm: *warm})
	if err != nil {
		fmt.Fprintln(stderr, "survivalservers dev:", err)
		return 1
	}
	absDir, _ := filepath.Abs(*dir)
	gsDir := filepath.Join(absDir, ".gs")
	for _, d := range []string{"logs", "artifacts"} {
		os.MkdirAll(filepath.Join(gsDir, d), 0o755)
	}
	allocFile := filepath.Join(gsDir, "allocation.json")
	os.Remove(allocFile)
	allocation := func() localapi.Allocation {
		a := localapi.Allocation{GameServerID: id, AllocatedAt: time.Now().UTC(), ExternalID: *externalID, Labels: labelMap,
			Env: env, Args: append([]string{}, startArgs...)}
		if *ttl > 0 {
			exp := time.Now().Add(*ttl)
			a.ExpiresAt = &exp
		}
		return a
	}
	allocate := func() {
		a := allocation()
		if err := localapi.WriteAllocationFile(allocFile, a); err != nil {
			say.line("could not write the allocation file: %v", err)
		}
		inst.Allocate(a)
		b, _ := json.Marshal(a)
		say.line("allocated: %s", b)
	}

	// The environment a hosting machine gives, over this computer's own.
	gsEnv := localapi.Env(localapi.EnvInput{Self: self, CPU: *cpu, MemoryMB: *memoryMB, LocalAPIURL: "http://" + ln.Addr().String(),
		Token: token, AllocationFile: allocFile, LogDir: filepath.Join(gsDir, "logs"), ArtifactDir: filepath.Join(gsDir, "artifacts")})
	procEnv := os.Environ()
	argv := program[1:]
	if !*warm {
		allocate() // a fresh start is allocated before the program starts
		for k, v := range env {
			procEnv = append(procEnv, k+"="+v)
		}
		argv = append(argv, startArgs...)
	}
	procEnv = append(procEnv, gsEnv...)

	cmd := exec.Command(program[0], argv...)
	cmd.Dir = absDir
	cmd.Env = procEnv
	cmd.Stdout, cmd.Stderr = stdout, stderr
	readyC := make(chan string, 1) // the ready check's own finding: log, port or delay
	if check.kind == "log" {
		w := &lineWatch{re: check.re, found: readyC}
		cmd.Stdout, cmd.Stderr = io.MultiWriter(stdout, w), io.MultiWriter(stderr, w.other())
	}
	prepareCommand(cmd, *stopMethod)
	say.line("starting %s as %s (local API http://%s, %s start)", strings.Join(program, " "), id, ln.Addr(), map[bool]string{true: "ready-server", false: "fresh"}[*warm])
	for _, kv := range gsEnv {
		if !strings.HasPrefix(kv, "GS_TOKEN=") {
			say.line("  %s", kv)
		}
	}
	started := time.Now()
	if err := cmd.Start(); err != nil {
		fmt.Fprintln(stderr, "survivalservers dev:", err)
		return 1
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	watchDone := make(chan struct{})
	defer close(watchDone)
	switch check.kind {
	case "none":
		readyC <- "no ready check"
	case "delay":
		go func() {
			select {
			case <-time.After(check.after):
				readyC <- fmt.Sprintf("delay %v", check.after)
			case <-watchDone:
			}
		}()
	case "port":
		go watchPort(portMap[check.port], check.port, readyC, watchDone)
	}

	var ttlC <-chan time.Time
	if *ttl > 0 {
		ttlC = time.After(*ttl)
	}
	enter := make(chan struct{}, 1)
	if *warm && *allocAfter == 0 {
		go func() {
			bufio.NewReader(stdin).ReadString('\n')
			enter <- struct{}{}
		}()
	}
	var allocTimer <-chan time.Time
	var killC, signalC <-chan time.Time
	stopping, isReady := false, false
	markReady := func(how string) {
		if isReady || stopping {
			return
		}
		isReady = true
		inst.MarkReady()
		if how != "" {
			how = " (" + how + ")"
		}
		say.line("ready after %v%s", time.Since(started).Round(time.Millisecond), how)
		if *warm {
			if *allocAfter > 0 {
				allocTimer = time.After(*allocAfter)
				say.line("allocating in %v", *allocAfter)
			} else {
				say.line("press Enter to allocate")
			}
		}
	}
	stopNow := func(reason string) {
		if stopping {
			return
		}
		stopping = true
		deadline := time.Now().Add(*grace)
		inst.RequestShutdown(localapi.Shutdown{Deadline: deadline, Reason: reason})
		say.line("stop requested (%s): shutdown notice sent, the program is ended in %v if still running", reason, *grace)
		// As on a hosting machine: a program that waits for the shutdown notice exits on it, and its stop signal goes
		// only if it still runs halfway through its shutdown time (a signal on top of its own exit can cut it short).
		if (*stopMethod == "sigterm" || *stopMethod == "sigint" || *stopMethod == "ctrl_break") && inst.WatchesShutdown() {
			say.line("it waits for the shutdown notice: the %s stop signal goes only if it still runs in %v", *stopMethod, *grace/2)
			signalC = time.After(*grace / 2)
		} else if err := signalStop(cmd, *stopMethod); err != nil {
			say.line("stop signal: %v", err)
		}
		killC = time.After(*grace)
	}
	for {
		select {
		case e := <-events:
			switch e.Type {
			case localapi.EventReady:
				if check.kind == "local_api" {
					markReady("")
				}
			case localapi.EventStop:
				say.line("the server asked to stop: %q", e.Reason)
				stopNow(localapi.ReasonSelf)
			default:
				say.event(e)
			}
		case how := <-readyC:
			markReady(how)
		case <-allocTimer:
			allocate()
		case <-enter:
			if s := inst.Self(); s.Status == localapi.StatusWarm {
				allocate()
			} else {
				say.line("not ready yet: allocate once the server has called POST /v0/self/ready")
				go func() { bufio.NewReader(stdin).ReadString('\n'); enter <- struct{}{} }()
			}
		case <-ttlC:
			stopNow(localapi.ReasonTTL)
		case <-stop:
			stopNow(localapi.ReasonAPI)
		case <-signalC:
			say.line("still running halfway through the shutdown time: sending the %s stop signal", *stopMethod)
			if err := signalStop(cmd, *stopMethod); err != nil {
				say.line("stop signal: %v", err)
			}
		case <-killC:
			say.line("shutdown time over: ending the program")
			if cmd.Process != nil {
				cmd.Process.Kill()
			}
		case err := <-exited:
			code := 0
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				code = exitCode(ee)
			} else if err != nil {
				code = 1
			}
			how := "on its own"
			if stopping {
				how = "after the stop request"
			}
			say.line("the server exited %s with code %d after %v", how, code, time.Since(started).Round(time.Millisecond))
			if code != 0 && !stopping {
				say.line("a hosting machine would report this run as crashed (and keep its crash dump)")
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			hs.Shutdown(ctx)
			cancel()
			return code
		}
	}
}

func random(n int) string {
	b := make([]byte, n/2+1)
	rand.Read(b)
	return hex.EncodeToString(b)[:n]
}

// printer writes the dev command's own lines, marked so they stand out from the server's console.
type printer struct {
	mu sync.Mutex
	w  io.Writer
}

func newPrinter(w io.Writer) *printer { return &printer{w: w} }

func (p *printer) line(format string, args ...any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fmt.Fprintf(p.w, "[dev %s] %s\n", time.Now().Format("15:04:05.000"), fmt.Sprintf(format, args...))
}

func (p *printer) event(e localapi.Event) {
	switch e.Type {
	case localapi.EventHealth:
		p.line("health")
	case localapi.EventPlayers:
		p.line("players: %d %v", e.Players.Count, e.Players.IDs)
	case localapi.EventLabels:
		keys := make([]string, 0, len(e.Labels))
		for k, v := range e.Labels {
			keys = append(keys, k+"="+v)
		}
		sort.Strings(keys)
		p.line("labels: %s", strings.Join(keys, " "))
	case localapi.EventTick:
		p50 := ""
		if e.Tick.P50ms != nil {
			p50 = fmt.Sprintf(" p50 %.2f ms", *e.Tick.P50ms)
		}
		p.line("tick: p99 %.2f ms%s at %d Hz", e.Tick.P99ms, p50, e.Tick.Hz)
	default:
		p.line("%s", e.Type)
	}
}
