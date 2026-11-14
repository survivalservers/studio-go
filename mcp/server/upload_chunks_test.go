package server

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// upload_windows_build sends a readable zip as only the chunks the studio's store lacks (file list, a link per missing
// chunk, complete with {}), and the same build again sends no chunk.
func TestUploadWindowsBuildSendsOnlyTheChunksTheStudioLacks(t *testing.T) {
	f := newFakeAPI(t)
	f.json("GET /v0/apps/brawl", 200, appBrawl)
	program := make([]byte, 6<<20)
	for i := range program {
		program[i] = byte(i*7919 + i/4093) // varied bytes, so the store cuts several chunks
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("WindowsServer/BrawlServer.exe")
	w.Write(program)
	zw.Close()
	path := filepath.Join(t.TempDir(), "BrawlServer.zip")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	held, puts := map[string]bool{}, map[string]int{}
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		puts[r.URL.Path]++
		if name, ok := strings.CutPrefix(r.URL.Path, "/chunk/"); ok {
			if sum := sha256.Sum256(data); hex.EncodeToString(sum[:]) != name {
				t.Errorf("chunk %s sent with other bytes", name)
			}
		}
		if r.Header.Get("Authorization") != "" {
			t.Error("the API key must never go to storage")
		}
	}))
	t.Cleanup(storage.Close)
	f.on("POST /v0/apps/brawl/builds", func(_ *http.Request, body map[string]any) (int, any) {
		if src, _ := body["source"].(map[string]any); src["chunks"] == nil {
			t.Errorf("the build was not sent as chunks: %v", body)
		}
		return 201, map[string]any{"id": "bld_c", "app": "app_brawl", "status": "awaiting_upload",
			"upload": map[string]any{"part_bytes": 0, "parts": []any{}, "list": map[string]any{"url": storage.URL + "/list"}}}
	})
	f.on("POST /v0/builds/bld_c/chunks", func(_ *http.Request, body map[string]any) (int, any) {
		mu.Lock()
		defer mu.Unlock()
		links := []any{}
		for _, c := range body["chunks"].([]any) {
			name := c.(map[string]any)["sha256"].(string)
			if !held[name] {
				links = append(links, map[string]any{"sha256": name, "url": storage.URL + "/chunk/" + name,
					"expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
			}
		}
		return 200, map[string]any{"links": links}
	})
	f.on("POST /v0/builds/bld_c/complete", func(_ *http.Request, body map[string]any) (int, any) {
		if len(body) != 0 {
			t.Errorf("a chunk build completes with {}, got %v", body)
		}
		mu.Lock()
		defer mu.Unlock()
		for p := range puts {
			if name, ok := strings.CutPrefix(p, "/chunk/"); ok {
				held[name] = true
			}
		}
		return 202, map[string]any{"id": "bld_c", "app": "app_brawl", "status": "processing"}
	})

	cs := f.session(t, Config{Key: key, Local: true})
	mustOK(t, cs, "upload_windows_build", map[string]any{"app": "brawl", "zip_path": path})
	mu.Lock()
	if puts["/list"] != 1 || len(held) < 2 {
		t.Errorf("the file list once and every chunk: list %d, chunks %d", puts["/list"], len(held))
	}
	mu.Unlock()
	mustOK(t, cs, "upload_windows_build", map[string]any{"app": "brawl", "zip_path": path})
	mu.Lock()
	defer mu.Unlock()
	for p, n := range puts {
		if strings.HasPrefix(p, "/chunk/") && n != 1 {
			t.Errorf("the same build again sent %s %d times", p, n)
		}
	}
	if puts["/list"] != 2 || len(f.called("POST", "/v0/builds/bld_c/complete")) != 2 {
		t.Errorf("the second upload sends its list and completes: list %d", puts["/list"])
	}
}
