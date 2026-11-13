package stats

import (
	"testing"
	"time"
)

func TestSummarizeNearestRank(t *testing.T) {
	var s []time.Duration
	for i := 100; i >= 1; i-- { // unsorted on purpose
		s = append(s, time.Duration(i)*time.Millisecond)
	}
	got := Summarize(s)
	want := Summary{Count: 100, Mean: 50.5, P50: 50, P99: 99, P999: 100, Max: 100}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if s[0] != 100*time.Millisecond {
		t.Fatal("Summarize reordered the caller's samples")
	}
}

func TestSummarizeEmptyAndSingle(t *testing.T) {
	if got := Summarize(nil); got != (Summary{}) {
		t.Fatalf("empty: got %+v", got)
	}
	got := Summarize([]time.Duration{1500 * time.Microsecond})
	if got.P50 != 1.5 || got.P999 != 1.5 || got.Max != 1.5 || got.Count != 1 {
		t.Fatalf("single: got %+v", got)
	}
}
