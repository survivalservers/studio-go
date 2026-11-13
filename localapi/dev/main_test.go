package dev

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/survivalservers/studio-go/localapi"
)

// The test binary is also the game server (DEVAGENT_TEST_GAME picks how it behaves).
func TestMain(m *testing.M) {
	if mode := os.Getenv("DEVAGENT_TEST_GAME"); mode != "" {
		game(mode)
		return
	}
	os.Exit(m.Run())
}

func game(mode string) {
	url, tok := os.Getenv(localapi.EnvLocalAPIURL), os.Getenv(localapi.EnvToken)
	call := func(method, path, body string) (int, []byte) {
		req, _ := http.NewRequest(method, url+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			fmt.Println("call failed:", err)
			os.Exit(9)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, b
	}
	switch mode {
	case "cold":
		term := make(chan os.Signal, 1)
		signal.Notify(term, syscall.SIGTERM) // a well-behaved server ends cleanly on the stop signal
		go func() { <-term; fmt.Println("sigterm"); os.Exit(0) }()
		_, err := os.Stat(os.Getenv(localapi.EnvAllocationFile))
		_, b := call("GET", "/v0/self/allocation", "")
		var a localapi.Allocation
		json.Unmarshal(b, &a)
		fmt.Printf("file=%v env=%s alloc=%s args=%v\n", err == nil, os.Getenv("MATCH_MODE"), a.Env["MATCH_MODE"], os.Args[1:])
		call("POST", "/v0/self/ready", "")
		call("PUT", "/v0/self/players", `{"count":2}`)
		call("POST", "/v0/self/tick", `{"p99_ms":3.9,"hz":60}`)
		call("POST", "/v0/self/stop", `{"reason":"match_over"}`)
		for {
			if code, _ := call("GET", "/v0/self/shutdown?wait=5s", ""); code == 200 {
				os.Exit(0)
			}
		}
	case "warm":
		call("POST", "/v0/self/ready", "")
		for {
			if code, b := call("GET", "/v0/self/allocation?wait=5s", ""); code == 200 {
				var a localapi.Allocation
				json.Unmarshal(b, &a)
				fmt.Printf("got match %s\n", a.ExternalID)
				break
			}
		}
		signal.Ignore(os.Interrupt) // the shutdown notice is what it listens to
		for {
			if code, _ := call("GET", "/v0/self/shutdown?wait=5s", ""); code == 200 {
				fmt.Println("shutting down")
				os.Exit(0)
			}
		}
	case "self": // prints what it is told it is, as a game built before or after the rename reads it
		term := make(chan os.Signal, 1)
		signal.Notify(term, syscall.SIGTERM)
		go func() { <-term; os.Exit(0) }()
		_, b := call("GET", "/v0/self", "")
		var self localapi.Self
		json.Unmarshal(b, &self)
		fmt.Printf("self kind=%s env kind=%s persistent=%v region=%s\n", self.Kind, os.Getenv(localapi.EnvKind),
			localapi.IsPersistentServer(self.Kind), self.Region)
		call("POST", "/v0/self/ready", "")
		call("POST", "/v0/self/stop", `{"reason":"done"}`)
		for {
			if code, _ := call("GET", "/v0/self/shutdown?wait=5s", ""); code == 200 {
				os.Exit(0)
			}
		}
	case "listens": // waits for the notice from the start, as the SDKs do, and exits on it; says if a signal comes
		term := make(chan os.Signal, 1)
		signal.Notify(term, syscall.SIGTERM)
		go func() { <-term; fmt.Println("got sigterm") }()
		call("POST", "/v0/self/ready", "")
		call("GET", "/v0/self/shutdown?wait=0s", "")
		call("POST", "/v0/self/stop", `{"reason":"match_over"}`)
		for {
			if code, _ := call("GET", "/v0/self/shutdown?wait=5s", ""); code == 200 {
				time.Sleep(300 * time.Millisecond) // saves, then leaves
				fmt.Println("saved and leaving")
				os.Exit(0)
			}
		}
	case "logline", "opens": // never calls ready: says it is up on the console, or opens its game port, then waits for the notice
		signal.Ignore(os.Interrupt, syscall.SIGTERM)
		if mode == "opens" {
			port := os.Getenv("GS_PORT_GAME")
			if os.Getenv("OPEN_PROTO") == "udp" {
				c, err := net.ListenPacket("udp", ":"+port)
				if err != nil {
					fmt.Println("could not open the port:", err)
					os.Exit(8)
				}
				defer c.Close()
			} else {
				l, err := net.Listen("tcp", ":"+port)
				if err != nil {
					fmt.Println("could not open the port:", err)
					os.Exit(8)
				}
				defer l.Close()
			}
		} else {
			time.Sleep(100 * time.Millisecond)
			fmt.Print("Loading map\nServer is up on port ")
			time.Sleep(50 * time.Millisecond)
			fmt.Println("7777")
		}
		for {
			if code, _ := call("GET", "/v0/self/shutdown?wait=5s", ""); code == 200 {
				fmt.Println("leaving on the notice")
				return
			}
		}
	case "crash":
		call("POST", "/v0/self/ready", "")
		os.Exit(3)
	case "takes-sigterm": // asks to stop and leaves the stop signal to the system, as most servers do
		call("POST", "/v0/self/ready", "")
		call("POST", "/v0/self/stop", `{"reason":"done"}`)
		time.Sleep(30 * time.Second)
	case "stubborn":
		signal.Ignore(os.Interrupt, syscall.SIGTERM)
		call("POST", "/v0/self/ready", "")
		call("POST", "/v0/self/stop", `{"reason":"done"}`)
		time.Sleep(30 * time.Second)
	}
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func start(t *testing.T, mode string, flags ...string) (*syncBuf, chan os.Signal, <-chan int) {
	t.Helper()
	t.Setenv("DEVAGENT_TEST_GAME", mode)
	self, _ := os.Executable()
	out := &syncBuf{}
	stop := make(chan os.Signal, 1)
	done := make(chan int, 1)
	args := append([]string{"-port", "0", "-dir", t.TempDir()}, flags...)
	args = append(args, "--", self)
	go func() { done <- Run(args, strings.NewReader(""), out, out, stop) }()
	return out, stop, done
}

func wait(t *testing.T, done <-chan int, out *syncBuf) int {
	t.Helper()
	select {
	case code := <-done:
		return code
	case <-time.After(20 * time.Second):
		t.Fatalf("the dev command did not finish:\n%s", out)
		return -1
	}
}

func TestColdStart(t *testing.T) {
	out, _, done := start(t, "cold", "-env", "MATCH_MODE=ranked", "-arg", "-MaxPlayers=10")
	if code := wait(t, done, out); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	s := out.String()
	for _, w := range []string{"file=true env=ranked alloc=ranked args=[-MaxPlayers=10]", "ready after", "players: 2",
		"tick: p99 3.90 ms at 60 Hz", `the server asked to stop: "match_over"`, "exited after the stop request with code 0", "GS_ID=gs_dev"} {
		if !strings.Contains(s, w) {
			t.Errorf("output lacks %q:\n%s", w, s)
		}
	}
	if strings.Contains(s, "GS_TOKEN=") {
		t.Error("the token was printed")
	}
}

func TestWarmStartAndCtrlC(t *testing.T) {
	out, stop, done := start(t, "warm", "-warm", "-allocate-after", "100ms", "-external-id", "m1", "-stop", "local_api")
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(out.String(), "got match m1") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	stop <- os.Interrupt
	if code := wait(t, done, out); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if s := out.String(); !strings.Contains(s, "allocating in 100ms") || !strings.Contains(s, "shutting down") {
		t.Fatalf("output:\n%s", s)
	}
}

// -kind world (the older name) is accepted and passed on unchanged, so a build made before the rename can be tried.
func TestKindIsPassedOnAsGiven(t *testing.T) {
	for kind, persistent := range map[string]bool{"world": true, "persistent_server": true, "match": false} {
		out, _, done := start(t, "self", "-kind", kind)
		if code := wait(t, done, out); code != 0 {
			t.Fatalf("-kind %s: exit %d:\n%s", kind, code, out)
		}
		want := fmt.Sprintf("self kind=%s env kind=%s persistent=%v region=local", kind, kind, persistent)
		if s := out.String(); !strings.Contains(s, want) || !strings.Contains(s, "GS_KIND="+kind) {
			t.Errorf("-kind %s: output lacks %q:\n%s", kind, want, s)
		}
	}
}

// A server that waits for the shutdown notice exits on it; the sigterm stop signal is held back (as on a hosting
// machine), so it never lands on top of the server's own exit.
func TestAServerOnTheNoticeGetsNoSignalOnTop(t *testing.T) {
	out, _, done := start(t, "listens", "-stop", "sigterm", "-shutdown-time", "4s")
	if code := wait(t, done, out); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	s := out.String()
	if !strings.Contains(s, "it waits for the shutdown notice: the sigterm stop signal goes only if it still runs in 2s") ||
		!strings.Contains(s, "saved and leaving") || strings.Contains(s, "got sigterm") {
		t.Fatalf("output:\n%s", s)
	}
}

func TestCrash(t *testing.T) {
	out, _, done := start(t, "crash")
	if code := wait(t, done, out); code != 3 || !strings.Contains(out.String(), "crashed") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}

func TestGracePeriodEndsAStubbornServer(t *testing.T) {
	out, _, done := start(t, "stubborn", "-shutdown-time", "300ms")
	code := wait(t, done, out)
	if code == 0 || !strings.Contains(out.String(), "shutdown time over") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}

// The version's other ready checks can be tried: a console line, a port the program opens, or a delay. Each is
// ready once, says how, and a ready server is allocated as with the local API.
func TestReadyChecks(t *testing.T) {
	free := func(proto string) string {
		if proto == "udp" {
			c, err := net.ListenPacket("udp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			return strconv.Itoa(c.LocalAddr().(*net.UDPAddr).Port)
		}
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		return strconv.Itoa(l.Addr().(*net.TCPAddr).Port)
	}
	tcp := free("tcp")
	cases := []struct {
		name, mode string
		flags      []string
		want       string
	}{
		{"log", "logline", []string{"-ready", `log:up on port \d+$`}, `(log line "Server is up on port 7777")`},
		{"delay", "logline", []string{"-ready", "delay:300ms"}, "(delay 300ms)"},
		{"tcp port", "opens", []string{"-ready", "port:game", "-port-map", "game=" + tcp + "/tcp"}, "(port game " + tcp + "/tcp is open)"},
	}
	if runtime.GOOS == "linux" {
		udp := free("udp")
		cases = append(cases, struct {
			name, mode string
			flags      []string
			want       string
		}{"udp port", "opens", []string{"-ready", "port:game", "-port-map", "game=" + udp + "/udp"}, "(port game " + udp + "/udp is open)"})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.name == "udp port" {
				t.Setenv("OPEN_PROTO", "udp")
			}
			flags := append([]string{"-warm", "-allocate-after", "50ms", "-stop", "local_api", "-shutdown-time", "5s"}, c.flags...)
			out, stop, done := start(t, c.mode, flags...)
			deadline := time.Now().Add(10 * time.Second)
			for !strings.Contains(out.String(), "allocated:") && time.Now().Before(deadline) {
				time.Sleep(20 * time.Millisecond)
			}
			stop <- os.Interrupt
			if code := wait(t, done, out); code != 0 {
				t.Fatalf("exit %d:\n%s", code, out)
			}
			s := out.String()
			if !strings.Contains(s, "ready after") || !strings.Contains(s, c.want) || !strings.Contains(s, "allocated:") ||
				!strings.Contains(s, "leaving on the notice") || strings.Count(s, "ready after") != 1 {
				t.Fatalf("output lacks %q, or was ready more than once:\n%s", c.want, s)
			}
		})
	}
}

// The size a hosting machine gives can be set, as the version's.
func TestSize(t *testing.T) {
	out, _, done := start(t, "self", "-cpu", "2.5", "-memory-mb", "6144")
	if code := wait(t, done, out); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if s := out.String(); !strings.Contains(s, "GS_CPU=2.5") || !strings.Contains(s, "GS_MEMORY_MB=6144") {
		t.Fatalf("output:\n%s", s)
	}
}

func TestBadFlags(t *testing.T) {
	for _, args := range [][]string{{}, {"-env", "GS_X=1", "--", "x"}, {"-kind", "lobby", "--", "x"}, {"-port-map", "game=x", "--", "x"}, {"-stop", "nope", "--", "x"},
		{"-ready", "nope", "--", "x"}, {"-ready", "log:(", "--", "x"}, {"-ready", "log:", "--", "x"}, {"-ready", "port:query", "--", "x"},
		{"-ready", "delay:0s", "--", "x"}, {"-ready", "none:x", "--", "x"}, {"-cpu", "0", "--", "x"}, {"-memory-mb", "-1", "--", "x"}} {
		var b bytes.Buffer
		if code := Run(args, strings.NewReader(""), &b, &b, nil); code != 2 {
			t.Errorf("%v: exit %d", args, code)
		}
	}
}
