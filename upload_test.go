package client

import (
	"context"
	"encoding/json"
	"fmt"
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

// fakeUpload is the API and storage for one zip build of 25 bytes in parts of 10 (three parts). Links carry a
// generation; storage refuses the parts and generations named in refuse, as an expired link is refused.
type fakeUpload struct {
	t                             *testing.T
	mu                            sync.Mutex
	gen                           int
	refuse                        map[string]bool // "part/gen"
	status                        string
	posts, reads, puts, completes int
	inFlight, maxInFlight         int
	hold                          chan struct{} // while set, a part waits for it (or a second) before it answers
	completed                     []UploadedPart
	api, store                    *httptest.Server
}

func newFakeUpload(t *testing.T) *fakeUpload {
	f := &fakeUpload{t: t, refuse: map[string]bool{}, status: "awaiting_upload"}
	f.store = httptest.NewServer(http.HandlerFunc(f.storage))
	f.api = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(func() { f.api.Close(); f.store.Close() })
	return f
}

func (f *fakeUpload) plan() map[string]any {
	parts := []any{}
	for n := 1; n <= 3; n++ {
		parts = append(parts, map[string]any{"number": n, "url": fmt.Sprintf("%s/part?n=%d&gen=%d", f.store.URL, n, f.gen)})
	}
	b := map[string]any{"id": "bld_1", "status": f.status}
	if f.status == "awaiting_upload" {
		b["upload"] = map[string]any{"part_bytes": 10, "parts": parts}
	}
	return b
}

func (f *fakeUpload) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == "POST" && r.URL.Path == "/v0/apps/brawl/builds":
		f.posts++
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(f.plan())
	case r.Method == "GET" && r.URL.Path == "/v0/builds/bld_1":
		f.reads++
		f.gen++ // every read hands out fresh links
		_ = json.NewEncoder(w).Encode(f.plan())
	case r.Method == "POST" && r.URL.Path == "/v0/builds/bld_1/complete":
		f.completes++
		var body struct{ Parts []UploadedPart }
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.completed = body.Parts
		f.status = "processing"
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "bld_1", "status": "processing"})
	default:
		f.t.Errorf("unexpected call %s %s", r.Method, r.URL.Path)
		w.WriteHeader(500)
	}
}

func (f *fakeUpload) storage(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	n, gen := r.URL.Query().Get("n"), r.URL.Query().Get("gen")
	f.mu.Lock()
	f.puts++
	f.inFlight++
	f.maxInFlight = max(f.maxInFlight, f.inFlight)
	hold, refused := f.hold, f.refuse[n+"/"+gen]
	f.mu.Unlock()
	if hold != nil {
		select {
		case <-hold:
		case <-time.After(time.Second):
		}
	}
	f.mu.Lock()
	f.inFlight--
	f.mu.Unlock()
	if refused {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("<Error><Code>AccessDenied</Code><Message>Request has expired</Message></Error>"))
		return
	}
	w.Header().Set("ETag", fmt.Sprintf(`"etag-%s-%d"`, n, len(body)))
}

func testZip(t *testing.T) string {
	zip := filepath.Join(t.TempDir(), "server.zip")
	if err := os.WriteFile(zip, []byte(strings.Repeat("z", 25)), 0o600); err != nil {
		t.Fatal(err)
	}
	return zip
}

// An upload outlives its links: a refused link is replaced by reading the build again (once for every part that
// saw it refused), and the upload carries on.
func TestUploadZipReplacesAnExpiredLinkAndCarriesOn(t *testing.T) {
	f := newFakeUpload(t)
	f.refuse["2/0"], f.refuse["3/0"] = true, true
	c, _ := New(f.api.URL, "gsk_live_EXAMPLE_not_a_real_key", nil, "")
	b, err := c.UploadZip(context.Background(), "brawl", testZip(t), UploadOptions{Storage: f.store.Client()})
	if err != nil || b["status"] != "processing" {
		t.Fatalf("the upload goes on with fresh links: %v %v", b, err)
	}
	if f.reads != 1 || f.completes != 1 {
		t.Errorf("one read for fresh links, one complete: reads %d, completes %d", f.reads, f.completes)
	}
	want := []UploadedPart{{1, `"etag-1-10"`}, {2, `"etag-2-10"`}, {3, `"etag-3-5"`}}
	if fmt.Sprint(f.completed) != fmt.Sprint(want) {
		t.Errorf("complete gets every part, in order, with its exact length: %v", f.completed)
	}
}

// A link refused again after a fresh one stops the upload with a reason.
func TestUploadZipStopsWhenAFreshLinkIsRefusedToo(t *testing.T) {
	f := newFakeUpload(t)
	f.refuse["2/0"], f.refuse["2/1"] = true, true
	c, _ := New(f.api.URL, "gsk_live_EXAMPLE_not_a_real_key", nil, "")
	_, err := c.UploadZip(context.Background(), "brawl", testZip(t), UploadOptions{Storage: f.store.Client(), Parallel: 1})
	if err == nil || !strings.Contains(err.Error(), "build bld_1, part 2 of 3: storage answered 403") || !strings.Contains(err.Error(), "expired or was refused") {
		t.Fatalf("refused twice: %v", err)
	}
	if f.reads != 1 || f.completes != 0 {
		t.Errorf("one try with fresh links, no complete: reads %d, completes %d", f.reads, f.completes)
	}
}

// Parts go three at once, as in the panel.
func TestUploadZipSendsThreePartsAtOnce(t *testing.T) {
	f := newFakeUpload(t)
	f.hold = make(chan struct{})
	go func() {
		for end := time.Now().Add(2 * time.Second); time.Now().Before(end); {
			f.mu.Lock()
			in := f.inFlight
			f.mu.Unlock()
			if in == 3 {
				close(f.hold)
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	c, _ := New(f.api.URL, "gsk_live_EXAMPLE_not_a_real_key", nil, "")
	var calls []int
	if _, err := c.UploadZip(context.Background(), "brawl", testZip(t), UploadOptions{Storage: f.store.Client(), Progress: func(done, total int) { calls = append(calls, done) }}); err != nil {
		t.Fatal(err)
	}
	if f.maxInFlight != 3 || fmt.Sprint(calls) != "[1 2 3]" {
		t.Errorf("three parts at once, progress counted one at a time: max %d, progress %v", f.maxInFlight, calls)
	}
}

// A re-run of a stopped upload, with no idempotency key, carries on with the build and the parts the first run
// left, sends only what is missing, and leaves no record once the build is complete.
func TestUploadZipCarriesOnFromTheLastRun(t *testing.T) {
	f := newFakeUpload(t)
	f.refuse["3/0"], f.refuse["3/1"] = true, true
	dir := filepath.Join(t.TempDir(), "uploads")
	zip := testZip(t)
	c, _ := New(f.api.URL, "gsk_live_EXAMPLE_not_a_real_key", nil, "")
	_, err := c.UploadZip(context.Background(), "brawl", zip, UploadOptions{Storage: f.store.Client(), Parallel: 1, ResumeDir: dir})
	if err == nil || !strings.Contains(err.Error(), "run the same upload again to carry on") {
		t.Fatalf("the first run stops at part 3 and says a re-run carries on: %v", err)
	}
	if f.posts != 1 || f.puts != 4 {
		t.Fatalf("first run: posts %d, puts %d", f.posts, f.puts)
	}

	var seen []UploadProgress
	b, err := c.UploadZip(context.Background(), "brawl", zip, UploadOptions{Storage: f.store.Client(), ResumeDir: dir,
		OnPart: func(p UploadProgress) { seen = append(seen, p) }})
	if err != nil || b["status"] != "processing" {
		t.Fatalf("the re-run completes the same build: %v %v", b, err)
	}
	if f.posts != 1 || f.puts != 5 || f.completes != 1 {
		t.Errorf("no second build, only part 3 sent again: posts %d, puts %d, completes %d", f.posts, f.puts, f.completes)
	}
	want := []UploadedPart{{1, `"etag-1-10"`}, {2, `"etag-2-10"`}, {3, `"etag-3-5"`}}
	if fmt.Sprint(f.completed) != fmt.Sprint(want) {
		t.Errorf("complete gets the first run's parts with the new one: %v", f.completed)
	}
	if len(seen) != 1 || seen[0].Parts != 3 || seen[0].Bytes != 25 || seen[0].Resumed != 20 || seen[0].TotalBytes != 25 {
		t.Errorf("progress counts the parts the first run sent: %+v", seen)
	}
	if left, _ := os.ReadDir(dir); len(left) != 0 {
		t.Errorf("no record left once the build is complete: %v", left)
	}

	// The same zip again: a new upload, since the last one finished.
	f.status = "awaiting_upload"
	if _, err := c.UploadZip(context.Background(), "brawl", zip, UploadOptions{Storage: f.store.Client(), ResumeDir: dir}); err != nil || f.posts != 2 {
		t.Errorf("a finished upload's zip uploads anew: posts %d, %v", f.posts, err)
	}
}

// A record whose build has gone (deleted, or failed) is dropped and a new build is made; one whose build was
// completed since returns that build.
func TestUploadZipRecordOfAGoneOrFinishedBuild(t *testing.T) {
	for _, tc := range []struct {
		status    string
		wantPosts int
	}{{"failed", 1}, {"ready", 0}} {
		f := newFakeUpload(t)
		dir := t.TempDir()
		zip := testZip(t)
		info, _ := InspectZip(zip)
		c, _ := New(f.api.URL, "gsk_live_EXAMPLE_not_a_real_key", nil, "")
		rec := openUploadRecord(dir, c.Base(), "brawl", info)
		rec.begin("bld_1", 10)
		rec.add(UploadedPart{Number: 1, ETag: `"etag-1-10"`})
		if tc.status == "ready" {
			f.status = "ready"
		} else {
			// The kept build reads as failed; the new one the POST makes waits for its upload.
			api := f.api.Config.Handler
			first := true
			f.api.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if first && r.Method == "GET" {
					first = false
					_ = json.NewEncoder(w).Encode(map[string]any{"id": "bld_1", "status": "failed"})
					return
				}
				api.ServeHTTP(w, r)
			})
		}
		b, err := c.UploadZip(context.Background(), "brawl", zip, UploadOptions{Storage: f.store.Client(), ResumeDir: dir})
		if err != nil || f.posts != tc.wantPosts {
			t.Errorf("%s: posts %d (want %d), %v %v", tc.status, f.posts, tc.wantPosts, b, err)
		}
		if tc.status == "ready" && (b["status"] != "ready" || f.puts != 0) {
			t.Errorf("a finished build is returned as it is: %v, puts %d", b, f.puts)
		}
		if tc.status == "failed" && f.puts != 3 {
			t.Errorf("a new build sends every part: puts %d", f.puts)
		}
	}
}
