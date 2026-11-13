package localapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type harness struct {
	t      *testing.T
	s      *Server
	d      *specDoc
	mu     sync.Mutex
	events []Event
}

func newHarness(t *testing.T, opt Options) *harness {
	h := &harness{t: t, d: loadSpec(t)}
	opt.OnEvent = func(e Event) {
		h.mu.Lock()
		h.events = append(h.events, e)
		h.mu.Unlock()
	}
	h.s = NewServer(opt)
	return h
}

func (h *harness) register(id string, warm bool) (*Instance, string) {
	h.t.Helper()
	tok := "tok-" + id + "-0123456789abcdef"
	in, err := h.s.Register(Registration{Token: tok, Warm: warm, Self: Self{
		ID: id, Kind: "match", App: "brawl-server", Version: "1.4.2", Region: "us-central", Continent: "north-america",
		PublicIP: "203.0.113.24", Ports: map[string]Port{"game": {Port: 31042, Protocol: "udp"}},
	}})
	if err != nil {
		h.t.Fatal(err)
	}
	return in, tok
}

// call makes one request and checks the reply against the spec.
func (h *harness) call(tok, method, target, body string, mod ...func(*http.Request)) *httptest.ResponseRecorder {
	h.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, target, rd)
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	for _, f := range mod {
		f(r)
	}
	rec := httptest.NewRecorder()
	h.s.ServeHTTP(rec, r)
	h.d.check(h.t, method, r.URL.Path, rec)
	return rec
}

func (h *harness) eventsOf(typ EventType) []Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []Event
	for _, e := range h.events {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

func want(t *testing.T, rec *httptest.ResponseRecorder, code int) {
	t.Helper()
	if rec.Code != code {
		t.Fatalf("got %d, want %d: %s", rec.Code, code, rec.Body.String())
	}
}

func wantCode(t *testing.T, rec *httptest.ResponseRecorder, code int, errCode string) {
	t.Helper()
	want(t, rec, code)
	var p struct {
		Code string `json:"code"`
	}
	json.Unmarshal(rec.Body.Bytes(), &p)
	if p.Code != errCode {
		t.Fatalf("error code %q, want %q", p.Code, errCode)
	}
}

func decodeSelf(t *testing.T, rec *httptest.ResponseRecorder) Self {
	t.Helper()
	var s Self
	if err := json.Unmarshal(rec.Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	return s
}

// asyncCall runs a (long-poll) call in the background.
func (h *harness) asyncCall(tok, method, target string) <-chan *httptest.ResponseRecorder {
	ch := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		r := httptest.NewRequest(method, target, nil)
		r.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		h.s.ServeHTTP(rec, r)
		ch <- rec
	}()
	return ch
}

func waitPolls(t *testing.T, in *Instance, kind string, n int) {
	t.Helper()
	for end := time.Now().Add(2 * time.Second); time.Now().Before(end); time.Sleep(time.Millisecond) {
		if in.openPolls(kind) == n {
			return
		}
	}
	t.Fatalf("%s polls never reached %d", kind, n)
}

func recv(t *testing.T, ch <-chan *httptest.ResponseRecorder, within time.Duration) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case rec := <-ch:
		return rec
	case <-time.After(within):
		t.Fatalf("no reply within %v", within)
		return nil
	}
}

func TestColdStart(t *testing.T) {
	h := newHarness(t, Options{})
	in, tok := h.register("gs_cold", false)

	s := decodeSelf(t, h.call(tok, "GET", "/v0/self", ""))
	if s.Status != StatusStarting || s.AllocatedAt != nil || s.Labels == nil || s.Ports["game"].Port != 31042 {
		t.Fatalf("fresh record: %+v", s)
	}
	// A cold start is allocated before its process starts.
	exp := time.Now().Add(2 * time.Hour)
	if err := in.Allocate(Allocation{ExternalID: "88231", Labels: map[string]string{"queue": "ranked"},
		Env: map[string]string{"MATCH_MODE": "ranked"}, Args: []string{"-MaxPlayers=10"}, ExpiresAt: &exp}); err != nil {
		t.Fatal(err)
	}
	if err := in.Allocate(Allocation{}); !errors.Is(err, ErrAlreadyAllocated) {
		t.Fatalf("second allocate: %v", err)
	}
	rec := h.call(tok, "GET", "/v0/self/allocation", "")
	want(t, rec, 200)
	var a Allocation
	json.Unmarshal(rec.Body.Bytes(), &a)
	if a.GameServerID != "gs_cold" || a.Env["MATCH_MODE"] != "ranked" || a.ExternalID != "88231" || a.ExpiresAt == nil {
		t.Fatalf("allocation: %s", rec.Body.String())
	}

	want(t, h.call(tok, "POST", "/v0/self/ready", ""), 204)
	if len(h.eventsOf(EventReady)) != 1 {
		t.Fatal("no ready event")
	}
	if s := in.Self(); s.Status != StatusStarting {
		t.Fatalf("the agent decides readiness; got %s before MarkReady", s.Status)
	}
	in.MarkReady()
	s = decodeSelf(t, h.call(tok, "GET", "/v0/self", ""))
	if s.Status != StatusRunning || s.AllocatedAt == nil || s.Labels["queue"] != "ranked" {
		t.Fatalf("after ready: %+v", s)
	}
	want(t, h.call(tok, "POST", "/v0/self/health", ""), 204)
	if len(h.eventsOf(EventHealth)) != 1 {
		t.Fatal("no health event")
	}
}

func TestWarmAllocationLongPoll(t *testing.T) {
	h := newHarness(t, Options{})
	in, tok := h.register("gs_warm", true)
	want(t, h.call(tok, "PATCH", "/v0/self/labels", `{"pool":"a"}`), 200)
	in.MarkReady()
	if in.Self().Status != StatusWarm {
		t.Fatalf("status %s, want warm", in.Self().Status)
	}
	want(t, h.call(tok, "GET", "/v0/self/allocation", ""), 204) // no wait: answers at once

	ch := h.asyncCall(tok, "GET", "/v0/self/allocation?wait=10s")
	waitPolls(t, in, "allocation", 1)
	t0 := time.Now()
	in.Allocate(Allocation{Labels: map[string]string{"match_id": "1"}})
	rec := recv(t, ch, time.Second)
	h.d.check(t, "GET", "/v0/self/allocation", rec)
	want(t, rec, 200)
	if d := time.Since(t0); d > time.Second {
		t.Errorf("long-poll answered %v after allocation", d)
	}
	s := in.Self()
	if s.Status != StatusRunning || s.Labels["match_id"] != "1" || s.Labels["pool"] != "a" {
		t.Fatalf("after allocation: %+v", s)
	}
	if in.openPolls("allocation") != 0 {
		t.Fatal("poll count not released")
	}
}

func TestLongPollTimeoutsAndCap(t *testing.T) {
	h := newHarness(t, Options{MaxWait: 150 * time.Millisecond})
	_, tok := h.register("gs_wait", false)
	for _, w := range []string{"50ms", "0.05s", "1"} {
		t0 := time.Now()
		rec := h.call(tok, "GET", "/v0/self/shutdown?wait="+w, "")
		want(t, rec, 204)
		if d := time.Since(t0); d < 45*time.Millisecond || d > 3*time.Second {
			t.Errorf("wait=%s took %v", w, d)
		}
	}
	t0 := time.Now()
	want(t, h.call(tok, "GET", "/v0/self/allocation?wait=10m", ""), 204) // capped at MaxWait
	if d := time.Since(t0); d > 3*time.Second {
		t.Errorf("wait was not capped: %v", d)
	}
	for _, bad := range []string{"abc", "1h", "-1s", "5 s"} {
		wantCode(t, h.call(tok, "GET", "/v0/self/allocation?wait="+strings.ReplaceAll(bad, " ", "%20"), ""), 422, "validation_failed")
		wantCode(t, h.call(tok, "GET", "/v0/self/shutdown?wait="+strings.ReplaceAll(bad, " ", "%20"), ""), 422, "validation_failed")
	}
}

func TestShutdown(t *testing.T) {
	h := newHarness(t, Options{})
	in, tok := h.register("gs_sd", true)
	alloc := h.asyncCall(tok, "GET", "/v0/self/allocation?wait=10s")
	sd := h.asyncCall(tok, "GET", "/v0/self/shutdown?wait=10s")
	waitPolls(t, in, "allocation", 1)
	waitPolls(t, in, "shutdown", 1)

	now := time.Now()
	in.RequestShutdown(Shutdown{Deadline: now.Add(30 * time.Second), Reason: ReasonAPI})
	in.RequestShutdown(Shutdown{Deadline: now.Add(60 * time.Second), Reason: ReasonTTL}) // later: ignored
	in.RequestShutdown(Shutdown{Deadline: now.Add(10 * time.Second), Reason: ReasonTTL}) // earlier: taken

	rec := recv(t, alloc, time.Second)
	h.d.check(t, "GET", "/v0/self/allocation", rec)
	want(t, rec, 204) // the allocation poll ends early so a one-loop game notices the stop
	rec = recv(t, sd, time.Second)
	h.d.check(t, "GET", "/v0/self/shutdown", rec)
	want(t, rec, 200)
	rec = h.call(tok, "GET", "/v0/self/shutdown", "")
	var got Shutdown
	json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Reason != ReasonAPI || got.Deadline.Sub(now) > 11*time.Second || got.Deadline.Sub(now) < 9*time.Second {
		t.Fatalf("shutdown %+v", got)
	}
	if s := in.Self(); s.Status != StatusStopping || s.ShutdownRequestedAt == nil {
		t.Fatalf("status %+v", s)
	}
	wantCode(t, h.call(tok, "POST", "/v0/self/ready", ""), 409, "invalid_state")
}

// A game that asks for the notice exits on it (the agent then holds back the stop signal); a compatibility layer's own
// poll (relay=true, compat's Agones watch) is not the game asking, so its game gets the signal with the notice.
func TestOnlyTheGamesOwnNoticePollCountsAsWaiting(t *testing.T) {
	h := newHarness(t, Options{})
	relayed, rtok := h.register("gs_relayed", false)
	own, otok := h.register("gs_own", false)
	for _, q := range []string{"?relay=true", "?wait=50ms&relay=true"} {
		rec := h.call(rtok, "GET", "/v0/self/shutdown"+q, "")
		h.d.check(t, "GET", "/v0/self/shutdown", rec)
		want(t, rec, 204)
	}
	if relayed.WatchesShutdown() {
		t.Fatal("a relayed poll counted as the game waiting for the notice")
	}
	wantCode(t, h.call(rtok, "GET", "/v0/self/shutdown?relay=yes", ""), 422, "validation_failed")
	if relayed.WatchesShutdown() {
		t.Fatal("a refused poll counted as the game waiting for the notice")
	}
	want(t, h.call(otok, "GET", "/v0/self/shutdown?relay=false", ""), 204)
	if !own.WatchesShutdown() {
		t.Fatal("the game's own poll did not count")
	}
	want(t, h.call(rtok, "GET", "/v0/self/shutdown", ""), 204) // the game itself asks after all
	if !relayed.WatchesShutdown() {
		t.Fatal("the game's own poll after a relayed one did not count")
	}
}

func TestSelfStop(t *testing.T) {
	h := newHarness(t, Options{})
	_, tok := h.register("gs_stop", false)
	rec := h.call(tok, "POST", "/v0/self/stop", `{"reason":"match_over","future_field":1}`)
	want(t, rec, 202)
	want(t, h.call(tok, "POST", "/v0/self/stop", ""), 202) // again, no body: fine
	ev := h.eventsOf(EventStop)
	if len(ev) != 1 || ev[0].Reason != "match_over" {
		t.Fatalf("stop events %+v", ev)
	}
	wantCode(t, h.call(tok, "POST", "/v0/self/ready", ""), 409, "invalid_state")
	wantCode(t, h.call(tok, "POST", "/v0/self/stop", `{"reason":"`+strings.Repeat("x", 129)+`"}`), 422, "validation_failed")
	wantCode(t, h.call(tok, "POST", "/v0/self/stop", `{"reason":5}`), 422, "validation_failed")
}

// A game can say it saved, every 10 seconds at most, and keep a save once a minute: a loop gets a plain refusal.
func TestSaved(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	h := newHarness(t, Options{Now: func() time.Time { return now }})
	_, match := h.register("gs_match", false)
	wantCode(t, h.call(match, "POST", "/v0/self/saved", ""), 409, "invalid_state")

	tok := "tok-gs_world-0123456789abcdef"
	if _, err := h.s.Register(Registration{Token: tok, Self: Self{ID: "gs_world", Kind: KindPersistentServer, App: "a"}}); err != nil {
		t.Fatal(err)
	}
	want(t, h.call(tok, "POST", "/v0/self/saved", ""), 202)
	rec := h.call(tok, "POST", "/v0/self/saved", "")
	wantCode(t, rec, 429, "rate_limited")
	if rec.Header().Get("Retry-After") != "10" {
		t.Errorf("Retry-After %q", rec.Header().Get("Retry-After"))
	}
	now = now.Add(10 * time.Second)
	want(t, h.call(tok, "POST", "/v0/self/saved", `{"keep":"Season 3 end"}`), 202)
	now = now.Add(10 * time.Second)
	rec = h.call(tok, "POST", "/v0/self/saved", `{"keep":"Season 4 end"}`)
	wantCode(t, rec, 429, "rate_limited")
	if !strings.Contains(rec.Body.String(), "A save can be kept once a minute") {
		t.Errorf("the refusal says %s", rec.Body)
	}
	want(t, h.call(tok, "POST", "/v0/self/saved", ""), 202) // a plain one is still let through
	now = now.Add(time.Minute)
	want(t, h.call(tok, "POST", "/v0/self/saved", `{"keep":"Season 4 end"}`), 202)
	wantCode(t, h.call(tok, "POST", "/v0/self/saved", `{"keep":""}`), 422, "validation_failed")
	wantCode(t, h.call(tok, "POST", "/v0/self/saved", `{"keep":"`+strings.Repeat("x", 101)+`"}`), 422, "validation_failed")
	wantCode(t, h.call(tok, "POST", "/v0/self/saved", `{"keep":"a\u0000b"}`), 422, "validation_failed")
	ev := h.eventsOf(EventSaved)
	if len(ev) != 4 || ev[0].Keep != "" || ev[1].Keep != "Season 3 end" || ev[2].Keep != "" || ev[3].Keep != "Season 4 end" {
		t.Fatalf("saved events %+v", ev)
	}
}

func TestPlayersWithPositions(t *testing.T) {
	h := newHarness(t, Options{})
	_, tok := h.register("gs_1", false)
	rec := h.call(tok, "PUT", "/v0/self/players", `{"players":[{"id":"p_1","name":"Vex_77","x":12.5,"y":-3,"team":"red","ping_ms":34},{"id":"p_2"}]}`)
	want(t, rec, 200)
	var got Players
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Count != 2 || len(got.IDs) != 2 || got.IDs[0] != "p_1" || len(got.Info) != 2 || got.Info[0].Name != "Vex_77" || *got.Info[0].X != 12.5 || *got.Info[0].PingMS != 34 {
		t.Fatalf("got %+v", got)
	}
	h.mu.Lock()
	last := h.events[len(h.events)-1]
	h.mu.Unlock()
	if last.Type != EventPlayers || len(last.Players.Info) != 2 {
		t.Fatalf("event %+v", last)
	}
	for _, body := range []string{`{"players":[{"id":"a"},{"id":"a"}]}`, `{"players":[{"name":"no id"}]}`, `{"players":[],"count":1}`, `{"players":[{"id":"a"}],"ids":["a"]}`} {
		wantCode(t, h.call(tok, "PUT", "/v0/self/players", body), 422, "validation_failed")
	}
}

func TestPlayers(t *testing.T) {
	h := newHarness(t, Options{})
	_, tok := h.register("gs_players", false)
	rec := h.call(tok, "PUT", "/v0/self/players", `{"ids":["p_1","p_2"]}`)
	want(t, rec, 200)
	if s := decodeSelf(t, h.call(tok, "GET", "/v0/self", "")); s.Players.Count != 2 || len(s.Players.IDs) != 2 {
		t.Fatalf("players %+v", s.Players)
	}
	rec = h.call(tok, "PUT", "/v0/self/players", `{"ids":[]}`)
	want(t, rec, 200)
	if s := decodeSelf(t, h.call(tok, "GET", "/v0/self", "")); s.Players.Count != 0 {
		t.Fatalf("empty list: %+v", s.Players)
	}
	rec = h.call(tok, "PUT", "/v0/self/players", `{"count":7}`)
	want(t, rec, 200)
	if strings.Contains(rec.Body.String(), "ids") {
		t.Fatalf("count-only reply has ids: %s", rec.Body.String())
	}
	if ev := h.eventsOf(EventPlayers); len(ev) != 3 || ev[2].Players.Count != 7 {
		t.Fatalf("player events %+v", ev)
	}
	long := strings.Repeat("x", 129)
	many := `{"ids":[` + strings.TrimSuffix(strings.Repeat(`"a",`, 1001), ",") + `]}`
	for _, body := range []string{`{"ids":["a"],"count":1}`, `{}`, `{"ids":["a","a"]}`, `{"ids":[""]}`,
		`{"ids":["` + long + `"]}`, `{"count":-1}`, `{"count":1.5}`, `{"count":100001}`, many, `[1]`, `not json`} {
		wantCode(t, h.call(tok, "PUT", "/v0/self/players", body), 422, "validation_failed")
	}
	wantCode(t, h.call(tok, "PUT", "/v0/self/players", ""), 422, "validation_failed")
}

func TestLabels(t *testing.T) {
	h := newHarness(t, Options{})
	_, tok := h.register("gs_labels", false)
	rec := h.call(tok, "PATCH", "/v0/self/labels", `{"map":"desert","mode":"ctf"}`,
		func(r *http.Request) { r.Header.Set("Content-Type", "application/merge-patch+json") })
	want(t, rec, 200)
	rec = h.call(tok, "PATCH", "/v0/self/labels", `{"mode":null,"gone":null,"round":"2"}`)
	want(t, rec, 200)
	var got struct {
		Labels map[string]string `json:"labels"`
	}
	json.Unmarshal(rec.Body.Bytes(), &got)
	if len(got.Labels) != 2 || got.Labels["map"] != "desert" || got.Labels["round"] != "2" {
		t.Fatalf("labels %v", got.Labels)
	}
	if ev := h.eventsOf(EventLabels); len(ev) != 2 || len(ev[1].Labels) != 2 {
		t.Fatalf("label events %+v", ev)
	}
	var b strings.Builder
	b.WriteString("{")
	for i := 0; i < 31; i++ {
		fmt.Fprintf(&b, `"k%d":"v",`, i)
	}
	b.WriteString(`"last":"v"}`)
	wantCode(t, h.call(tok, "PATCH", "/v0/self/labels", b.String()), 422, "validation_failed") // 2 + 32 > 32
	for _, body := range []string{`{"Upper":"x"}`, `{"":"x"}`, `{"k":"` + strings.Repeat("v", 257) + `"}`, `{"k":1}`, `["k"]`, `null`} {
		wantCode(t, h.call(tok, "PATCH", "/v0/self/labels", body), 422, "validation_failed")
	}

	// At most 64 keys in one call (the spec's LabelsPatch), removals included, whatever the labels add up to after.
	// 63 removals of names that are not set, plus the map's removal: 64 keys pass, one more is refused whole.
	patch := func(extra int) string {
		var b strings.Builder
		b.WriteString(`{"map":null`)
		for i := 0; i < 63+extra; i++ {
			fmt.Fprintf(&b, `,"gone%d":null`, i)
		}
		b.WriteString("}")
		return b.String()
	}
	wantCode(t, h.call(tok, "PATCH", "/v0/self/labels", patch(1)), 422, "validation_failed")
	var after struct {
		Labels map[string]string `json:"labels"`
	}
	json.Unmarshal(h.call(tok, "GET", "/v0/self", "").Body.Bytes(), &after)
	if after.Labels["map"] != "desert" || len(after.Labels) != 2 {
		t.Fatalf("labels after the refused patch: %v", after.Labels)
	}
	rec = h.call(tok, "PATCH", "/v0/self/labels", patch(0))
	want(t, rec, 200)
	after.Labels = nil
	json.Unmarshal(rec.Body.Bytes(), &after)
	if _, has := after.Labels["map"]; has || len(after.Labels) != 1 {
		t.Fatalf("labels after a 64-key patch: %v", after.Labels)
	}
}

func TestTick(t *testing.T) {
	h := newHarness(t, Options{})
	_, tok := h.register("gs_tick", false)
	want(t, h.call(tok, "POST", "/v0/self/tick", `{"p50_ms":2.1,"p99_ms":3.9,"hz":128}`), 204)
	want(t, h.call(tok, "POST", "/v0/self/tick", `{"p99_ms":0}`), 204)
	ev := h.eventsOf(EventTick)
	if len(ev) != 2 || ev[0].Tick.Hz != 128 || *ev[0].Tick.P50ms != 2.1 || ev[1].Tick.P50ms != nil {
		t.Fatalf("tick events %+v", ev)
	}
	for _, body := range []string{`{}`, `{"p99_ms":-1}`, `{"p99_ms":1,"p50_ms":1e9}`, `{"p99_ms":1,"hz":0}`, `{"p99_ms":1,"hz":60.5}`, `{"p99_ms":"3"}`} {
		wantCode(t, h.call(tok, "POST", "/v0/self/tick", body), 422, "validation_failed")
	}
}

func TestAuth(t *testing.T) {
	h := newHarness(t, Options{})
	a, tokA := h.register("gs_a", false)
	_, tokB := h.register("gs_b", false)
	wantCode(t, h.call("", "GET", "/v0/self", ""), 401, "unauthenticated")
	wantCode(t, h.call("wrong-token-0123456789", "GET", "/v0/self", ""), 401, "unauthenticated")
	rec := h.call("", "GET", "/v0/self", "", func(r *http.Request) { r.Header.Set("Authorization", "bearer "+tokB) })
	want(t, rec, 200)
	if decodeSelf(t, rec).ID != "gs_b" {
		t.Fatal("token B saw another record")
	}
	if decodeSelf(t, h.call(tokA, "GET", "/v0/self", "")).ID != "gs_a" {
		t.Fatal("token A saw another record")
	}

	// Unregister: an open poll answers 204, later calls 401.
	ch := h.asyncCall(tokA, "GET", "/v0/self/allocation?wait=10s")
	waitPolls(t, a, "allocation", 1)
	h.s.Unregister("gs_a")
	if rec := recv(t, ch, time.Second); rec.Code != 204 {
		t.Fatalf("poll after unregister: %d", rec.Code)
	}
	wantCode(t, h.call(tokA, "GET", "/v0/self", ""), 401, "unauthenticated")

	// A Linux container's calls must come from its own address.
	tokC := "tok-container-0123456789"
	if _, err := h.s.Register(Registration{Token: tokC, Self: Self{ID: "gs_c", Kind: KindPersistentServer}, From: netip.MustParsePrefix("192.0.2.10/32")}); err != nil {
		t.Fatal(err)
	}
	from := func(addr string) func(*http.Request) { return func(r *http.Request) { r.RemoteAddr = addr } }
	want(t, h.call(tokC, "GET", "/v0/self", "", from("192.0.2.10:40000")), 200)
	wantCode(t, h.call(tokC, "GET", "/v0/self", "", from("192.0.2.11:40000")), 401, "unauthenticated")
	wantCode(t, h.call(tokC, "GET", "/v0/self", "", from("garbage")), 401, "unauthenticated")

	if _, err := h.s.Register(Registration{Token: "valid-token-but-no-kind-0123", Self: Self{ID: "gs_k"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no kind: %v", err)
	}
	if _, err := h.s.Register(Registration{Token: "short", Self: Self{ID: "x", Kind: "match"}}); !errors.Is(err, ErrNoToken) {
		t.Fatalf("short token: %v", err)
	}
	if _, err := h.s.Register(Registration{Token: tokB, Self: Self{ID: "other", Kind: "match"}}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate token: %v", err)
	}
	if _, err := h.s.Register(Registration{Token: "another-token-0123456789", Self: Self{ID: "gs_b", Kind: "match"}}); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate id: %v", err)
	}
}

// Calls counts every call a game server made with its own token, an unknown path and a rate-limited one included, and
// none made without it: the agent's "not ready" line tells a game that never found the local API from one that never
// called ready by it.
func TestCallsCountsEveryCallWithItsOwnToken(t *testing.T) {
	h := newHarness(t, Options{Rate: 0.001, Burst: 2})
	a, tokA := h.register("gs_a", false)
	b, _ := h.register("gs_b", false)
	if a.Calls() != 0 {
		t.Fatalf("calls before any: %d", a.Calls())
	}
	wantCode(t, h.call("", "GET", "/v0/self", ""), 401, "unauthenticated")
	wantCode(t, h.call("wrong-token-0123456789", "POST", "/v0/self/ready", ""), 401, "unauthenticated")
	wantCode(t, h.call("wrong-token-0123456789", "GET", "/v0/nope", ""), 404, "not_found")
	if a.Calls() != 0 || b.Calls() != 0 {
		t.Fatalf("calls without the token counted: a %d, b %d", a.Calls(), b.Calls())
	}
	want(t, h.call(tokA, "GET", "/v0/self", ""), 200)
	wantCode(t, h.call(tokA, "GET", "/v0/nope", ""), 404, "not_found")
	wantCode(t, h.call(tokA, "DELETE", "/v0/self", ""), 405, "bad_request")
	want(t, h.call(tokA, "POST", "/v0/self/health", ""), 204)
	wantCode(t, h.call(tokA, "POST", "/v0/self/health", ""), 429, "rate_limited")
	if a.Calls() != 5 || b.Calls() != 0 {
		t.Fatalf("calls: a %d (want 5), b %d (want 0)", a.Calls(), b.Calls())
	}
}

func TestRoutingErrors(t *testing.T) {
	h := newHarness(t, Options{})
	_, tok := h.register("gs_r", false)
	wantCode(t, h.call(tok, "GET", "/v0/nope", ""), 404, "not_found")
	rec := h.call(tok, "DELETE", "/v0/self", "")
	wantCode(t, rec, 405, "bad_request")
	if rec.Header().Get("Allow") != "GET" {
		t.Fatalf("Allow %q", rec.Header().Get("Allow"))
	}
}

// TestLimits produces 429 and 413 for every call that declares them.
func TestLimits(t *testing.T) {
	h := newHarness(t, Options{Rate: 0.001, Burst: 1})
	calls := []struct{ method, path, body string }{
		{"GET", "/v0/self", ""}, {"POST", "/v0/self/ready", ""}, {"POST", "/v0/self/health", ""},
		{"GET", "/v0/self/allocation", ""}, {"GET", "/v0/self/shutdown", ""},
		{"PUT", "/v0/self/players", `{"count":1}`}, {"PATCH", "/v0/self/labels", `{"a":"b"}`},
		{"POST", "/v0/self/stop", `{"reason":"x"}`}, {"POST", "/v0/self/tick", `{"p99_ms":1}`}, {"POST", "/v0/self/saved", `{"keep":"x"}`},
	}
	big := `{"ids":["` + strings.Repeat("x", MaxBodyBytes) + `"]}`
	for i, c := range calls {
		_, tok := h.register(fmt.Sprintf("gs_lim%d", i), false)
		if c.body != "" {
			// 413 uses up the one call in the bucket.
			wantCode(t, h.call(tok, c.method, c.path, big), 413, "too_large")
		} else {
			h.call(tok, c.method, c.path, c.body)
		}
		rec := h.call(tok, c.method, c.path, c.body)
		wantCode(t, rec, 429, "rate_limited")
		if rec.Header().Get("Retry-After") == "" {
			t.Fatal("no Retry-After")
		}
	}

	// At most MaxPolls open long-polls of each kind.
	h2 := newHarness(t, Options{MaxPolls: 2})
	in, tok := h2.register("gs_polls", false)
	a := h2.asyncCall(tok, "GET", "/v0/self/shutdown?wait=10s")
	b := h2.asyncCall(tok, "GET", "/v0/self/shutdown?wait=10s")
	waitPolls(t, in, "shutdown", 2)
	wantCode(t, h2.call(tok, "GET", "/v0/self/shutdown?wait=10s", ""), 429, "rate_limited")
	want(t, h2.call(tok, "GET", "/v0/self/allocation", ""), 204) // the other kind is not affected
	in.RequestShutdown(Shutdown{Deadline: time.Now().Add(time.Second), Reason: ReasonAPI})
	recv(t, a, time.Second)
	recv(t, b, time.Second)
}

// TestOverRealHTTP runs the long-poll through a real listener with the server's own timeouts.
func TestOverRealHTTP(t *testing.T) {
	s := NewServer(Options{})
	ts := httptest.NewUnstartedServer(s)
	ts.Config = s.NewHTTPServer("")
	ts.Start()
	defer ts.Close()
	in, _ := s.Register(Registration{Token: "real-http-token-0123456789", Self: Self{ID: "gs_http", Kind: "match"}, Warm: true})
	in.MarkReady()
	done := make(chan *http.Response, 1)
	go func() {
		req, _ := http.NewRequest("GET", ts.URL+"/v0/self/allocation?wait=5s", nil)
		req.Header.Set("Authorization", "Bearer real-http-token-0123456789")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Error(err)
			close(done)
			return
		}
		done <- resp
	}()
	waitPolls(t, in, "allocation", 1)
	in.Allocate(Allocation{Args: []string{"-x"}})
	select {
	case resp := <-done:
		if resp == nil {
			return
		}
		defer resp.Body.Close()
		var a Allocation
		if err := json.NewDecoder(resp.Body).Decode(&a); err != nil || resp.StatusCode != 200 || a.GameServerID != "gs_http" || a.Args[0] != "-x" {
			t.Fatalf("status %d, %+v, %v", resp.StatusCode, a, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no reply")
	}
}

// GS_AGENT_URL, the local API's old name, has an end: removed on 2026-12-01, or when the beta opens, whichever is
// first, once every live build is newer than the rename of 2026-11-07. From that day this test fails while Env still hands it
// out, so it cannot be forgotten. To remove it: take EnvAgentURLOld out of Env's map (LocalAPIURLFromEnv may keep
// reading it: that costs nothing and hands nothing out), then the old-name checks in TestEnv and in the agent's own
// test of a build that reads only the old name, then this test. A new date needs the same review as this one, and every
// place that states the date moves with it (env.go and the project's records).
func TestTheOldLocalAPINameHasAnEnd(t *testing.T) {
	ends := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	handed := func(env []string) bool {
		for _, kv := range env {
			if strings.HasPrefix(kv, EnvAgentURLOld+"=") {
				return true
			}
		}
		return false
	}
	overdue := func(env []string, now time.Time) bool { return handed(env) && !now.Before(ends) }
	env := Env(EnvInput{Self: Self{ID: "gs_1"}, LocalAPIURL: "http://127.0.0.1:7600"})
	// The check itself, either side of the date: a second before passes, the day itself fails.
	if handed(env) && (overdue(env, ends.Add(-time.Second)) || !overdue(env, ends)) {
		t.Fatal("the end-date check is wrong")
	}
	switch {
	case overdue(env, time.Now()):
		t.Fatalf("%s, the local API's old name, is still handed to every game server on or after %s, its end date: "+
			"remove it from Env now (the steps are above this test), or have a new date reviewed", EnvAgentURLOld, ends.Format("2006-01-02"))
	case handed(env):
		t.Logf("%s is handed out until %s (%d days left)", EnvAgentURLOld, ends.Format("2006-01-02"), int(time.Until(ends).Hours()/24))
	default:
		t.Logf("%s is no longer handed out: this test can go", EnvAgentURLOld)
	}
}

func TestEnv(t *testing.T) {
	env := Env(EnvInput{
		Self: Self{ID: "gs_1", Kind: "match", App: "brawl", Version: "1.0", Region: "us-central", Continent: "north-america",
			PublicIP: "203.0.113.24", Ports: map[string]Port{"game": {Port: 31042, Protocol: "udp", Internal: 7777}, "query-port": {Port: 31043, Protocol: "udp"}}},
		CPU: 0.5, MemoryMB: 2048, LocalAPIURL: "http://192.0.2.1:7600", Token: "t", AllocationFile: "/gs/.gs/allocation.json",
	})
	got := strings.Join(env, "\n")
	for _, w := range []string{"GS_ID=gs_1", "GS_CPU=0.5", "GS_MEMORY_MB=2048", "GS_PORT_GAME=31042", "GS_PORT_GAME_INTERNAL=7777",
		"GS_PORT_QUERY_PORT=31043", "GS_LOCAL_API_URL=http://192.0.2.1:7600", "GS_AGENT_URL=http://192.0.2.1:7600",
		"GS_ALLOCATION_FILE=/gs/.gs/allocation.json"} {
		if !strings.Contains(got, w+"\n") && !strings.HasSuffix(got, w) {
			t.Errorf("missing %s in\n%s", w, got)
		}
	}
	if strings.Contains(got, "GS_PORT_QUERY_PORT_INTERNAL") || strings.Contains(got, "GS_LOG_DIR") {
		t.Errorf("empty values should be left out:\n%s", got)
	}
	if none := strings.Join(Env(EnvInput{Self: Self{ID: "gs_2"}}), "\n"); strings.Contains(none, "API_URL") || strings.Contains(none, "GS_AGENT_URL") {
		t.Errorf("no local API address given, yet:\n%s", none)
	}
	for name, reserved := range map[string]bool{"GS_ID": true, "gs_custom": true, "Gs_x": true, "GSX": false, "MY_GS_ID": false, "GS": false} {
		if IsReserved(name) != reserved {
			t.Errorf("IsReserved(%q) = %v", name, !reserved)
		}
	}
}

func TestAllocationFile(t *testing.T) {
	d := loadSpec(t)
	dir := t.TempDir()
	p := filepath.Join(dir, ".gs", "allocation.json")
	if err := WriteAllocationFile(p, Allocation{GameServerID: "gs_f", AllocatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	a, err := ReadAllocationFile(p)
	if err != nil || a.GameServerID != "gs_f" || a.Env == nil || a.Args == nil {
		t.Fatalf("%+v %v", a, err)
	}
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Fatalf("temporary files left behind: %v", entries)
	}
	b, _ := os.ReadFile(p)
	inst, _ := jsonUnmarshal(b)
	if err := d.schema(t, "/components/schemas/Allocation").Validate(inst); err != nil {
		t.Fatalf("allocation file does not match the spec: %v\n%s", err, b)
	}
}

// TestAllocationSecretsStayOffDisk: secret values reach the game through the allocation call only.
func TestAllocationSecretsStayOffDisk(t *testing.T) {
	h := newHarness(t, Options{})
	in, tok := h.register("gs_secret", true)
	a := Allocation{Env: map[string]string{"MATCH_MODE": "ranked"}, SecretEnv: map[string]string{"MATCH_KEY": "hunter2"}}
	p := filepath.Join(t.TempDir(), ".gs", "allocation.json")
	if err := WriteAllocationFile(p, a); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); strings.Contains(string(b), "hunter2") || strings.Contains(string(b), "MATCH_KEY") {
		t.Fatalf("secret written to the allocation file:\n%s", b)
	}
	if err := in.Allocate(a); err != nil {
		t.Fatal(err)
	}
	a.SecretEnv["MATCH_KEY"] = "changed after" // the server keeps its own copy
	rec := h.call(tok, "GET", "/v0/self/allocation", "")
	want(t, rec, 200)
	var got Allocation
	json.Unmarshal(rec.Body.Bytes(), &got)
	if got.Env["MATCH_KEY"] != "hunter2" || got.Env["MATCH_MODE"] != "ranked" {
		t.Fatalf("allocation reply env %v", got.Env)
	}
	// A compatibility layer passing the match on as another platform's settings gets no secret value (relay=true).
	rec = h.call(tok, "GET", "/v0/self/allocation?relay=true", "")
	want(t, rec, 200)
	if strings.Contains(rec.Body.String(), "hunter2") || strings.Contains(rec.Body.String(), "MATCH_KEY") || !strings.Contains(rec.Body.String(), "ranked") {
		t.Fatalf("relayed allocation: %s", rec.Body.String())
	}
	wantCode(t, h.call(tok, "GET", "/v0/self/allocation?relay=yes", ""), 422, "validation_failed")
	if b, _ := json.Marshal(in.alloc); strings.Contains(string(b), "hunter2") {
		t.Fatal("the Allocation type encodes secret values")
	}
}

// TestPlayerChosenTextWithControlCharacters: a player ID (often a name the player chose), a label value or a stop
// reason with a control character is refused with a 422 naming the field, here the ID's place in the list; one
// with a NUL in it once cost the platform a whole players report. The players already reported
// stand, and a list without the bad ID is taken.
func TestPlayerChosenTextWithControlCharacters(t *testing.T) {
	h := newHarness(t, Options{})
	_, tok := h.register("gs_names", false)
	want(t, h.call(tok, "PUT", "/v0/self/players", `{"ids":["ana","bo"]}`), 200)
	for _, bad := range []string{`bad\u0000name`, `bad\u0007`, `tab\there`, `del\u007f`, `c1\u0085`, `line\nbreak`} {
		rec := h.call(tok, "PUT", "/v0/self/players", `{"ids":["ana","bo","`+bad+`"]}`)
		wantCode(t, rec, 422, "validation_failed")
		if !strings.Contains(rec.Body.String(), `"field":"ids[2]"`) || !strings.Contains(rec.Body.String(), "control character") {
			t.Fatalf("%s: %s", bad, rec.Body.String())
		}
	}
	if s := decodeSelf(t, h.call(tok, "GET", "/v0/self", "")); s.Players.Count != 2 {
		t.Fatalf("a refused list changed the players: %+v", s.Players)
	}
	want(t, h.call(tok, "PUT", "/v0/self/players", `{"ids":["ana","bo","cy","Zoë 🎮"]}`), 200)
	if s := decodeSelf(t, h.call(tok, "GET", "/v0/self", "")); s.Players.Count != 4 {
		t.Fatalf("the next good list: %+v", s.Players)
	}
	rec := h.call(tok, "PUT", "/v0/self/players", `{"ids":["ana","ana"]}`)
	if wantCode(t, rec, 422, "validation_failed"); !strings.Contains(rec.Body.String(), `"field":"ids[1]"`) {
		t.Fatalf("duplicate: %s", rec.Body.String())
	}
	rec = h.call(tok, "PATCH", "/v0/self/labels", `{"host":"bad\u0000name"}`)
	if wantCode(t, rec, 422, "validation_failed"); !strings.Contains(rec.Body.String(), `"field":"host"`) {
		t.Fatalf("label: %s", rec.Body.String())
	}
	rec = h.call(tok, "POST", "/v0/self/stop", `{"reason":"over\u0000"}`)
	if wantCode(t, rec, 422, "validation_failed"); !strings.Contains(rec.Body.String(), `"field":"reason"`) {
		t.Fatalf("stop reason: %s", rec.Body.String())
	}
	for id, want := range map[string]string{"": "is empty", "ok": "", "a\x00b": "has a control character (U+0000)",
		strings.Repeat("é", 128): "", strings.Repeat("é", 129): "is longer than 128 characters", "\xff": "is not UTF-8 text"} {
		if got := PlayerIDProblem(id); got != want {
			t.Errorf("PlayerIDProblem(%q) = %q, want %q", id, got, want)
		}
	}
}

// The older names a game may still meet (kind world, status active, a persistent server's sleeping and waking, and
// GS_AGENT_URL for the local API) read the same as the new ones.
func TestOlderNamesReadTheSame(t *testing.T) {
	for kind, persistent := range map[string]bool{KindMatch: false, KindPersistentServer: true, KindWorld: true, "lobby": false} {
		if IsPersistentServer(kind) != persistent || ValidKind(kind) != (kind != "lobby") {
			t.Errorf("kind %q: persistent %v, valid %v", kind, IsPersistentServer(kind), ValidKind(kind))
		}
	}
	for in, want := range map[Status]Status{StatusActive: StatusRunning, StatusRunning: StatusRunning, StatusWarm: StatusWarm,
		"sleeping": "paused", "paused": "paused", "waking": "resuming", "resuming": "resuming", "": ""} {
		if got := in.Normal(); got != want {
			t.Errorf("Status(%q).Normal() = %q, want %q", in, got, want)
		}
	}
	if !StatusActive.IsRunning() || !StatusRunning.IsRunning() || StatusWarm.IsRunning() || StatusStopping.IsRunning() {
		t.Error("IsRunning: active and running only")
	}

	// GET /v0/self decodes and encodes under the current names.
	var self Self
	if err := json.Unmarshal([]byte(`{"id":"gs_1","kind":"persistent_server","region":"us-central","continent":"north-america","status":"running","players":{"count":2}}`), &self); err != nil {
		t.Fatal(err)
	}
	if self.ID != "gs_1" || self.Region != "us-central" || self.Continent != "north-america" || !IsPersistentServer(self.Kind) ||
		!self.Status.IsRunning() || self.Players.Count != 2 {
		t.Errorf("decoded as %+v", self)
	}
	if out, _ := json.Marshal(self); !strings.Contains(string(out), `"region":"us-central","continent":"north-america"`) {
		t.Errorf("encoded as %s", out)
	}
	var bad Self
	if err := json.Unmarshal([]byte(`{"id":`), &bad); err == nil {
		t.Error("broken JSON decoded")
	}

	// The variables: GS_REGION and GS_CONTINENT; the local API's address under its name or its older one.
	if r, c := RegionFromEnv(func(k string) string {
		return map[string]string{"GS_REGION": "us-central", "GS_CONTINENT": "north-america"}[k]
	}); r != "us-central" || c != "north-america" {
		t.Errorf("region %q, continent %q", r, c)
	}
	for _, env := range []map[string]string{
		{"GS_LOCAL_API_URL": "http://127.0.0.1:7600"},
		{"GS_AGENT_URL": "http://127.0.0.1:7600"},
		{"GS_LOCAL_API_URL": "http://127.0.0.1:7600", "GS_AGENT_URL": "http://ignored:1"},
	} {
		if u := LocalAPIURLFromEnv(func(k string) string { return env[k] }); u != "http://127.0.0.1:7600" {
			t.Errorf("%v: local API %q", env, u)
		}
	}

	// Register takes world as well, and keeps the kind as given (a build tried under survivalservers dev -kind world sees it).
	s := NewServer(Options{})
	in, err := s.Register(Registration{Token: "tok-world-0123456789abcdef", Self: Self{ID: "gs_w", Kind: KindWorld}})
	if err != nil {
		t.Fatal(err)
	}
	if k := in.Self().Kind; k != KindWorld {
		t.Fatalf("kind %q, want world", k)
	}
	if _, err := s.Register(Registration{Token: "tok-lobby-0123456789abcdef", Self: Self{ID: "gs_l", Kind: "lobby"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("kind lobby: %v", err)
	}
}

// A warm game server's own labels stay at allocation while there is room, by key order, the start's winning: the cell
// keeps the same ones (control placement's handoverLabels), so the API and the game see one set.
func TestAllocationKeepsWarmLabelsByKeyOrder(t *testing.T) {
	h := newHarness(t, Options{})
	in, tok := h.register("gs_warm_labels", true)
	var b strings.Builder
	b.WriteString("{")
	for i := 31; i >= 0; i-- {
		fmt.Fprintf(&b, `"k%02d":"own"`, i)
		if i > 0 {
			b.WriteString(",")
		}
	}
	b.WriteString("}")
	want(t, h.call(tok, "PATCH", "/v0/self/labels", b.String()), 200)
	if err := in.Allocate(Allocation{Labels: map[string]string{"queue": "ranked", "k05": "start"}}); err != nil {
		t.Fatal(err)
	}
	var self struct {
		Labels map[string]string `json:"labels"`
	}
	json.Unmarshal(h.call(tok, "GET", "/v0/self", "").Body.Bytes(), &self)
	if len(self.Labels) != 32 || self.Labels["queue"] != "ranked" || self.Labels["k05"] != "start" || self.Labels["k00"] != "own" ||
		self.Labels["k30"] != "own" {
		t.Fatalf("labels after allocation: %v", self.Labels)
	}
	if _, kept := self.Labels["k31"]; kept {
		t.Fatalf("the last key by order should have no room: %v", self.Labels)
	}
}

// With CheckCaller (the Windows agent), a token is taken only from its own game server's processes: a call the
// host marked as another game server's, or did not mark at all (a process in no game server's job), is refused like a
// wrong token, and counted.
func TestCheckCallerTakesATokenOnlyFromItsOwnGame(t *testing.T) {
	h := newHarness(t, Options{CheckCaller: true})
	_, tokA := h.register("gs_a", false)
	h.register("gs_b", false)
	as := func(id string) func(*http.Request) {
		return func(r *http.Request) { *r = *r.WithContext(WithCaller(r.Context(), id)) }
	}
	want(t, h.call(tokA, "GET", "/v0/self", "", as("gs_a")), 200)
	wantCode(t, h.call(tokA, "GET", "/v0/self", "", as("gs_b")), 401, "unauthenticated")
	wantCode(t, h.call(tokA, "GET", "/v0/self", ""), 401, "unauthenticated")
	wantCode(t, h.call(tokA, "POST", "/v0/self/stop", "{}", as("gs_b")), 401, "unauthenticated")
	if n := h.s.RefusedCallers(); n != 3 {
		t.Fatalf("refused %d, want 3", n)
	}
	// A wrong token is not a refused caller; without CheckCaller nothing changes.
	wantCode(t, h.call("tok-nobody-0123456789abcdef", "GET", "/v0/self", "", as("gs_a")), 401, "unauthenticated")
	if n := h.s.RefusedCallers(); n != 3 {
		t.Fatalf("refused %d after a wrong token, want 3", n)
	}
	h2 := newHarness(t, Options{})
	_, tok := h2.register("gs_c", false)
	want(t, h2.call(tok, "GET", "/v0/self", ""), 200)
}
