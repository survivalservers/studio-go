# gsbench

A small, dependency-free kit that answers two questions about a machine for fixed-rate game servers:

1. **How many game servers fit at 64 and 128 updates a second** before the slowest 1% of updates run late.
2. **How good the network path is** at a game's packet rate: round trip, jitter, loss and reordering.

Runs the same on Windows and Linux. Apache-2.0.

## Install

With Go installed, on Windows or Linux:

```
go install github.com/survivalservers/studio-go/bench/cmd/gsbench@latest
```

Or in a clone, from this folder: `go build ./cmd/gsbench`.

## Update-loop test

```
gsbench tick -hz 64,128 -instances ramp -duration 30s -out result.json
```

Each simulated game server is one OS thread running a fixed-rate loop. Every update does a fixed amount of work
(arithmetic plus dependent reads across `-mem-mb` of state, so cache and memory speed count), then waits for the next
update. Because the work is fixed, a slower or busier machine shows up as a longer update.

For every update the kit records:

| Field | Meaning |
|---|---|
| `work` | time the update's work took |
| `late` | how late the update started against its schedule (timer and scheduler quality) |
| `frame` | late + work: from the scheduled start to the end of the work |
| `overrun_pct` | share of updates whose frame ran past the next scheduled start |
| `skipped_ticks` | whole updates dropped to catch up, as a game server would |

A run **fits** when frame p99 is inside the update budget (15.625 ms at 64 Hz, 7.8125 ms at 128 Hz) and no update was
dropped. `-instances ramp` doubles the count from 1 and ends at twice the thread count (24 threads: 1, 2, 4, 8, 16,
32, 48), stopping at the first run that does not fit; `fits` in the report gives the largest passing count per rate.

Options: `-work` (units per update, default 400, a light-to-medium server on a current desktop-class core),
`-mem-mb` (default 32), `-wait sleep|hybrid|spin` (default `sleep`, what most servers do), `-warmup`, `-duration`.

Run it on an otherwise idle machine; anything else running counts against the result.

## Network test

```
gsbench udp-echo -listen :7777                         # on the server, for 10 minutes
gsbench udp -to server:7777 -hz 64 -size 200 -duration 30s   # on the client
```

The client sends `-size` byte packets at `-hz` and reports round-trip p50/p99/p99.9, jitter (mean change between
consecutive round trips), loss, duplicates and reordering. The echo server answers only the kit's own packets.

Run the echo server only for a test: it answers whoever sends the kit's packets, so it stops by itself after 10
minutes (`-duration` sets another time).

## Machine details

`gsbench sysinfo` prints the OS, kernel or build, CPU name, thread count and the clock used for timings; every tick
report includes it.

## Timing on Windows

Go's own clock on Windows moves only once per system timer tick (0.5 to 15.6 ms), too coarse for a 7.8 ms update.
The kit times everything with the performance counter there (`clock` in the report says which clock and how fine its
steps were). Results from version 0.1.0 on Windows used the coarse clock and are not comparable.
