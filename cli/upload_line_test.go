package cli

import (
	"path/filepath"
	"runtime"
	"testing"
	"time"

	api "github.com/survivalservers/studio-go"
)

// The upload line says how far, how fast and how long is left, counting speed only on what this run sent; the
// upload records live in the user's cache folder.
func TestUploadSpeedLineAndRecordFolder(t *testing.T) {
	p := api.UploadProgress{Parts: 8, TotalParts: 16, Bytes: 512 << 20, Resumed: 256 << 20, TotalBytes: 1 << 30, Elapsed: 10 * time.Second}
	if got := speedText(p); got != ", 512.0 MB of 1.0 GB, 25.6 MB/s, about 20s left" {
		t.Errorf("speed line %q", got)
	}
	if got := speedText(api.UploadProgress{Bytes: 10, TotalBytes: 20}); got != ", 10 bytes of 20 bytes" {
		t.Errorf("no speed before a second has passed: %q", got)
	}
	vars := map[string]string{"HOME": "/home/ci"}
	c := &ctxt{env: Env{Getenv: func(k string) string { return vars[k] }}}
	if got := c.uploadRecords(); runtime.GOOS != "windows" && got != filepath.Join("/home/ci", ".cache", "survivalservers", "uploads") {
		t.Errorf("records folder %q", got)
	}
	vars["XDG_CACHE_HOME"] = "/cache"
	if got := c.uploadRecords(); got != filepath.Join("/cache", "survivalservers", "uploads") {
		t.Errorf("XDG_CACHE_HOME first: %q", got)
	}
	if got := (&ctxt{env: Env{Getenv: func(string) string { return "" }}}).uploadRecords(); got != "" {
		t.Errorf("no home, no records: %q", got)
	}
}
