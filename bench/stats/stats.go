// Package stats turns raw timing samples into the percentiles the kit reports.
package stats

import (
	"math"
	"sort"
	"time"
)

// Summary is a set of samples described in milliseconds.
type Summary struct {
	Count int     `json:"count"`
	Mean  float64 `json:"mean_ms"`
	P50   float64 `json:"p50_ms"`
	P99   float64 `json:"p99_ms"`
	P999  float64 `json:"p999_ms"`
	Max   float64 `json:"max_ms"`
}

// Summarize sorts a copy of samples and reads its percentiles by nearest rank.
func Summarize(samples []time.Duration) Summary {
	n := len(samples)
	if n == 0 {
		return Summary{}
	}
	s := make([]time.Duration, n)
	copy(s, samples)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	var total time.Duration
	for _, d := range s {
		total += d
	}
	return Summary{
		Count: n,
		Mean:  ms(total / time.Duration(n)),
		P50:   ms(Percentile(s, 0.50)),
		P99:   ms(Percentile(s, 0.99)),
		P999:  ms(Percentile(s, 0.999)),
		Max:   ms(s[n-1]),
	}
}

// Percentile reads the nearest-rank percentile p (0..1] from samples that are already sorted.
func Percentile(sorted []time.Duration, p float64) time.Duration {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	i := int(math.Ceil(p*float64(n))) - 1
	if i < 0 {
		i = 0
	}
	if i >= n {
		i = n - 1
	}
	return sorted[i]
}

func ms(d time.Duration) float64 {
	return math.Round(float64(d)/float64(time.Millisecond)*1000) / 1000
}
