package client

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeChunks is the API and storage for builds sent as chunks. held is the studio's store: the chunks it holds
// already. A complete moves every chunk sent into held, as processing does, after checking the list against them.
type fakeChunks struct {
	t        *testing.T
	mu       sync.Mutex
	held     map[string][]byte
	sent     map[string][]byte // this build's chunks, by name
	list     []byte
	source   map[string]any
	refuse   bool           // the API does not take chunks (an older API: an unknown field is bad_request)
	expire   map[string]int // a chunk's first n links are refused by storage, as an expired link is
	links    map[string]int // links handed out per chunk
	life     time.Duration  // a link's life
	builds   int
	complete int
	api      *httptest.Server
	store    *httptest.Server
}

// snap reads the fake's counts under its lock: the handlers ran on the servers' goroutines.
func (f *fakeChunks) snap() (held, sent, builds, complete int, list string, source map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.held), len(f.sent), f.builds, f.complete, string(f.list), f.source
}

func newFakeChunks(t *testing.T) *fakeChunks {
	f := &fakeChunks{t: t, held: map[string][]byte{}, sent: map[string][]byte{}, expire: map[string]int{}, links: map[string]int{}, life: 15 * time.Minute}
	f.store = httptest.NewServer(http.HandlerFunc(f.storage))
	f.api = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(func() { f.api.Close(); f.store.Close() })
	return f
}

func (f *fakeChunks) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	switch {
	case r.Method == "POST" && r.URL.Path == "/v0/apps/brawl/builds":
		src, _ := body["source"].(map[string]any)
		if f.refuse && src["chunks"] != nil {
			w.WriteHeader(400)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": 400, "code": "bad_request", "detail": "unknown field source.chunks"})
			return
		}
		f.builds++
		f.source, f.sent, f.list = src, map[string][]byte{}, nil
		if src["chunks"] == nil { // a zip, after the fallback: ready at once, nothing to send here
			w.WriteHeader(201)
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "bld_zip", "status": "ready"})
			return
		}
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "bld_c", "status": "awaiting_upload", "upload": map[string]any{"part_bytes": 0, "parts": []any{},
			"list": map[string]any{"url": f.store.URL + "/list", "headers": map[string]string{"X-Amz-Checksum-Sha256": "list"}}}})
	case r.Method == "POST" && r.URL.Path == "/v0/builds/bld_c/chunks":
		var links []any
		for _, c := range body["chunks"].([]any) {
			name := c.(map[string]any)["sha256"].(string)
			if _, ok := f.held[name]; ok {
				continue
			}
			if _, ok := f.sent[name]; ok {
				continue
			}
			f.links[name]++
			links = append(links, map[string]any{"sha256": name, "url": fmt.Sprintf("%s/chunk/%s?n=%d", f.store.URL, name, f.links[name]),
				"headers": map[string]string{"X-Amz-Checksum-Sha256": name}, "expires_at": time.Now().Add(f.life).UTC().Format(time.RFC3339Nano)})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"links": links})
	case r.Method == "POST" && r.URL.Path == "/v0/builds/bld_c/complete":
		f.complete++
		if len(body) != 0 {
			f.t.Errorf("a chunk build completes with {}, got %v", body)
		}
		f.checkList()
		for k, v := range f.sent {
			f.held[k] = v
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "bld_c", "status": "processing"})
	default:
		f.t.Errorf("unexpected call %s %s", r.Method, r.URL.Path)
		w.WriteHeader(500)
	}
}

func (f *fakeChunks) storage(w http.ResponseWriter, r *http.Request) {
	data, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "" {
		f.t.Error("the API key never goes to storage")
	}
	if r.ContentLength != int64(len(data)) {
		f.t.Errorf("%s sent without its exact length", r.URL.Path)
	}
	if r.URL.Path == "/list" {
		f.list = data
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/chunk/")
	if n, _ := strconv.Atoi(r.URL.Query().Get("n")); n <= f.expire[name] {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != name || r.Header.Get("X-Amz-Checksum-Sha256") != name {
		w.WriteHeader(http.StatusBadRequest) // the store refuses bytes that are not the ones the link was signed for
		return
	}
	if _, dup := f.sent[name]; dup {
		f.t.Errorf("chunk %s sent twice", name)
	}
	f.sent[name] = data
}

// checkList reads the list as processing does: every file's chunks are in the store or sent, and add up to the
// file's SHA-256 and size.
func (f *fakeChunks) checkList() {
	sum := sha256.Sum256(f.list)
	if f.source["sha256"] != hex.EncodeToString(sum[:]) || f.source["bytes"] != float64(len(f.list)) {
		f.t.Errorf("the list sent is not the one the build was made with")
	}
	files, total := 0, int64(0)
	sc := bufio.NewScanner(bytes.NewReader(f.list))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var l struct {
			Path   string
			Folder bool
			Size   int64
			SHA256 string
			Chunks [][2]any
		}
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			f.t.Fatalf("list line %q: %v", sc.Text(), err)
		}
		if l.Folder {
			continue
		}
		var whole []byte
		for _, c := range l.Chunks {
			name := c[0].(string)
			data, ok := f.sent[name]
			if !ok {
				data, ok = f.held[name]
			}
			if !ok || float64(len(data)) != c[1].(float64) {
				f.t.Errorf("%s names chunk %s, which is neither held nor sent at its size", l.Path, name)
				return
			}
			whole = append(whole, data...)
		}
		ws := sha256.Sum256(whole)
		if hex.EncodeToString(ws[:]) != l.SHA256 || int64(len(whole)) != l.Size {
			f.t.Errorf("%s: its chunks do not make the file", l.Path)
		}
		files++
		total += l.Size
	}
	chunks := f.source["chunks"].(map[string]any)
	if chunks["files"] != float64(files) || chunks["bytes"] != float64(total) {
		f.t.Errorf("the build was made for %v, the list holds %d files of %d bytes", chunks, files, total)
	}
}

// game writes a game server folder: a 9 MiB program, a small settings file and an empty saves folder.
func game(t *testing.T, seed int64) string {
	dir := t.TempDir()
	exe := make([]byte, 9<<20)
	rand.New(rand.NewSource(seed)).Read(exe)
	if err := os.MkdirAll(filepath.Join(dir, "Server", "Saved"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Server", "BrawlServer.exe"), exe, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Server", "Game.ini"), []byte("[Server]\nMaxPlayers=16\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestUploadChunksSendsOnlyWhatTheStudioLacks: the first build sends every chunk; the same build again sends none;
// a build with one byte changed in the middle of a 9 MiB program sends the one chunk that holds it.
func TestUploadChunksSendsOnlyWhatTheStudioLacks(t *testing.T) {
	f := newFakeChunks(t)
	c, err := New(f.api.URL, "gsk_live_EXAMPLE_not_a_real_key", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	dir := game(t, 1)
	var counted []int
	opt := UploadOptions{Storage: f.store.Client(), OnPart: func(p UploadProgress) {
		if !p.Chunks {
			t.Errorf("a chunk build's progress says Chunks")
		}
		counted = append(counted, p.Parts)
	}}
	b, err := c.UploadChunks(context.Background(), "brawl", dir, opt)
	if err != nil || b["status"] != "processing" {
		t.Fatalf("first build: %v %v", b, err)
	}
	first, _, _, _, list, _ := f.snap()
	if first < 4 || len(counted) != first {
		t.Fatalf("first build sent %d chunks, counted %v", first, counted)
	}
	if !strings.Contains(list, `"path":"Server/Saved","folder":true`) {
		t.Errorf("the empty saves folder is in the list: %s", list)
	}

	if _, err := c.UploadChunks(context.Background(), "brawl", dir, UploadOptions{Storage: f.store.Client()}); err != nil {
		t.Fatal(err)
	}
	if _, sent, _, _, _, _ := f.snap(); sent != 0 {
		t.Fatalf("the same build again sent %d chunks", sent)
	}

	exe := filepath.Join(dir, "Server", "BrawlServer.exe")
	data, _ := os.ReadFile(exe)
	data[5<<20] ^= 0xff
	if err := os.WriteFile(exe, data, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := c.UploadChunks(context.Background(), "brawl", dir, UploadOptions{Storage: f.store.Client()}); err != nil {
		t.Fatal(err)
	}
	// The chunk that holds the byte changes; a cut within 64 bytes of it could move, so at most the one after too.
	if _, sent, builds, complete, _, _ := f.snap(); sent < 1 || sent > 2 || builds != 3 || complete != 3 {
		t.Errorf("a one-byte change sent %d chunks (want 1, at most 2); %d builds, %d completes", sent, builds, complete)
	}
}

// TestUploadChunksFromAZip: a zip's files are cut the same way, so a zip of the same folder sends nothing new.
func TestUploadChunksFromAZip(t *testing.T) {
	f := newFakeChunks(t)
	c, _ := New(f.api.URL, "gsk_live_EXAMPLE_not_a_real_key", nil, "")
	dir := game(t, 2)
	if _, err := c.UploadChunks(context.Background(), "brawl", dir, UploadOptions{Storage: f.store.Client()}); err != nil {
		t.Fatal(err)
	}
	zipPath := filepath.Join(t.TempDir(), "BrawlServer.zip")
	out, _ := os.Create(zipPath)
	zw := zip.NewWriter(out)
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || p == dir {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if info.IsDir() {
			_, err := zw.Create(rel + "/")
			return err
		}
		w, err := zw.Create(rel)
		if err != nil {
			return err
		}
		data, _ := os.ReadFile(p)
		_, err = w.Write(data)
		return err
	})
	zw.Close()
	out.Close()
	if _, err := c.UploadChunks(context.Background(), "brawl", zipPath, UploadOptions{Storage: f.store.Client()}); err != nil {
		t.Fatal(err)
	}
	if _, sent, _, _, _, _ := f.snap(); sent != 0 {
		t.Errorf("the zip of a folder already sent sent %d chunks", sent)
	}
}

// TestUploadChunksAsksAgainForARefusedLink: a link storage refuses has run out: the client waits until it has, asks
// for a fresh one, and the chunk goes on that.
func TestUploadChunksAsksAgainForARefusedLink(t *testing.T) {
	f := newFakeChunks(t)
	f.life = time.Second
	c, _ := New(f.api.URL, "gsk_live_EXAMPLE_not_a_real_key", nil, "")
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Server.exe"), []byte("a small program"), 0o755)
	sum := sha256.Sum256([]byte("a small program"))
	name := hex.EncodeToString(sum[:])
	f.expire[name] = 1
	if _, err := c.UploadChunks(context.Background(), "brawl", dir, UploadOptions{Storage: f.store.Client()}); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.links[name] != 2 || f.held[name] == nil {
		t.Errorf("%d links for the chunk, held %v", f.links[name], f.held[name] != nil)
	}
}

// TestUploadChunksFallsBackToAZip: an API that does not take chunks gets a zip whole; a folder then fails, saying
// to zip it.
func TestUploadChunksFallsBackToAZip(t *testing.T) {
	f := newFakeChunks(t)
	f.refuse = true
	c, _ := New(f.api.URL, "gsk_live_EXAMPLE_not_a_real_key", nil, "")
	dir := game(t, 3)
	if _, err := c.UploadChunks(context.Background(), "brawl", dir, UploadOptions{Storage: f.store.Client()}); !errors.Is(err, ErrChunksNotTaken) {
		t.Errorf("a folder on an API without chunks: %v", err)
	}
	notAZip := filepath.Join(t.TempDir(), "server.zip")
	os.WriteFile(notAZip, []byte(strings.Repeat("z", 25)), 0o600)
	b, err := c.UploadChunks(context.Background(), "brawl", notAZip, UploadOptions{Storage: f.store.Client()})
	_, _, _, _, _, source := f.snap()
	if err != nil || b["id"] != "bld_zip" || source["chunks"] != nil || source["sha256"] == nil {
		t.Errorf("a file that is not a readable zip goes up whole: %v %v %v", b, err, source)
	}
}
