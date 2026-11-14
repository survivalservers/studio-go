package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// partStore is storage taking parts at /p/<n>?gen=<g>: it keeps each part's bytes and answers its ETag. Links of
// generation 0 for the parts in refuse are refused (403, as a link that ran out).
type partStore struct {
	mu     sync.Mutex
	got    map[int][]byte
	refuse map[int]bool
	always bool // every link refused
}

func (s *partStore) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var n int
	fmt.Sscanf(strings.TrimPrefix(r.URL.Path, "/p/"), "%d", &n)
	b, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.always || (r.URL.Query().Get("gen") == "0" && s.refuse[n]) {
		http.Error(w, "Request has expired", http.StatusForbidden)
		return
	}
	s.got[n] = b
	w.Header().Set("ETag", fmt.Sprintf(`"etag-%d"`, n))
}

func linksAt(base string, total, gen int) map[int]string {
	l := map[int]string{}
	for n := 1; n <= total; n++ {
		l[n] = fmt.Sprintf("%s/p/%d?gen=%d", base, n, gen)
	}
	return l
}

// A save zip in parts: every part PUT with its own bytes, the last what is left, the ETags in number order; links
// refused (run out) are replaced once by reading the upload again, for all the parts that saw them refused.
func TestSendPartsSendsEveryPartAndReplacesRefusedLinksOnce(t *testing.T) {
	data := make([]byte, 10_000_003)
	rand.Read(data)
	path := filepath.Join(t.TempDir(), "saves.zip")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(path)
	defer f.Close()
	st := &partStore{got: map[int][]byte{}, refuse: map[int]bool{2: true, 3: true}}
	srv := httptest.NewServer(st)
	defer srv.Close()
	const part = 2 << 20
	total := (len(data) + part - 1) / part
	var reads atomic.Int32
	fresh := func(context.Context) (map[int]string, error) { reads.Add(1); return linksAt(srv.URL, total, 1), nil }
	var last atomic.Int64
	parts, err := SendParts(context.Background(), f, int64(len(data)), part, linksAt(srv.URL, total, 0), fresh, 3, func(n int64) { last.Store(n) })
	if err != nil {
		t.Fatal(err)
	}
	if len(parts) != total || reads.Load() != 1 || last.Load() != int64(len(data)) {
		t.Fatalf("%d parts of %d, %d reads of the upload, %d bytes reported", len(parts), total, reads.Load(), last.Load())
	}
	for i, p := range parts {
		n := i + 1
		end := min(n*part, len(data))
		if p.Number != n || p.ETag != fmt.Sprintf(`"etag-%d"`, n) || !bytes.Equal(st.got[n], data[(n-1)*part:end]) {
			t.Fatalf("part %d: %+v, %d bytes", n, p, len(st.got[n]))
		}
	}
	// What complete sends: the parts as JSON.
	b, _ := json.Marshal(map[string]any{"parts": parts})
	if !strings.Contains(string(b), `{"number":1,"etag":"\"etag-1\""}`) {
		t.Fatalf("complete body %s", b)
	}

	// Refused again after fresh links: the upload stops with the part named.
	st2 := &partStore{got: map[int][]byte{}, always: true}
	srv2 := httptest.NewServer(st2)
	defer srv2.Close()
	_, err = SendParts(context.Background(), f, int64(len(data)), part, linksAt(srv2.URL, total, 0),
		func(context.Context) (map[int]string, error) { return linksAt(srv2.URL, total, 1), nil }, 2, nil)
	if err == nil || !strings.Contains(err.Error(), "part ") {
		t.Fatalf("every link refused: %v", err)
	}
	// Links that do not match the parts are refused before anything is sent.
	if _, err := SendParts(context.Background(), f, int64(len(data)), part, linksAt(srv.URL, total-1, 0), fresh, 1, nil); err == nil {
		t.Fatal("too few links")
	}
}

func TestPartLinksReadsAnUploadInParts(t *testing.T) {
	var up map[string]any
	json.Unmarshal([]byte(`{"id":"upl_a","upload":{"part_bytes":67108864,"parts":[{"number":1,"url":"https://s/1"},{"number":2,"url":"https://s/2"}],"expires_at":"x"}}`), &up)
	pb, links, ok := PartLinks(up)
	if !ok || pb != 64<<20 || len(links) != 2 || links[2] != "https://s/2" {
		t.Fatalf("%v %d %v", ok, pb, links)
	}
	up = nil
	json.Unmarshal([]byte(`{"id":"upl_b","url":"https://s/zip"}`), &up)
	if _, _, ok := PartLinks(up); ok {
		t.Fatal("one PUT read as parts")
	}
}
