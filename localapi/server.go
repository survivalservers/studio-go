package localapi

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"
)

// Limits from openapi.yaml.
const (
	DefaultMaxWait   = 60 * time.Second
	DefaultRate      = 50  // calls a second per game server
	DefaultBurst     = 100 // calls at once per game server
	DefaultMaxPolls  = 4   // open long-polls of each kind per game server
	MaxBodyBytes     = 64 << 10
	maxLabels        = 32
	maxLabelValue    = 256
	maxPlayerIDs     = 1000
	maxPlayerIDLen   = 128
	maxPlayerCount   = 100000
	maxStopReasonLen = 128
	maxKeepLen       = 100

	// How often a game can say it saved. A folder that changed is backed up by the watcher anyway, so a plain call
	// refused here loses nothing. A kept save stays until someone deletes it, so that one is rarer; the cell also caps
	// how many a game can keep of one server.
	savedEvery = 10 * time.Second
	keptEvery  = time.Minute
)

var (
	labelKey = regexp.MustCompile(`^[a-z0-9_.-]{1,63}$`)
	waitRe   = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?(ms|s|m)?$`)

	ErrDuplicate        = errors.New("localapi: a game server with this ID or token is already registered")
	ErrNoToken          = errors.New("localapi: registration needs a token of at least 16 bytes")
	ErrInvalid          = errors.New("localapi: registration needs an ID and a kind of match or persistent_server (or world, its older name)")
	ErrAlreadyAllocated = errors.New("localapi: game server is already allocated")
)

// Options configure a Server. Zero values take the defaults above.
type Options struct {
	// OnEvent receives every call a game server makes. It runs on the request's goroutine, after the call's
	// effect is recorded and before the reply is written, so it must return quickly (hand off to a channel).
	OnEvent  func(Event)
	Now      func() time.Time
	MaxWait  time.Duration
	Rate     float64
	Burst    int
	MaxPolls int
	// CheckCaller: a call is taken only when the host has marked its request with the game server whose process made
	// it (WithCaller), and that is the game server its token names. The Windows agent sets it: there every game server
	// calls one shared address, and a token alone must not be enough from another game server's process.
	CheckCaller bool
}

// Registration describes a game server the agent has started (or is about to start).
type Registration struct {
	Token string // the GS_TOKEN value; random, at least 16 bytes
	Self  Self   // its record; Status and Players are ignored and start empty
	// Warm marks a pre-started game server, a ready server: ready means "waiting for allocation".
	Warm bool
	// From, when set, is the only address its calls may come from (a Linux container's own address).
	From netip.Prefix
}

// Server serves the local API for every game server on one machine.
type Server struct {
	opt     Options
	refused atomic.Uint64 // CheckCaller: calls with a good token from another process (RefusedCallers)
	mu      sync.RWMutex
	byToken map[[32]byte]*Instance
	byID    map[string]*Instance
	routes  map[string]map[string]handlerFunc
}

type handlerFunc func(*Instance, http.ResponseWriter, *http.Request)

// NewServer returns a Server with no game servers registered.
func NewServer(opt Options) *Server {
	if opt.Now == nil {
		opt.Now = time.Now
	}
	if opt.MaxWait <= 0 {
		opt.MaxWait = DefaultMaxWait
	}
	if opt.Rate <= 0 {
		opt.Rate = DefaultRate
	}
	if opt.Burst <= 0 {
		opt.Burst = DefaultBurst
	}
	if opt.MaxPolls <= 0 {
		opt.MaxPolls = DefaultMaxPolls
	}
	s := &Server{opt: opt, byToken: map[[32]byte]*Instance{}, byID: map[string]*Instance{}}
	s.routes = map[string]map[string]handlerFunc{
		"/v0/self":            {http.MethodGet: s.getSelf},
		"/v0/self/ready":      {http.MethodPost: s.postReady},
		"/v0/self/health":     {http.MethodPost: s.postHealth},
		"/v0/self/allocation": {http.MethodGet: s.getAllocation},
		"/v0/self/shutdown":   {http.MethodGet: s.getShutdown},
		"/v0/self/players":    {http.MethodPut: s.putPlayers},
		"/v0/self/labels":     {http.MethodPatch: s.patchLabels},
		"/v0/self/stop":       {http.MethodPost: s.postStop},
		"/v0/self/tick":       {http.MethodPost: s.postTick},
		"/v0/self/saved":      {http.MethodPost: s.postSaved},
	}
	return s
}

// NewHTTPServer wraps h in an http.Server with timeouts that suit long-polls and keep a misbehaving game server
// from holding connections open forever.
func (s *Server) NewHTTPServer(addr string) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           s,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      s.opt.MaxWait + 15*time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    16 << 10,
	}
}

// Register adds a game server. Its calls are accepted from now on.
func (s *Server) Register(r Registration) (*Instance, error) {
	if len(r.Token) < 16 {
		return nil, ErrNoToken
	}
	if r.Self.ID == "" || !ValidKind(r.Self.Kind) { // the kind is kept as given, so world shows as world
		return nil, ErrInvalid
	}
	self := r.Self
	self.Status = StatusStarting
	self.Players = Players{}
	self.Labels = copyLabels(self.Labels)
	self.AllocatedAt, self.ShutdownRequestedAt = nil, nil
	if self.Ports == nil {
		self.Ports = map[string]Port{}
	}
	in := &Instance{
		srv: s, self: self, warm: r.Warm, from: r.From, tokenHash: sha256.Sum256([]byte(r.Token)),
		allocated: make(chan struct{}), shutdown: make(chan struct{}), gone: make(chan struct{}),
		tokens: float64(s.opt.Burst), last: s.opt.Now(),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, dup := s.byToken[in.tokenHash]; dup {
		return nil, ErrDuplicate
	}
	if _, dup := s.byID[self.ID]; dup {
		return nil, ErrDuplicate
	}
	s.byToken[in.tokenHash] = in
	s.byID[self.ID] = in
	return in, nil
}

// Get returns a registered game server, or nil.
func (s *Server) Get(id string) *Instance {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.byID[id]
}

// Unregister removes a game server once it has ended. Open long-polls answer 204; later calls get 401.
func (s *Server) Unregister(id string) {
	s.mu.Lock()
	in := s.byID[id]
	if in != nil {
		delete(s.byID, id)
		delete(s.byToken, in.tokenHash)
	}
	s.mu.Unlock()
	if in != nil {
		in.goneOnce.Do(func() { close(in.gone) })
	}
}

// Instance is one registered game server. The agent drives it; its methods are safe for concurrent use.
type Instance struct {
	srv       *Server
	tokenHash [32]byte
	from      netip.Prefix
	warm      bool

	mu        sync.Mutex
	self      Self
	ready     bool
	alloc     *Allocation
	sd        *Shutdown
	watched   bool // the game server has asked for the shutdown notice at least once
	stopAsked bool
	saved     time.Time // the last saved call let through
	kept      time.Time // the last one that asked to keep
	tokens    float64
	last      time.Time
	calls     int // calls made with its token, answered or not (Calls)
	polls     map[string]int

	allocated chan struct{}
	shutdown  chan struct{}
	gone      chan struct{}
	goneOnce  sync.Once
}

// Self returns a copy of the game server's record.
func (in *Instance) Self() Self {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.selfLocked()
}

func (in *Instance) selfLocked() Self {
	s := in.self
	s.Labels = copyLabels(s.Labels)
	s.Ports = make(map[string]Port, len(in.self.Ports))
	for k, v := range in.self.Ports {
		s.Ports[k] = v
	}
	s.Players.IDs = append([]string(nil), s.Players.IDs...)
	return s
}

// MarkReady records that the version's ready check passed (a ready call, a log line, a bound port or a delay; the
// agent decides). A ready server becomes warm until allocated; any other becomes running.
func (in *Instance) MarkReady() {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.ready = true
	if in.self.Status == StatusStarting {
		in.self.Status = in.statusAfterReady()
	}
}

func (in *Instance) statusAfterReady() Status {
	if in.warm && in.alloc == nil {
		return StatusWarm
	}
	return StatusRunning
}

// Allocate hands the game server its match: the long-poll answers and GET /v0/self shows the allocation. The agent
// writes the allocation file itself (WriteAllocationFile) before calling this.
func (in *Instance) Allocate(a Allocation) error {
	if a.AllocatedAt.IsZero() {
		a.AllocatedAt = in.srv.opt.Now()
	}
	a = normalAllocation(a)
	in.mu.Lock()
	if in.alloc != nil {
		in.mu.Unlock()
		return ErrAlreadyAllocated
	}
	a.GameServerID = in.self.ID
	in.alloc = &a
	t := a.AllocatedAt
	in.self.AllocatedAt = &t
	// The start request's labels win; labels the game server set while warm stay while there is room, by key order
	// (the cell keeps the same ones: placement's handoverLabels).
	labels := copyLabels(a.Labels)
	own := make([]string, 0, len(in.self.Labels))
	for k := range in.self.Labels {
		own = append(own, k)
	}
	sort.Strings(own)
	for _, k := range own {
		if _, set := labels[k]; !set && len(labels) < maxLabels {
			labels[k] = in.self.Labels[k]
		}
	}
	in.self.Labels = labels
	if in.ready && in.self.Status == StatusWarm {
		in.self.Status = StatusRunning
	}
	in.mu.Unlock()
	close(in.allocated)
	return nil
}

// RequestShutdown tells the game server to stop by the deadline: the shutdown long-poll answers and its status
// becomes stopping. Calling it again only moves the deadline earlier.
func (in *Instance) RequestShutdown(sd Shutdown) {
	if sd.RequestedAt.IsZero() {
		sd.RequestedAt = in.srv.opt.Now()
	}
	sd.RequestedAt, sd.Deadline = utcMillis(sd.RequestedAt), utcMillis(sd.Deadline)
	in.mu.Lock()
	first := in.sd == nil
	if first {
		in.sd = &sd
		t := sd.RequestedAt
		in.self.ShutdownRequestedAt = &t
	} else if sd.Deadline.Before(in.sd.Deadline) {
		in.sd.Deadline = sd.Deadline
	}
	in.self.Status = StatusStopping
	in.mu.Unlock()
	if first {
		close(in.shutdown)
	}
}

// WatchesShutdown says whether the game server has asked for the shutdown notice (GET /v0/self/shutdown) during this
// run, so it hears a stop from the notice itself. The agent then holds back a stop signal that could cut its own
// exit short. A poll with relay=true (a compatibility layer passing the notice on, such as compat's Agones watch)
// does not count: a game built for that platform expects the signal with the notice.
func (in *Instance) WatchesShutdown() bool {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.watched
}

// Calls is how many calls the game server has made with its token: answered, refused for its rate or for an unknown
// path or method alike. The agent tells a start that never calls ready apart by it: none at all points at the address
// or the token the game reads (GS_LOCAL_API_URL, GS_TOKEN), some at the game's own start.
func (in *Instance) Calls() int {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.calls
}

// ServeHTTP routes a call to the game server its token belongs to.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	methods, ok := s.routes[r.URL.Path]
	if !ok {
		s.countCall(r)
		problem(w, http.StatusNotFound, "not_found", "No such call.", nil)
		return
	}
	h, ok := methods[r.Method]
	if !ok {
		s.countCall(r)
		allow := make([]string, 0, len(methods))
		for m := range methods {
			allow = append(allow, m)
		}
		sort.Strings(allow)
		w.Header().Set("Allow", strings.Join(allow, ", "))
		problem(w, http.StatusMethodNotAllowed, "bad_request", "Use "+strings.Join(allow, " or ")+" for this call.", nil)
		return
	}
	in := s.authenticate(r)
	if in == nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="game server"`)
		problem(w, http.StatusUnauthorized, "unauthenticated", "Send Authorization: Bearer $GS_TOKEN.", nil)
		return
	}
	if wait, ok := in.allow(); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(wait))
		problem(w, http.StatusTooManyRequests, "rate_limited", "Too many calls from this game server.", nil)
		return
	}
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
	}
	h(in, w, r)
}

func (s *Server) authenticate(r *http.Request) *Instance {
	auth := r.Header.Get("Authorization")
	const prefix = "bearer "
	if len(auth) <= len(prefix) || !strings.EqualFold(auth[:len(prefix)], prefix) {
		return nil
	}
	h := sha256.Sum256([]byte(strings.TrimSpace(auth[len(prefix):])))
	s.mu.RLock()
	in := s.byToken[h]
	s.mu.RUnlock()
	if in == nil {
		return nil
	}
	if in.from.IsValid() {
		ap, err := netip.ParseAddrPort(r.RemoteAddr)
		if err != nil {
			host, _, _ := net.SplitHostPort(r.RemoteAddr)
			a, perr := netip.ParseAddr(host)
			if perr != nil {
				return nil
			}
			ap = netip.AddrPortFrom(a, 0)
		}
		if !in.from.Contains(ap.Addr().Unmap()) {
			return nil
		}
	}
	if s.opt.CheckCaller {
		if id, ok := CallerOf(r.Context()); !ok || id != in.self.ID {
			s.refused.Add(1)
			return nil
		}
	}
	return in
}

// RefusedCallers counts calls refused because their token belonged to a game server whose processes did not make them
// (CheckCaller). The token itself is never kept or logged.
func (s *Server) RefusedCallers() uint64 { return s.refused.Load() }

// countCall counts a call to an unknown path or method for the game server whose token it carries (Calls): the game
// found the local API, whatever it asked.
func (s *Server) countCall(r *http.Request) {
	if in := s.authenticate(r); in != nil {
		in.mu.Lock()
		in.calls++
		in.mu.Unlock()
	}
}

// allow takes one call from the game server's token bucket; when empty it says how many seconds to wait. Every call it
// is asked about counts (Calls).
func (in *Instance) allow() (int, bool) {
	o := in.srv.opt
	now := o.Now()
	in.mu.Lock()
	defer in.mu.Unlock()
	in.calls++
	in.tokens = math.Min(float64(o.Burst), in.tokens+now.Sub(in.last).Seconds()*o.Rate)
	in.last = now
	if in.tokens >= 1 {
		in.tokens--
		return 0, true
	}
	return int(math.Ceil((1 - in.tokens) / o.Rate)), false
}

func (s *Server) emit(e Event) {
	if s.opt.OnEvent != nil {
		s.opt.OnEvent(e)
	}
}

func (s *Server) getSelf(in *Instance, w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, in.Self())
}

func (s *Server) postReady(in *Instance, w http.ResponseWriter, r *http.Request) {
	in.mu.Lock()
	stopping := in.sd != nil || in.stopAsked
	in.mu.Unlock()
	if stopping {
		problem(w, http.StatusConflict, "invalid_state", "A stop has been requested; this game server cannot become ready.", nil)
		return
	}
	s.emit(Event{GameServerID: in.self.ID, Type: EventReady, At: s.opt.Now()})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) postHealth(in *Instance, w http.ResponseWriter, r *http.Request) {
	s.emit(Event{GameServerID: in.self.ID, Type: EventHealth, At: s.opt.Now()})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getAllocation(in *Instance, w http.ResponseWriter, r *http.Request) {
	relay := r.URL.Query().Get("relay")
	if relay != "" && relay != "true" && relay != "false" {
		problem(w, http.StatusUnprocessableEntity, "validation_failed", "relay is true or false.", []fieldError{{"relay", "relay is true or false"}})
		return
	}
	s.longPoll(in, w, r, "allocation", in.allocated, func() (any, bool) {
		in.mu.Lock()
		defer in.mu.Unlock()
		if in.alloc == nil {
			return nil, false
		}
		// Secret values are handed over here only (never in the allocation file), and never to a compatibility layer
		// that passes the match on as another platform's settings (relay=true: compat's GameLift game properties).
		a := *in.alloc
		a.Env = copyLabels(a.Env)
		if relay == "true" {
			return a, true
		}
		for k, v := range a.SecretEnv {
			a.Env[k] = v
		}
		return a, true
	})
}

func (s *Server) getShutdown(in *Instance, w http.ResponseWriter, r *http.Request) {
	relay := r.URL.Query().Get("relay")
	if relay != "" && relay != "true" && relay != "false" {
		problem(w, http.StatusUnprocessableEntity, "validation_failed", "relay is true or false.", []fieldError{{"relay", "relay is true or false"}})
		return
	}
	// A compatibility layer that passes the notice on in another platform's terms (relay=true: compat's Agones watch)
	// is not the game waiting for it: that game expects its stop signal with the notice, as on its own platform.
	if relay != "true" {
		in.mu.Lock()
		in.watched = true
		in.mu.Unlock()
	}
	s.longPoll(in, w, r, "shutdown", in.shutdown, func() (any, bool) {
		in.mu.Lock()
		defer in.mu.Unlock()
		if in.sd == nil {
			return nil, false
		}
		return *in.sd, true
	})
}

// longPoll answers 200 with get() once it has a value, or 204 when wait runs out, the client goes away, the game
// server is unregistered, or (for the allocation poll) a shutdown is requested.
func (s *Server) longPoll(in *Instance, w http.ResponseWriter, r *http.Request, kind string, ready <-chan struct{}, get func() (any, bool)) {
	wait, err := parseWait(r.URL.Query().Get("wait"), s.opt.MaxWait)
	if err != nil {
		problem(w, http.StatusUnprocessableEntity, "validation_failed", err.Error(), []fieldError{{"wait", err.Error()}})
		return
	}
	if v, ok := get(); ok {
		writeJSON(w, http.StatusOK, v)
		return
	}
	if wait > 0 {
		in.mu.Lock()
		if in.polls == nil {
			in.polls = map[string]int{}
		}
		if in.polls[kind] >= s.opt.MaxPolls {
			in.mu.Unlock()
			w.Header().Set("Retry-After", "1")
			problem(w, http.StatusTooManyRequests, "rate_limited", fmt.Sprintf("At most %d open %s long-polls per game server.", s.opt.MaxPolls, kind), nil)
			return
		}
		in.polls[kind]++
		in.mu.Unlock()
		defer func() {
			in.mu.Lock()
			in.polls[kind]--
			in.mu.Unlock()
		}()
		var early <-chan struct{}
		if kind == "allocation" {
			early = in.shutdown
		}
		t := time.NewTimer(wait)
		defer t.Stop()
		select {
		case <-ready:
		case <-early:
		case <-t.C:
		case <-in.gone:
		case <-r.Context().Done():
			return
		}
		if v, ok := get(); ok {
			writeJSON(w, http.StatusOK, v)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// openPolls reports how many long-polls of a kind are waiting (for tests and the agent's metrics).
func (in *Instance) openPolls(kind string) int {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.polls[kind]
}

func (s *Server) putPlayers(in *Instance, w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs     *[]string        `json:"ids"`
		Count   *json.RawMessage `json:"count"`
		Players *[]PlayerInfo    `json:"players"`
	}
	if !decode(w, r, &body, false) {
		return
	}
	var p Players
	given := 0
	for _, is := range []bool{body.IDs != nil, body.Count != nil, body.Players != nil} {
		if is {
			given++
		}
	}
	if body.Players != nil && given == 1 {
		// Who they are and where: its IDs are checked as any others, and the count follows.
		ids := make([]string, 0, len(*body.Players))
		for _, pl := range *body.Players {
			ids = append(ids, pl.ID)
		}
		body.IDs = &ids
		p.Info = *body.Players
	}
	switch {
	case given != 1:
		problem(w, http.StatusUnprocessableEntity, "validation_failed", "Send exactly one of players, ids or count.", []fieldError{{"ids", "send players, ids or count, one of them"}})
		return
	case body.IDs != nil:
		ids := *body.IDs
		if len(ids) > maxPlayerIDs {
			problem(w, http.StatusUnprocessableEntity, "validation_failed", "Too many player IDs.", []fieldError{{"ids", fmt.Sprintf("at most %d", maxPlayerIDs)}})
			return
		}
		seen := make(map[string]int, len(ids))
		for i, id := range ids {
			field := fmt.Sprintf("ids[%d]", i)
			if p := PlayerIDProblem(id); p != "" {
				problem(w, http.StatusUnprocessableEntity, "validation_failed", "A player ID is not allowed.",
					[]fieldError{{field, fmt.Sprintf("it %s (a player ID is 1 to %d characters with no control characters)", p, maxPlayerIDLen)}})
				return
			}
			if j, ok := seen[id]; ok {
				problem(w, http.StatusUnprocessableEntity, "validation_failed", "A player ID appears twice.", []fieldError{{field, fmt.Sprintf("the same as ids[%d]", j)}})
				return
			}
			seen[id] = i
		}
		p.Count, p.IDs = len(ids), append(make([]string, 0, len(ids)), ids...)
	default:
		n, err := strconv.Atoi(strings.TrimSpace(string(*body.Count)))
		if err != nil || n < 0 || n > maxPlayerCount {
			problem(w, http.StatusUnprocessableEntity, "validation_failed", "count must be a whole number.", []fieldError{{"count", fmt.Sprintf("a whole number from 0 to %d", maxPlayerCount)}})
			return
		}
		p = Players{Count: n}
	}
	in.mu.Lock()
	in.self.Players = p
	in.mu.Unlock()
	out := Players{Count: p.Count, IDs: append([]string(nil), p.IDs...), Info: append([]PlayerInfo(nil), p.Info...)}
	s.emit(Event{GameServerID: in.self.ID, Type: EventPlayers, At: s.opt.Now(), Players: &out})
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) patchLabels(in *Instance, w http.ResponseWriter, r *http.Request) {
	var patch map[string]*string
	if !decode(w, r, &patch, false) {
		return
	}
	if patch == nil {
		problem(w, http.StatusUnprocessableEntity, "validation_failed", "Send a JSON object of labels.", []fieldError{{"labels", "a JSON object"}})
		return
	}
	const maxPatch = 64 // keys in one call, as the spec's LabelsPatch says; removals count too
	if len(patch) > maxPatch {
		problem(w, http.StatusUnprocessableEntity, "validation_failed", "Too many labels in one call.", []fieldError{{"labels", fmt.Sprintf("at most %d keys in one call", maxPatch)}})
		return
	}
	for k, v := range patch {
		if !labelKey.MatchString(k) {
			problem(w, http.StatusUnprocessableEntity, "validation_failed", "A label name is not allowed.", []fieldError{{k, "1 to 63 characters of a-z 0-9 _ . -"}})
			return
		}
		if v == nil {
			continue
		}
		if p := textProblem(*v, maxLabelValue); p != "" {
			problem(w, http.StatusUnprocessableEntity, "validation_failed", "A label value is not allowed.",
				[]fieldError{{k, fmt.Sprintf("it %s (at most %d characters, no control characters)", p, maxLabelValue)}})
			return
		}
	}
	in.mu.Lock()
	next := copyLabels(in.self.Labels)
	for k, v := range patch {
		if v == nil {
			delete(next, k)
		} else {
			next[k] = *v
		}
	}
	if len(next) > maxLabels {
		in.mu.Unlock()
		problem(w, http.StatusUnprocessableEntity, "validation_failed", "Too many labels.", []fieldError{{"labels", fmt.Sprintf("at most %d", maxLabels)}})
		return
	}
	in.self.Labels = next
	in.mu.Unlock()
	s.emit(Event{GameServerID: in.self.ID, Type: EventLabels, At: s.opt.Now(), Labels: copyLabels(next)})
	writeJSON(w, http.StatusOK, map[string]any{"labels": next})
}

func (s *Server) postStop(in *Instance, w http.ResponseWriter, r *http.Request) {
	var body struct {
		Reason string `json:"reason"`
	}
	if !decode(w, r, &body, true) {
		return
	}
	if p := textProblem(body.Reason, maxStopReasonLen); p != "" {
		problem(w, http.StatusUnprocessableEntity, "validation_failed", "reason is not allowed.",
			[]fieldError{{"reason", fmt.Sprintf("it %s (at most %d characters, no control characters)", p, maxStopReasonLen)}})
		return
	}
	in.mu.Lock()
	first := !in.stopAsked
	in.stopAsked = true
	in.mu.Unlock()
	if first {
		s.emit(Event{GameServerID: in.self.ID, Type: EventStop, At: s.opt.Now(), Reason: body.Reason})
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": string(StatusStopping)})
}

func (s *Server) postTick(in *Instance, w http.ResponseWriter, r *http.Request) {
	var body struct {
		P50ms *float64         `json:"p50_ms"`
		P99ms *float64         `json:"p99_ms"`
		Hz    *json.RawMessage `json:"hz"`
	}
	if !decode(w, r, &body, false) {
		return
	}
	bad := func(f, m string) {
		problem(w, http.StatusUnprocessableEntity, "validation_failed", f+" "+m+".", []fieldError{{f, m}})
	}
	inRange := func(v *float64) bool { return v == nil || (*v >= 0 && *v <= 100000) }
	switch {
	case body.P99ms == nil:
		bad("p99_ms", "is required")
		return
	case !inRange(body.P99ms):
		bad("p99_ms", "must be from 0 to 100000")
		return
	case !inRange(body.P50ms):
		bad("p50_ms", "must be from 0 to 100000")
		return
	}
	t := Tick{P50ms: body.P50ms, P99ms: *body.P99ms}
	if body.Hz != nil {
		hz, err := strconv.Atoi(strings.TrimSpace(string(*body.Hz)))
		if err != nil || hz < 1 || hz > 1000 {
			bad("hz", "must be a whole number from 1 to 1000")
			return
		}
		t.Hz = hz
	}
	s.emit(Event{GameServerID: in.self.ID, Type: EventTick, At: s.opt.Now(), Tick: &t})
	w.WriteHeader(http.StatusNoContent)
}

// postSaved is the game saying it just finished a save: it is backed up at once, and with keep, kept under that name.
func (s *Server) postSaved(in *Instance, w http.ResponseWriter, r *http.Request) {
	var body struct {
		Keep *string `json:"keep"`
	}
	if !decode(w, r, &body, true) {
		return
	}
	keep := ""
	if body.Keep != nil {
		keep = *body.Keep
		if p := textProblem(keep, maxKeepLen); p != "" || keep == "" {
			if keep == "" {
				p = "is empty"
			}
			problem(w, http.StatusUnprocessableEntity, "validation_failed", "keep "+p+".",
				[]fieldError{{"keep", fmt.Sprintf("it %s (1 to %d characters, no control characters)", p, maxKeepLen)}})
			return
		}
	}
	if !IsPersistentServer(in.self.Kind) {
		problem(w, http.StatusConflict, "invalid_state", "Only a persistent server has backups.", nil)
		return
	}
	now := s.opt.Now()
	in.mu.Lock()
	wait := savedEvery - now.Sub(in.saved)
	if keep != "" {
		wait = max(wait, keptEvery-now.Sub(in.kept))
	}
	if wait > 0 {
		in.mu.Unlock()
		secs := int(wait.Round(time.Second) / time.Second)
		w.Header().Set("Retry-After", strconv.Itoa(max(secs, 1)))
		detail := fmt.Sprintf("A save can be backed up once every %d seconds. Its folder is still watched, so nothing is lost.", int(savedEvery/time.Second))
		if keep != "" {
			detail = "A save can be kept once a minute. This one was not kept; ask again when the wait is over."
		}
		problem(w, http.StatusTooManyRequests, "rate_limited", detail, nil)
		return
	}
	in.saved = now
	if keep != "" {
		in.kept = now
	}
	in.mu.Unlock()
	s.emit(Event{GameServerID: in.self.ID, Type: EventSaved, At: now, Keep: keep})
	w.WriteHeader(http.StatusAccepted)
}

// decode reads a JSON body. Unknown fields are ignored so a game built against a newer version of this local API
// still works with an older agent.
func decode(w http.ResponseWriter, r *http.Request, v any, optional bool) bool {
	b, err := io.ReadAll(r.Body)
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			problem(w, http.StatusRequestEntityTooLarge, "too_large", fmt.Sprintf("Request bodies are at most %d bytes.", MaxBodyBytes), nil)
		} else {
			problem(w, http.StatusBadRequest, "bad_request", "Could not read the request body.", nil)
		}
		return false
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		if optional {
			return true
		}
		problem(w, http.StatusUnprocessableEntity, "validation_failed", "This call needs a JSON body.", []fieldError{{"body", "required"}})
		return false
	}
	if err := json.Unmarshal(b, v); err != nil {
		problem(w, http.StatusUnprocessableEntity, "validation_failed", "The body is not the JSON this call expects: "+err.Error(), []fieldError{{"body", "invalid JSON"}})
		return false
	}
	return true
}

func parseWait(v string, max time.Duration) (time.Duration, error) {
	if v == "" {
		return 0, nil
	}
	if !waitRe.MatchString(v) {
		return 0, fmt.Errorf("wait must be a number with a unit, such as 30s")
	}
	if last := v[len(v)-1]; last >= '0' && last <= '9' {
		v += "s"
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("wait must be a number with a unit, such as 30s")
	}
	return min(d, max), nil
}

// PlayerIDProblem says what is wrong with a player ID a game reports, or "" when nothing is: 1 to 128 characters of
// UTF-8 text with no control characters. Player IDs are often names the players chose themselves, and one with a NUL
// in it once cost a whole players report (the platform's records cannot keep it), so the
// local API refuses such an ID with a 422 naming it, and the SDKs refuse it at connect, before it joins their list.
func PlayerIDProblem(id string) string {
	if id == "" {
		return "is empty"
	}
	return textProblem(id, maxPlayerIDLen)
}

// textProblem is the rule for text a game passes on (player IDs, label values, a stop reason): UTF-8, at most max
// characters, no control characters (U+0000-U+001F, U+007F-U+009F).
func textProblem(s string, max int) string {
	if !utf8.ValidString(s) {
		return "is not UTF-8 text"
	}
	n := 0
	for _, r := range s {
		if r < 0x20 || r >= 0x7f && r <= 0x9f {
			return fmt.Sprintf("has a control character (U+%04X)", r)
		}
		n++
	}
	if n > max {
		return fmt.Sprintf("is longer than %d characters", max)
	}
	return ""
}

type fieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ErrorDocsBase is where each error code is explained: the public API's errors page, one anchor per code (every
// local API code is one of the public API's).
const ErrorDocsBase = "https://studio.survivalservers.com/docs/reference/errors/#"

func problem(w http.ResponseWriter, status int, code, detail string, errs []fieldError) {
	body := map[string]any{
		"type":   ErrorDocsBase + code,
		"title":  http.StatusText(status),
		"status": status,
		"code":   code,
		"detail": detail,
	}
	if len(errs) > 0 {
		body["errors"] = errs
	}
	b, _ := json.Marshal(body)
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	w.Write(append(b, '\n'))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		problem(w, http.StatusInternalServerError, "internal", "Could not encode the reply.", nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	w.Write(append(b, '\n'))
}

func copyLabels(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func utcMillis(t time.Time) time.Time { return t.UTC().Truncate(time.Millisecond) }

// normalAllocation fills nil maps and slices so the JSON always has every field the spec requires.
func normalAllocation(a Allocation) Allocation {
	a.AllocatedAt = utcMillis(a.AllocatedAt)
	if a.ExpiresAt != nil {
		t := utcMillis(*a.ExpiresAt)
		a.ExpiresAt = &t
	}
	a.Labels = copyLabels(a.Labels)
	env := make(map[string]string, len(a.Env))
	for k, v := range a.Env {
		env[k] = v
	}
	a.Env = env
	if a.SecretEnv != nil {
		a.SecretEnv = copyLabels(a.SecretEnv)
	}
	a.Args = append(make([]string, 0, len(a.Args)), a.Args...)
	return a
}
