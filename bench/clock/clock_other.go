//go:build !windows

package clock

import "time"

const source = "monotonic"

var base = time.Now()

func now() time.Duration { return time.Since(base) }
