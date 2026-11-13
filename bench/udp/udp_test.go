package udp

import (
	"context"
	"net"
	"testing"
	"time"
)

func TestProbeAgainstLocalEcho(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Serve(ctx, pc)

	conn, err := net.Dial("udp", pc.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	r, err := Probe(ctx, conn, Config{Hz: 128, Size: 200, Duration: 500 * time.Millisecond, Grace: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if r.Sent != 64 || r.Received != 64 || r.LossPct != 0 || r.Duplicates != 0 {
		t.Fatalf("local echo lost packets: %+v", r)
	}
	// Catches a units mistake (seconds or nanoseconds reported as milliseconds), not a slow machine: a loaded
	// test runner can take tens of milliseconds for a local round trip.
	if r.RTT.P50 < 0.001 || r.RTT.P99 > 1000 {
		t.Fatalf("local round trip p50 %.6f ms, p99 %.3f ms: not in milliseconds", r.RTT.P50, r.RTT.P99)
	}
}

func TestServeIgnoresForeignPackets(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Serve(ctx, pc)

	conn, err := net.Dial("udp", pc.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.Write([]byte("not ours at all, long enough to pass the size check"))
	conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if n, err := conn.Read(make([]byte, 128)); err == nil {
		t.Fatalf("server echoed a foreign packet (%d bytes)", n)
	}
}

func TestJitter(t *testing.T) {
	ms := time.Millisecond
	if got := jitter([]time.Duration{10 * ms, 12 * ms, 11 * ms, 15 * ms}); got != (2.0+1.0+4.0)/3 {
		t.Fatalf("jitter = %v", got)
	}
}
