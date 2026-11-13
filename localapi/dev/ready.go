package dev

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/survivalservers/studio-go/localapi"
)

// readyCheck is the -ready flag: the version's ready check, tried on this computer.
type readyCheck struct {
	kind  string // local_api, log, port, delay or none
	re    *regexp.Regexp
	port  string
	after time.Duration
}

func parseReady(s string, ports map[string]localapi.Port) (readyCheck, error) {
	kind, arg, _ := strings.Cut(s, ":")
	switch kind {
	case "local_api", "none":
		if arg == "" {
			return readyCheck{kind: kind}, nil
		}
	case "log":
		re, err := regexp.Compile(arg)
		if err != nil || arg == "" {
			return readyCheck{}, fmt.Errorf("log:PATTERN takes a regular expression: %q", arg)
		}
		return readyCheck{kind: kind, re: re}, nil
	case "port":
		p, ok := ports[arg]
		if !ok {
			return readyCheck{}, fmt.Errorf("port:%s names no port of -port-map (default game)", arg)
		}
		if p.Protocol == "udp" && runtime.GOOS != "linux" {
			return readyCheck{}, fmt.Errorf("port:%s is a udp port, and only Linux shows which udp ports a program has open: try log:PATTERN or delay:20s here", arg)
		}
		return readyCheck{kind: kind, port: arg}, nil
	case "delay":
		d, err := time.ParseDuration(arg)
		if err != nil || d <= 0 {
			return readyCheck{}, fmt.Errorf("delay takes a time such as delay:20s, not %q", arg)
		}
		return readyCheck{kind: kind, after: d}, nil
	}
	return readyCheck{}, fmt.Errorf("%q is local_api, log:PATTERN, port:NAME, delay:20s or none", s)
}

// lineWatch reads the program's console, a line at a time, for the log ready check. The program's output and errors
// each keep their own partial line; the first line that matches is sent once.
type lineWatch struct {
	re    *regexp.Regexp
	found chan<- string
	mu    sync.Mutex
	done  bool
	buf   bytes.Buffer
}

// other is a watch for the second stream, sharing the first one's pattern and its one finding.
func (w *lineWatch) other() *lineWatch { return &lineWatch{re: w.re, found: w.found} }

func (w *lineWatch) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.done {
		return len(p), nil
	}
	w.buf.Write(p)
	for {
		line, err := w.buf.ReadBytes('\n')
		if err != nil { // no whole line yet: keep it for the next write
			rest := append([]byte{}, line...)
			w.buf.Reset()
			if len(rest) > 64<<10 { // a "line" this long is not a console line; look at it now
				w.match(rest)
			} else {
				w.buf.Write(rest)
			}
			return len(p), nil
		}
		if w.match(bytes.TrimRight(line, "\r\n")) {
			w.buf.Reset()
			return len(p), nil
		}
	}
}

func (w *lineWatch) match(line []byte) bool {
	if w.done || !w.re.Match(line) {
		return false
	}
	w.done = true
	select {
	case w.found <- fmt.Sprintf("log line %q", truncate(string(line), 120)):
	default: // the other stream found one first
	}
	return true
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// watchPort sends once a program on this computer has the port open: a tcp port listening, or a udp port bound.
func watchPort(p localapi.Port, name string, found chan<- string, done <-chan struct{}) {
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	for {
		if portOpen(p) {
			select {
			case found <- fmt.Sprintf("port %s %d/%s is open", name, p.Port, p.Protocol):
			default:
			}
			return
		}
		select {
		case <-tick.C:
		case <-done:
			return
		}
	}
}

// portOpen reads Linux's socket tables (any address, IPv4 or IPv6). Elsewhere it tries a tcp connection on this
// computer; parseReady refuses a udp port there.
func portOpen(p localapi.Port) bool {
	if runtime.GOOS == "linux" {
		files := map[string][]string{"tcp": {"/proc/net/tcp", "/proc/net/tcp6"}, "udp": {"/proc/net/udp", "/proc/net/udp6"}}[p.Protocol]
		for _, f := range files {
			if procHasPort(f, p.Port, p.Protocol) {
				return true
			}
		}
		return false
	}
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p.Port)), 200*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// procHasPort reports whether one of the table's sockets has the port as its local port: listening for tcp (state
// 0A), bound for udp (state 07).
func procHasPort(path string, port int, proto string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	want := map[string]string{"tcp": "0A", "udp": "07"}[proto]
	hexPort := fmt.Sprintf(":%04X", port)
	sc := bufio.NewScanner(f)
	sc.Scan() // the heading
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) > 3 && strings.HasSuffix(fields[1], hexPort) && fields[3] == want {
			return true
		}
	}
	return false
}
