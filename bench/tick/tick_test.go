package tick

import (
	"context"
	"testing"
	"time"

	"github.com/survivalservers/studio-go/bench/clock"
)

func TestWorkerChainIsOneCycle(t *testing.T) {
	w := NewWorker(64<<10, 7)
	n := len(w.chain)
	seen := make([]bool, n)
	pos := uint32(0)
	for i := 0; i < n; i++ {
		if seen[pos] {
			t.Fatalf("walk came back to %d after %d of %d steps", pos, i, n)
		}
		seen[pos] = true
		pos = w.chain[pos]
	}
	if pos != 0 {
		t.Fatalf("walk did not return to the start after %d steps", n)
	}
}

func TestLightLoadFits(t *testing.T) {
	r, err := Run(context.Background(), Config{Hz: 64, WorkUnits: 1, MemBytes: 1 << 20, Instances: 2,
		Duration: 500 * time.Millisecond, Warmup: 50 * time.Millisecond, Wait: WaitHybrid})
	if err != nil {
		t.Fatal(err)
	}
	// 64 updates a second for half a second, two instances: about 64 scheduled updates, each of which either ran
	// or was counted as dropped (allow for the start beat and a catch-up jump at the end).
	if got := r.Updates + r.SkippedTicks; r.Updates == 0 || got < 56 || got > 72 {
		t.Fatalf("updates %d + dropped %d = %d, want about 64", r.Updates, r.SkippedTicks, got)
	}
	if r.BudgetMS != 15.625 {
		t.Fatalf("budget = %v ms, want 15.625", r.BudgetMS)
	}
	// Machine quality is not this test's business (shared runners and busy PCs stall); the mechanics are: one
	// unit of work takes microseconds, so the typical update's work must measure as tiny, wait not included.
	if r.Work.P50 > 1 {
		t.Fatalf("light load measured as heavy: %+v", r)
	}
}

func TestFitsLine(t *testing.T) {
	ok := Result{Updates: 100, BudgetMS: 15.625}
	ok.Frame.P99 = 15.6
	if !fits(ok) {
		t.Fatal("p99 inside the budget should fit")
	}
	late := ok
	late.Frame.P99 = 15.7
	skipped := ok
	skipped.SkippedTicks = 1
	if fits(late) || fits(skipped) || fits(Result{BudgetMS: 15.625}) {
		t.Fatal("over budget, a dropped update or no updates must not fit")
	}
}

func TestOverloadIsCaught(t *testing.T) {
	w := NewWorker(1<<20, 1)
	// The fastest of several timings: on a busy machine a single one can be stretched, giving too little work.
	perUnit := time.Duration(1 << 62)
	for i := 0; i < 5; i++ {
		t0 := clock.Now()
		w.Do(50)
		if d := (clock.Now() - t0) / 50; d > 0 && d < perUnit {
			perUnit = d
		}
	}
	units := int(30*time.Millisecond/perUnit) + 1 // about 30 ms of work against a 7.8 ms budget

	r, err := Run(context.Background(), Config{Hz: 128, WorkUnits: units, MemBytes: 1 << 20, Instances: 1,
		Duration: 400 * time.Millisecond, Wait: WaitSleep})
	if err != nil {
		t.Fatal(err)
	}
	// What matters: the run does not fit and whole updates were dropped. (The share of updates that overran
	// depends on how busy the machine is, so it is only required to be most of them.)
	if r.Fits || r.SkippedTicks == 0 || r.OverrunPct < 50 {
		t.Fatalf("overload not caught: %+v", r)
	}
}

func TestRejectsBadConfig(t *testing.T) {
	if _, err := Run(context.Background(), Config{Hz: 0, Instances: 1, Duration: time.Second}); err == nil {
		t.Fatal("hz 0 accepted")
	}
	if _, err := Run(context.Background(), Config{Hz: 64, Instances: 1, Duration: time.Second, Wait: "nap"}); err == nil {
		t.Fatal("unknown wait accepted")
	}
}
