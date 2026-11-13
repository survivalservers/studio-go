package localapi

import (
	"net/http"
	"net/url"
	"testing"
	"time"
)

var fuzzCalls = []struct{ method, path string }{
	{"GET", "/v0/self"}, {"POST", "/v0/self/ready"}, {"POST", "/v0/self/health"}, {"GET", "/v0/self/allocation"},
	{"GET", "/v0/self/shutdown"}, {"PUT", "/v0/self/players"}, {"PATCH", "/v0/self/labels"}, {"POST", "/v0/self/stop"},
	{"POST", "/v0/self/tick"}, {"DELETE", "/v0/self/labels"}, {"GET", "/v0/other"},
}

// FuzzCalls sends whatever a game server could send: any call, any wait, any body, with or without its token.
// Every reply must be one the spec allows, and nothing a call does may take the record past the limits.
func FuzzCalls(f *testing.F) {
	for i, body := range []string{"", `{"ids":["p1","p2"]}`, `{"count":3}`, `{"a":"b","c":null}`, `{"reason":"over"}`,
		`{"p99_ms":4.5,"p50_ms":1,"hz":64}`, `{"ids":["p1","p1"]}`, `{"count":-1}`, `{"hz":1e999}`, "[]", "null", `{"ids":null}`,
		"\xff\xfe", `{"` + "\u0000" + `":"x"}`} {
		f.Add(uint8(i), "", []byte(body), true)
		f.Add(uint8(i+5), "5ms", []byte(body), true)
	}
	f.Add(uint8(3), "1.5m", []byte(nil), true)
	f.Add(uint8(3), "99999999999999999999h", []byte(nil), true)
	f.Add(uint8(0), "", []byte(nil), false)
	f.Fuzz(func(t *testing.T, which uint8, wait string, body []byte, auth bool) {
		h := newHarness(t, Options{MaxWait: 2 * time.Millisecond, Rate: 1e9, Burst: 1 << 30})
		in, tok := h.register("gs_fuzz", which&0x80 != 0)
		in.MarkReady()
		c := fuzzCalls[int(which&0x7f)%len(fuzzCalls)]
		target := c.path
		if wait != "" {
			target += "?wait=" + url.QueryEscape(wait)
		}
		if !auth {
			tok = "not-the-token-0123456789"
		}
		for i := 0; i < 2; i++ { // twice: the second sees what the first left
			rec := h.call(tok, c.method, target, string(body), func(r *http.Request) {})
			if !auth && rec.Code != http.StatusUnauthorized && rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("no token: %d", rec.Code)
			}
		}
		self := in.Self()
		if len(self.Labels) > maxLabels || len(self.Players.IDs) > maxPlayerIDs || self.Players.Count > maxPlayerCount ||
			self.Players.Count < 0 {
			t.Fatalf("record past the limits: %d labels, %d IDs, count %d", len(self.Labels), len(self.Players.IDs), self.Players.Count)
		}
	})
}

// FuzzWait: any wait value is refused or capped, never more than the longest wait.
func FuzzWait(f *testing.F) {
	for _, s := range []string{"", "0", "30s", "1.5m", "500ms", "61s", "9999999999999999999m", "1e3", "-1s", ".5s"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		d, err := parseWait(s, DefaultMaxWait)
		if err == nil && (d < 0 || d > DefaultMaxWait) {
			t.Fatalf("%q: %v", s, d)
		}
	})
}
