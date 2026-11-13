package client

import (
	"context"
	"time"
)

// Wait reads an object with get until done says it has settled, the time runs out or ctx ends. It starts polling
// every poll (3 seconds when zero) and backs off to 10 seconds, so a long wait costs few calls. progress, when not
// nil, hears the state after every read that has not settled. It returns the last object read and whether done
// said so.
func Wait(ctx context.Context, timeout, poll time.Duration, get func(context.Context) (map[string]any, error),
	done func(map[string]any) (bool, string), progress func(waited time.Duration, state string)) (map[string]any, bool, error) {
	if poll <= 0 {
		poll = 3 * time.Second
	}
	start := time.Now()
	deadline := start.Add(timeout)
	for {
		obj, err := get(ctx)
		if err != nil {
			return nil, false, err
		}
		ok, state := done(obj)
		if ok || !time.Now().Before(deadline) {
			return obj, ok, nil
		}
		if progress != nil {
			progress(time.Since(start), state)
		}
		select {
		case <-ctx.Done():
			return obj, false, ctx.Err()
		case <-time.After(min(poll, time.Until(deadline)+10*time.Millisecond)):
		}
		if poll < 10*time.Second {
			poll = min(poll+poll/2, 10*time.Second)
		}
	}
}

// Settled states, for Wait.
var (
	// BuildSettled: ready, failed or deleted.
	BuildSettled = func(b map[string]any) (bool, string) {
		st := str(b["status"])
		return st == "ready" || st == "failed" || st == "deleted", "build " + st
	}
	// GameServerSettled: anything but starting.
	GameServerSettled = func(gs map[string]any) (bool, string) {
		st := str(gs["status"])
		return st != "starting", "game server " + st
	}
	// PersistentServerSettled: running, stopped, paused or failed.
	PersistentServerSettled = func(p map[string]any) (bool, string) {
		st := str(p["status"])
		return st == "running" || st == "stopped" || st == "failed" || st == "paused", "persistent server " + st
	}
)

// VersionRegions counts a version's regions by readiness.
func VersionRegions(v map[string]any) (ready, failed, all int) {
	for _, r := range asList(v["regions"]) {
		all++
		switch str(r["status"]) {
		case "ready":
			ready++
		case "failed":
			failed++
		}
	}
	return
}

// VersionRemoved counts the regions whose machines removed the version (status removed): it is never copied there
// again until it is put on a branch, so nothing there is worth waiting for.
func VersionRemoved(v map[string]any) (removed int) {
	for _, r := range asList(v["regions"]) {
		if str(r["status"]) == "removed" {
			removed++
		}
	}
	return
}

// VersionSettled: ready in at least one region, or failed or removed in every one.
func VersionSettled(v map[string]any) (bool, string) {
	ready, failed, all := VersionRegions(v)
	return ready > 0 || (all > 0 && failed+VersionRemoved(v) == all), itoa(ready) + " of " + itoa(all) + " regions ready"
}
