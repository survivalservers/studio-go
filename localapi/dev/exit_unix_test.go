//go:build !windows

package dev

import (
	"strings"
	"testing"
)

// A server that takes the stop signal's default action is ended by the system: dev reports and exits with 143 (128
// plus SIGTERM), as a shell and the page "Test locally" say, not -1 and 255.
func TestASignalEndIsReportedAsTheShellWould(t *testing.T) {
	out, _, done := start(t, "takes-sigterm", "-shutdown-time", "1s")
	if code := wait(t, done, out); code != 143 {
		t.Fatalf("exit %d, want 143:\n%s", code, out)
	}
	if s := out.String(); !strings.Contains(s, "with code 143") {
		t.Fatalf("output lacks the code 143:\n%s", s)
	}
}
