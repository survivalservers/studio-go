package main

import (
	"context"
	"net"
	"reflect"
	"testing"
	"time"
)

func TestRampEndsAtTwiceTheCPUs(t *testing.T) {
	for cpus, want := range map[int][]int{
		1:  {1, 2},
		2:  {1, 2, 4},
		12: {1, 2, 4, 8, 16, 24},
		16: {1, 2, 4, 8, 16, 32},
		24: {1, 2, 4, 8, 16, 32, 48},
	} {
		if got := rampCounts(cpus); !reflect.DeepEqual(got, want) {
			t.Errorf("%d CPUs: %v, want %v", cpus, got, want)
		}
	}
}

func TestEchoStopsByItself(t *testing.T) {
	if echoDefault != 10*time.Minute {
		t.Fatalf("udp-echo answers for %v by default, want 10m", echoDefault)
	}
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err := echoFor(context.Background(), pc, 200*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(start); took < 200*time.Millisecond || took > 5*time.Second {
		t.Fatalf("echo stopped after %v, want about 200ms", took)
	}
	if _, err := pc.WriteTo([]byte("x"), pc.LocalAddr()); err == nil {
		t.Fatal("the socket is still open after the echo stopped")
	}
}
