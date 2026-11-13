// Package udp measures the network path a game uses: a client sends small packets at the game's update rate
// to an echo server and reports round-trip time, jitter, loss and reordering.
package udp

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/survivalservers/studio-go/bench/clock"
	"github.com/survivalservers/studio-go/bench/stats"
)

const (
	magic      = 0x67736231 // "gsb1"
	headerSize = 4 + 8 + 8  // magic, sequence, send time
)

// Serve echoes every packet that carries the kit's header back to its sender until ctx ends.
func Serve(ctx context.Context, conn net.PacketConn) error {
	go func() { <-ctx.Done(); conn.Close() }()
	buf := make([]byte, 65535)
	for {
		n, from, err := conn.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if n < headerSize || binary.BigEndian.Uint32(buf) != magic {
			continue
		}
		if _, err := conn.WriteTo(buf[:n], from); err != nil && ctx.Err() == nil {
			return err
		}
	}
}

// Config describes a client run.
type Config struct {
	Hz       int
	Size     int // bytes per packet, header included
	Duration time.Duration
	Grace    time.Duration // how long to wait for late echoes after the last send
}

// Result is one client run.
type Result struct {
	Target     string        `json:"target"`
	Hz         int           `json:"hz"`
	Size       int           `json:"size_bytes"`
	Sent       int           `json:"sent"`
	Received   int           `json:"received"`
	LossPct    float64       `json:"loss_pct"`
	Reordered  int           `json:"reordered"`
	Duplicates int           `json:"duplicates"`
	RTT        stats.Summary `json:"rtt"`
	JitterMS   float64       `json:"jitter_ms"` // mean change in round-trip time between consecutive echoes
}

// Probe sends packets over conn (already connected to the echo server) and measures the echoes.
func Probe(ctx context.Context, conn net.Conn, c Config) (Result, error) {
	if c.Hz <= 0 || c.Duration <= 0 {
		return Result{}, errors.New("hz and duration must be positive")
	}
	if c.Size < headerSize {
		c.Size = headerSize
	}
	if c.Grace <= 0 {
		c.Grace = time.Second
	}
	epoch := clock.Now() // send times and round trips use the fine clock (package clock)
	total := int(c.Duration * time.Duration(c.Hz) / time.Second)

	var (
		mu       sync.Mutex
		rtts     []time.Duration
		got      = make([]bool, total)
		maxSeq   = int64(-1)
		reorder  int
		dup      int
		readDone = make(chan error, 1)
	)
	go func() {
		buf := make([]byte, 65535)
		for {
			n, err := conn.Read(buf)
			now := clock.Now() - epoch
			if err != nil {
				readDone <- err
				return
			}
			if n < headerSize || binary.BigEndian.Uint32(buf) != magic {
				continue
			}
			seq := int64(binary.BigEndian.Uint64(buf[4:]))
			sentAt := time.Duration(binary.BigEndian.Uint64(buf[12:]))
			if seq < 0 || seq >= int64(total) {
				continue
			}
			mu.Lock()
			switch {
			case got[seq]:
				dup++
			default:
				got[seq] = true
				rtts = append(rtts, now-sentAt)
				if seq < maxSeq {
					reorder++
				} else {
					maxSeq = seq
				}
			}
			mu.Unlock()
		}
	}()

	pkt := make([]byte, c.Size)
	binary.BigEndian.PutUint32(pkt, magic)
	interval := time.Second / time.Duration(c.Hz)
	sent := 0
	for seq := 0; seq < total && ctx.Err() == nil; seq++ {
		for d := epoch + time.Duration(seq)*interval - clock.Now(); d > 0; d = epoch + time.Duration(seq)*interval - clock.Now() {
			time.Sleep(d)
		}
		binary.BigEndian.PutUint64(pkt[4:], uint64(seq))
		binary.BigEndian.PutUint64(pkt[12:], uint64(clock.Now()-epoch))
		if _, err := conn.Write(pkt); err != nil {
			return Result{}, fmt.Errorf("send: %w", err)
		}
		sent++
	}
	select {
	case <-time.After(c.Grace):
	case <-ctx.Done():
	case err := <-readDone:
		return Result{}, fmt.Errorf("receive: %w", err)
	}
	conn.SetReadDeadline(time.Now())

	mu.Lock()
	defer mu.Unlock()
	r := Result{
		Target:     conn.RemoteAddr().String(),
		Hz:         c.Hz,
		Size:       c.Size,
		Sent:       sent,
		Received:   len(rtts),
		Reordered:  reorder,
		Duplicates: dup,
		RTT:        stats.Summarize(rtts),
		JitterMS:   jitter(rtts),
	}
	if sent > 0 {
		r.LossPct = float64(sent-r.Received) * 100 / float64(sent)
	}
	return r, nil
}

// jitter is the mean absolute change between consecutive round trips, in milliseconds (RFC 3550's idea, unsmoothed).
func jitter(rtts []time.Duration) float64 {
	if len(rtts) < 2 {
		return 0
	}
	var sum time.Duration
	for i := 1; i < len(rtts); i++ {
		d := rtts[i] - rtts[i-1]
		if d < 0 {
			d = -d
		}
		sum += d
	}
	return float64(sum) / float64(len(rtts)-1) / float64(time.Millisecond)
}
