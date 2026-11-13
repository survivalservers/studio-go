// Package tick simulates game servers running a fixed-rate update loop and measures how well a machine keeps
// that rate: how long each update's work takes, how late each update starts, and how often an update runs past
// the start of the next one.
package tick

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/survivalservers/studio-go/bench/clock"
	"github.com/survivalservers/studio-go/bench/stats"
)

// Wait is how a loop waits for its next update.
type Wait string

const (
	WaitSleep  Wait = "sleep"  // sleep until the next update, as most game servers do
	WaitSpin   Wait = "spin"   // busy-wait: best timing, burns the core
	WaitHybrid Wait = "hybrid" // sleep until shortly before, then busy-wait
)

// hybridSpin is how long before the deadline a hybrid wait stops sleeping.
const hybridSpin = time.Millisecond

// Config describes one run: Instances game servers updating Hz times a second for Duration.
type Config struct {
	Hz        int
	WorkUnits int // fixed work per update; see Worker
	MemBytes  int // state each game server walks per update
	Instances int
	Duration  time.Duration
	Warmup    time.Duration // updates in this first stretch are not counted
	Wait      Wait
}

// Result is one run, all instances pooled.
type Result struct {
	Hz           int           `json:"hz"`
	Instances    int           `json:"instances"`
	WorkUnits    int           `json:"work_units"`
	MemMB        int           `json:"mem_mb_per_instance"`
	Wait         Wait          `json:"wait"`
	BudgetMS     float64       `json:"budget_ms"`
	Updates      int           `json:"updates"`
	Work         stats.Summary `json:"work"`          // time spent on each update's work
	Late         stats.Summary `json:"late"`          // how late each update started
	Frame        stats.Summary `json:"frame"`         // late + work: from the scheduled start to the end of the work
	OverrunPct   float64       `json:"overrun_pct"`   // updates whose frame ran past the next scheduled start
	SkippedTicks int           `json:"skipped_ticks"` // whole updates dropped to catch up
	Fits         bool          `json:"fits"`
}

// Fits is the pass line for "this many game servers fit": the slowest 1% of updates still finish inside the
// update budget and no update had to be dropped.
func fits(r Result) bool {
	return r.Updates > 0 && r.Frame.P99 <= r.BudgetMS && r.SkippedTicks == 0
}

type sample struct{ work, late, frame time.Duration }

// Run runs the configured instances side by side until ctx ends or Duration passes.
func Run(ctx context.Context, c Config) (Result, error) {
	if c.Hz <= 0 || c.Instances <= 0 || c.WorkUnits < 0 || c.Duration <= 0 {
		return Result{}, fmt.Errorf("hz, instances and duration must be positive, work units not negative")
	}
	switch c.Wait {
	case WaitSleep, WaitSpin, WaitHybrid:
	case "":
		c.Wait = WaitSleep
	default:
		return Result{}, fmt.Errorf("unknown wait %q", c.Wait)
	}
	budget := time.Second / time.Duration(c.Hz)

	workers := make([]*Worker, c.Instances)
	for i := range workers {
		workers[i] = NewWorker(c.MemBytes, uint64(i)+1)
	}

	per := make([][]sample, c.Instances)
	skipped := make([]int, c.Instances)
	// Every time below is a reading of the kit's fine clock (see package clock), never Go's own on Windows.
	start := clock.Now() + 50*time.Millisecond // every instance starts on the same beat
	countFrom := start + c.Warmup
	end := countFrom + c.Duration

	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			runtime.LockOSThread() // one OS thread per game server, like a real server's main loop
			defer runtime.UnlockOSThread()
			w := workers[i]
			out := make([]sample, 0, int(c.Duration/budget)+1)
			for k := int64(0); ; k++ {
				scheduled := start + time.Duration(k)*budget
				if scheduled >= end || ctx.Err() != nil {
					break
				}
				wait(c.Wait, scheduled)
				began := clock.Now()
				w.Do(c.WorkUnits)
				done := clock.Now()
				if scheduled >= countFrom {
					out = append(out, sample{work: done - began, late: began - scheduled, frame: done - scheduled})
				}
				// Fell behind by one or more whole updates: drop them, as a game server would, and count it.
				if behind := done - (scheduled + budget); behind > 0 {
					n := int64(behind / budget)
					if n > 0 && scheduled >= countFrom {
						skipped[i] += int(n)
					}
					k += n
				}
			}
			per[i] = out
		}(i)
	}
	wg.Wait()

	var work, late, frame []time.Duration
	overruns, skips := 0, 0
	for i, ss := range per {
		for _, s := range ss {
			work = append(work, s.work)
			late = append(late, s.late)
			frame = append(frame, s.frame)
			if s.frame > budget {
				overruns++
			}
		}
		skips += skipped[i]
	}
	r := Result{
		Hz:           c.Hz,
		Instances:    c.Instances,
		WorkUnits:    c.WorkUnits,
		MemMB:        c.MemBytes >> 20,
		Wait:         c.Wait,
		BudgetMS:     float64(budget) / float64(time.Millisecond),
		Updates:      len(frame),
		Work:         stats.Summarize(work),
		Late:         stats.Summarize(late),
		Frame:        stats.Summarize(frame),
		SkippedTicks: skips,
	}
	if r.Updates > 0 {
		r.OverrunPct = float64(overruns) * 100 / float64(r.Updates)
	}
	r.Fits = fits(r)
	return r, ctx.Err()
}

// wait returns at or after until (a clock reading).
func wait(mode Wait, until time.Duration) {
	switch mode {
	case WaitSpin:
		for clock.Now() < until {
		}
	case WaitHybrid:
		if d := until - clock.Now() - hybridSpin; d > 0 {
			time.Sleep(d)
		}
		for clock.Now() < until {
		}
	default:
		// Sleep again if woken early by the fine clock's reckoning, as a sleeping game server loop would.
		for d := until - clock.Now(); d > 0; d = until - clock.Now() {
			time.Sleep(d)
		}
	}
}
