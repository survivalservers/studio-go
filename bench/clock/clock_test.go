package clock

import (
	"testing"
	"time"
)

// The reason this package exists: a clock that only moves once per timer tick makes every update look instant
// or a whole tick long. Any working fine clock steps in well under 10 microseconds.
func TestFineGrained(t *testing.T) {
	if r := Resolution(); r > 10*time.Microsecond {
		t.Fatalf("%s steps %v at a time; timings would be meaningless", Source(), r)
	}
}

func TestKeepsTime(t *testing.T) {
	a := Now()
	time.Sleep(20 * time.Millisecond)
	d := Now() - a
	if d < 15*time.Millisecond || d > 5*time.Second {
		t.Fatalf("a 20 ms sleep measured %v", d)
	}
	if b := Now(); b < a {
		t.Fatalf("went backwards: %v then %v", a, b)
	}
}
