// Package clock is the kit's fine-grained monotonic clock.
//
// Go's own monotonic clock on Windows reads the system interrupt time, which moves only once per timer tick
// (0.5 to 15.6 ms depending on what else runs). An update at 128 a second has 7.8 ms, so timings taken with it
// come out as 0 or one whole tick. On Windows this package reads the performance counter instead; elsewhere Go's
// clock is already fine-grained and is used as is.
package clock

import (
	"sort"
	"time"
)

// Now is the time since a fixed point in this process. Only differences between two readings mean anything.
func Now() time.Duration { return now() }

// Source names the clock in use, for reports.
func Source() string { return source }

// Resolution measures the smallest step the clock takes: the median of many steps between two readings that
// differ.
func Resolution() time.Duration {
	const n = 201
	steps := make([]time.Duration, n)
	for i := range steps {
		t0 := Now()
		t1 := Now()
		for t1 == t0 {
			t1 = Now()
		}
		steps[i] = t1 - t0
	}
	sort.Slice(steps, func(a, b int) bool { return steps[a] < steps[b] })
	return steps[n/2]
}
