// gsbench measures how well a machine runs fixed-rate game servers and how well a network path carries them.
//
//	gsbench tick       update-loop test: how many game servers fit at 64 and 128 updates a second
//	gsbench udp-echo   echo server for the network test
//	gsbench udp        network test: round trip, jitter, loss and reordering at a game's packet rate
//	gsbench sysinfo    what machine this is
//
// Results print as JSON on stdout (or to -out); a short summary goes to stderr.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/survivalservers/studio-go/bench/tick"
	"github.com/survivalservers/studio-go/bench/udp"
)

const version = "0.2.1" // 0.2.1: the ramp ends at 2x CPUs; 0.2.0: timings from the fine clock; 0.1.0 Windows results are not comparable

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	var err error
	switch os.Args[1] {
	case "tick":
		err = cmdTick(ctx, os.Args[2:])
	case "udp-echo":
		err = cmdEcho(ctx, os.Args[2:])
	case "udp":
		err = cmdUDP(ctx, os.Args[2:])
	case "sysinfo":
		err = emit("", sysinfo())
	case "version":
		fmt.Println(version)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "gsbench:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: gsbench tick|udp-echo|udp|sysinfo|version [flags]  (gsbench <command> -h for flags)")
}

// TickReport is the whole output of a tick run: the machine, then one result per rate and instance count.
type TickReport struct {
	Kit     string            `json:"kit"`
	Machine map[string]string `json:"machine"`
	Started time.Time         `json:"started"`
	Runs    []tick.Result     `json:"runs"`
	Fits    map[string]int    `json:"fits"` // per rate: the most instances that passed
}

func cmdTick(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("tick", flag.ExitOnError)
	hzList := fs.String("hz", "64,128", "update rates to test, comma separated")
	instList := fs.String("instances", "1", `instance counts to test, comma separated, or "ramp" (1, 2, 4 ... then 2x CPUs, stopping at the first that does not fit)`)
	work := fs.Int("work", 400, "work units per update (fixed work; a slower machine takes longer)")
	memMB := fs.Int("mem-mb", 32, "state each game server walks, in MB")
	dur := fs.Duration("duration", 30*time.Second, "measured time per run")
	warm := fs.Duration("warmup", 2*time.Second, "unmeasured time at the start of each run")
	wait := fs.String("wait", "sleep", "how loops wait for the next update: sleep, hybrid or spin")
	out := fs.String("out", "", "write JSON here instead of stdout")
	fs.Parse(args)

	hzs, err := ints(*hzList)
	if err != nil {
		return fmt.Errorf("-hz: %w", err)
	}
	ramp := *instList == "ramp"
	var counts []int
	if ramp {
		counts = rampCounts(runtime.NumCPU())
	} else if counts, err = ints(*instList); err != nil {
		return fmt.Errorf("-instances: %w", err)
	}

	rep := TickReport{Kit: "gsbench " + version, Machine: sysinfo(), Started: time.Now().UTC(), Fits: map[string]int{}}
	for _, hz := range hzs {
		best := 0
		for _, n := range counts {
			r, err := tick.Run(ctx, tick.Config{Hz: hz, WorkUnits: *work, MemBytes: *memMB << 20, Instances: n,
				Duration: *dur, Warmup: *warm, Wait: tick.Wait(*wait)})
			if err != nil {
				return err
			}
			rep.Runs = append(rep.Runs, r)
			verdict := "fits"
			if !r.Fits {
				verdict = "DOES NOT FIT"
			}
			fmt.Fprintf(os.Stderr, "%3d Hz x %3d: work p50 %.2f p99 %.2f ms, late p99 %.2f ms, frame p99 %.2f of %.2f ms, overrun %.2f%%, skipped %d: %s\n",
				hz, n, r.Work.P50, r.Work.P99, r.Late.P99, r.Frame.P99, r.BudgetMS, r.OverrunPct, r.SkippedTicks, verdict)
			if r.Fits && n > best {
				best = n
			}
			if ramp && !r.Fits {
				break
			}
		}
		rep.Fits[strconv.Itoa(hz)] = best
	}
	return emit(*out, rep)
}

// rampCounts doubles from 1 and always ends at 2x CPUs, so a machine whose CPU count is not a power of two is still
// tried at its top count (24 threads: 1, 2, 4, 8, 16, 32, 48; before 0.2.1 the ramp stopped at 32).
func rampCounts(cpus int) []int {
	top := 2 * cpus
	var counts []int
	for n := 1; n < top; n *= 2 {
		counts = append(counts, n)
	}
	return append(counts, top)
}

// echoDefault is how long udp-echo answers when -duration is not given. It answers anyone who sends the kit's header,
// so it is for a test and stops by itself, never left running as a service.
const echoDefault = 10 * time.Minute

func cmdEcho(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("udp-echo", flag.ExitOnError)
	listen := fs.String("listen", ":7777", "address to listen on")
	dur := fs.Duration("duration", echoDefault, "how long to answer before it stops by itself")
	fs.Parse(args)
	if *dur <= 0 {
		return fmt.Errorf("-duration must be more than 0")
	}
	pc, err := net.ListenPacket("udp", *listen)
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "echoing on", pc.LocalAddr(), "for", *dur)
	return echoFor(ctx, pc, *dur)
}

// echoFor answers the kit's packets on pc for d, then stops and closes it.
func echoFor(ctx context.Context, pc net.PacketConn, d time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	return udp.Serve(ctx, pc)
}

func cmdUDP(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("udp", flag.ExitOnError)
	to := fs.String("to", "", "echo server host:port (required)")
	hz := fs.Int("hz", 64, "packets a second")
	size := fs.Int("size", 200, "bytes per packet")
	dur := fs.Duration("duration", 30*time.Second, "how long to send")
	out := fs.String("out", "", "write JSON here instead of stdout")
	fs.Parse(args)
	if *to == "" {
		return fmt.Errorf("-to is required")
	}
	conn, err := net.Dial("udp", *to)
	if err != nil {
		return err
	}
	defer conn.Close()
	r, err := udp.Probe(ctx, conn, udp.Config{Hz: *hz, Size: *size, Duration: *dur})
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "%s at %d Hz: rtt p50 %.2f p99 %.2f ms, jitter %.2f ms, loss %.2f%%, reordered %d\n",
		r.Target, r.Hz, r.RTT.P50, r.RTT.P99, r.JitterMS, r.LossPct, r.Reordered)
	return emit(*out, r)
}

func emit(path string, v any) error {
	var w io.Writer = os.Stdout
	if path != "" {
		f, err := os.Create(path)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func ints(s string) ([]int, error) {
	var out []int
	for _, p := range strings.Split(s, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("%q is not a positive number", p)
		}
		out = append(out, n)
	}
	return out, nil
}
