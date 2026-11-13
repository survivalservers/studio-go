// Package client is a small Go client for the public game server API: JSON in and out, an Idempotency-Key on every
// POST and PATCH, a call tried again on 429 rate_limited and 503 unavailable (waiting Retry-After), problem details
// turned into errors that say what to do next, and plain-text and redirect replies for logs and downloads.
package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
	"time"
)

// DefaultURL is the public API's address, used when none is set (decisions "Addresses in code"). Inside a test binary
// (testing.Testing) New refuses an empty address, so a test always names the address it calls.
const DefaultURL = "https://studio.survivalservers.com"

// ErrNoAddressInTests is New's answer to an empty address inside a test binary.
var ErrNoAddressInTests = errors.New("no API address given: tests must set one")

// Client calls the API with one key.
type Client struct {
	base      *url.URL
	key       string
	http      *http.Client
	streams   *http.Client // no overall timeout: followed logs and event streams stay open
	userAgent string
	tries     int           // how many times a call is sent in all (SetRetry)
	maxWait   time.Duration // the longest Retry-After waited for (SetRetry)
	sleep     func(context.Context, time.Duration) error
}

// DefaultTries and DefaultMaxWait are how a new client tries again: a call is sent at most 3 times, and it waits for a
// Retry-After of up to 30 seconds. See SetRetry.
const (
	DefaultTries   = 3
	DefaultMaxWait = 30 * time.Second
)

// AnswerTimeout is how long a call may take with the default HTTP client: above the API's longest wait of 60
// seconds. Followed logs and event streams have none.
const AnswerTimeout = 70 * time.Second

// New returns a client for the API at baseURL (with or without /v0) using key. A nil httpClient means a client
// with AnswerTimeout (none for streams) that never follows redirects (downloads answer with a link, which is
// returned instead). An empty userAgent sends DefaultUserAgent.
func New(baseURL, key string, httpClient *http.Client, userAgent string) (*Client, error) {
	if baseURL == "" {
		if testing.Testing() {
			return nil, ErrNoAddressInTests
		}
		baseURL = DefaultURL
	}
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("the API address %q is not an http(s) address", baseURL)
	}
	u.Path = strings.TrimSuffix(u.Path, "/v0")
	noFollow := func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	streams := httpClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: AnswerTimeout, CheckRedirect: noFollow}
		streams = &http.Client{CheckRedirect: noFollow}
	}
	if userAgent == "" {
		userAgent = DefaultUserAgent()
	}
	return &Client{base: u, key: key, http: httpClient, streams: streams, userAgent: userAgent,
		tries: DefaultTries, maxWait: DefaultMaxWait, sleep: sleepCtx}, nil
}

// SetRetry sets how many times a call is sent in all (1: never again) and the longest Retry-After it waits for. A
// call is tried again only when the API did not do it: on 429 rate_limited (any call), and on 503 unavailable (an
// API location briefly down) for reads and for writes that carry an Idempotency-Key, which every POST and PATCH of
// this client does (the same key on every try). Never on 503 capacity_unavailable: more regions, or later, is the
// caller's call. An answer asking for a longer wait than maxWait is returned at once, Retry-After and all.
func (c *Client) SetRetry(tries int, maxWait time.Duration) {
	c.tries, c.maxWait = max(tries, 1), max(maxWait, 0)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// again says whether a failed call may be sent once more: the API refused it before doing anything (429), or a
// location was briefly down (503 unavailable) and the call is a read or carries an Idempotency-Key.
func again(method string, keyed bool, p *Problem) bool {
	switch {
	case p.Status == http.StatusTooManyRequests:
		return true
	case p.Status == http.StatusServiceUnavailable && p.Code == "unavailable":
		return keyed || (method != http.MethodPost && method != http.MethodPatch)
	}
	return false
}

// retryWait is how long to wait before try number try+1: Retry-After's seconds when the API sent it, otherwise 1 and
// then 2 seconds.
func retryWait(retryAfter string, try int) time.Duration {
	if n, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil {
		return time.Duration(max(n, 0)) * time.Second
	}
	return time.Duration(try) * time.Second
}

// HasKey reports whether the client has a key to send.
func (c *Client) HasKey() bool { return c.key != "" }

// Base is the API address without /v0.
func (c *Client) Base() string { return c.base.String() }

// Problem is an error reply from the API (RFC 9457 problem details).
type Problem struct {
	Status       int               `json:"status"`
	Code         string            `json:"code"`
	Title        string            `json:"title"`
	Detail       string            `json:"detail"`
	RequestID    string            `json:"request_id"`
	Errors       []FieldError      `json:"errors"`
	RegionsTried []json.RawMessage `json:"regions_tried"`
	RetryAfter   string            `json:"-"`
	Method       string            `json:"-"`
	Path         string            `json:"-"`
}

// FieldError is one field's problem in a validation_failed reply.
type FieldError struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// NextStep is what a caller should do about each error code, so a program, a person or an AI tool can correct
// itself without asking. It is written for any caller: the AI tool adds its own lines (oss/mcp/server/schema.go).
// A test ties it to the error codes the API file lists.
var NextStep = map[string]string{
	"unauthenticated":        "The API key is missing, unknown, expired or revoked. Use a current key from the panel (API keys).",
	"missing_scope":          "The API key lacks a scope this call needs. Use a key with that scope (panel: API keys), or skip this step.",
	"key_restricted":         "The API key is limited to other apps, addresses or studios. Use an app the key allows, or a key that covers this app.",
	"card_required":          "Add a card in the panel, or pay an overdue invoice there, then try again.",
	"payment_disputed":       "A card payment for this studio is disputed with its bank, or a lost dispute isn't settled yet. New starts are refused until it is; game servers already running keep running.",
	"limit_reached":          "A studio limit is reached (running vCPUs or versions per app). Stop or delete something first, or ask support to raise the limit.",
	"studio_suspended":       "The studio is suspended: reads, backup downloads and the billing page still work, changes do not. Contact support.",
	"panel_session_required": "This works only from the panel, not with an API key.",
	"not_found":              "No such object for this studio. List the objects first and use an ID or name from the list.",
	"already_stopped":        "The game server has already ended. Read its logs and files instead.",
	"invalid_state":          "The object's state does not allow this now (for example a build that is not ready yet). Read its state, wait if it is still changing, then try again.",
	"idempotency_key_reused": "This request repeated an earlier one with different values. Send it again as a new call.",
	"idempotency_key_in_use": "The same request is still running. Wait a few seconds and read the object's state.",
	"version_not_ready":      "No region this start may use has the version yet. Wait for the version to reach a region, or allow more regions.",
	"name_taken":             "That name is already used. Pick another, or use the existing object.",
	"in_use":                 "Something still uses it (a running game server, a branch or a version). Stop or move that first.",
	"too_large":              "Over a size limit: a request body over 1 MiB (files go up through upload links), a file read or written through the files calls, or a build.",
	"validation_failed":      "Some fields are not valid; fix the fields listed and try again.",
	"size_not_allowed":       "The size does not fit the tier or the studio's limits. Standard sizes go in 0.25 vCPU steps, high-frequency sizes are whole vCPUs, and memory is 0.5 to 4 GB per vCPU.",
	"not_available_yet":      "This field or call is not available yet on this API. Leave it out.",
	"rate_limited":           "Over the rate limit. Wait the Retry-After time, then try once more.",
	"internal":               "A fault on the API's side. It is safe to try the same call again.",
	"capacity_unavailable":   "No capacity in any region tried (see regions_tried). Allow more regions or continents, or try again shortly.",
	"unavailable":            "The API is briefly unavailable. Try again in a few seconds.",
	"bad_request":            "The request is malformed or has a field the call does not take (errors names it). Fix the request; the same one again will fail the same way.",
	"studio_required":        "This member belongs to several studios. Name the studio in the Studio header.",
	"studio_closed":          "The studio is closed. Its owners can still read and download its data for 30 days after it closed; nothing else works.",
	"lab_only":               "This call works only on the API's own test setup. Leave it out.",
	"mfa_required":           "Owners and admins must sign in with two-factor authentication. Sign in again with it, then try again.",
	"sign_in_again":          "This change needs a sign-in with two-factor authentication in the last 10 minutes. Sign in again, then try once more.",
	"sso_required":           "This email's company uses single sign-on. Sign in again through the company's own sign-in, then try again.",
	"plan_changed":           "A game server or a backup changed since the preview. Ask for a new preview, check it, then run that one.",
	"unsupported_image":      "The picture is not a PNG, JPEG or WebP the API can read, is outside the size the call allows, or its Content-Type names another type. Send a plain PNG, JPEG or WebP of the right size.",
	"agent_not_allowed":      "The member who connected this AI agent did not switch this on. They connect it again with that permission.",
	"agent_spending_cap":     "This AI agent reached its monthly spending cap. It can start game servers again on the 1st (UTC).",
	"link_address_changed":   "This download link was made from another address. Make a new link from the address that will download it.",
	"signups_closed":         "Open sign-up is closed. A person invited to a studio signs up with the invitation's link.",
}

func (p *Problem) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s failed: %d", p.Method, p.Path, p.Status)
	if p.Code != "" {
		fmt.Fprintf(&b, " %s", p.Code)
	}
	if p.Detail != "" {
		fmt.Fprintf(&b, ": %s", p.Detail)
	} else if p.Title != "" {
		fmt.Fprintf(&b, ": %s", p.Title)
	}
	for _, f := range p.Errors {
		fmt.Fprintf(&b, "\n- %s: %s", f.Field, firstNonEmpty(f.Message, f.Code))
	}
	if len(p.RegionsTried) > 0 {
		fmt.Fprintf(&b, "\nRegions tried: %s", joinRaw(p.RegionsTried))
	}
	if p.RetryAfter != "" {
		fmt.Fprintf(&b, "\nRetry after %s seconds.", p.RetryAfter)
	}
	if step := NextStep[p.Code]; step != "" {
		fmt.Fprintf(&b, "\nNext: %s", step)
	} else if p.Status == http.StatusUnauthorized {
		fmt.Fprintf(&b, "\nNext: %s", NextStep["unauthenticated"])
	}
	if p.RequestID != "" {
		fmt.Fprintf(&b, "\nRequest ID (quote it to support): %s", p.RequestID)
	}
	return b.String()
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

func joinRaw(items []json.RawMessage) string {
	parts := make([]string, len(items))
	for i, r := range items {
		parts[i] = string(r)
	}
	return strings.Join(parts, ", ")
}

// DefaultUserAgent names this module and the version a program was built with, so the API's logs say which client
// called: "studio-go/v0.3.1", or "studio-go" in a build that does not record it.
func DefaultUserAgent() string {
	const path = "github.com/survivalservers/studio-go"
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, m := range append([]*debug.Module{&bi.Main}, bi.Deps...) {
			if m.Path == path && m.Version != "" && m.Version != "(devel)" {
				return "studio-go/" + m.Version
			}
		}
	}
	return "studio-go"
}

// ErrNoKey is returned before any call when the client has no key.
var ErrNoKey = errors.New("no API key: give New a key from the panel (API keys), with only the scopes the program needs")

// Call is one request.
type Call struct {
	Method string
	Path   string // starting with /v0/
	Query  url.Values
	Body   any
	// IdempotencyKey is sent on POST and PATCH; a random one is made when empty. A retry of the same call
	// must reuse it, so callers that retry set it themselves.
	IdempotencyKey string
	Accept         string
}

// File is a Call.Body sent as a file's own bytes rather than JSON: the picture for an icon or a banner
// (PUT /v0/studio/icon, /v0/apps/{app}/icon and /banner). The API reads the type from the bytes; ContentType, when
// set, must name the same type (empty sends application/octet-stream).
type File struct {
	Data        []byte
	ContentType string
}

// Reply is a successful reply.
type Reply struct {
	Status   int
	Body     []byte
	Location string // for 3xx replies (downloads)
	Header   http.Header
}

// Do sends one call. 2xx and 3xx replies are returned; anything else becomes a *Problem.
func (c *Client) Do(ctx context.Context, call Call) (*Reply, error) {
	resp, err := c.send(ctx, c.http, call)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, fmt.Errorf("%s %s: reading the reply: %w", call.Method, call.Path, err)
	}
	return &Reply{Status: resp.StatusCode, Body: data, Location: resp.Header.Get("Location"), Header: resp.Header}, nil
}

// Stream sends one call and hands back the open reply body for the caller to read and close: followed logs and
// the event stream. Errors are the same as Do's.
func (c *Client) Stream(ctx context.Context, call Call) (io.ReadCloser, error) {
	resp, err := c.send(ctx, c.streams, call)
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

// send makes the request, trying again as SetRetry says; a reply that is not 2xx or 3xx is read and turned into a
// *Problem.
func (c *Client) send(ctx context.Context, hc *http.Client, call Call) (*http.Response, error) {
	if c.key == "" {
		return nil, ErrNoKey
	}
	u := *c.base
	u.Path = c.base.Path + call.Path
	if len(call.Query) > 0 {
		u.RawQuery = call.Query.Encode()
	}
	var body []byte
	contentType := "application/json"
	if f, ok := call.Body.(File); ok { // a file's own bytes: an icon or a banner
		body, contentType = f.Data, firstNonEmpty(f.ContentType, "application/octet-stream")
	} else if call.Body != nil {
		buf, err := json.Marshal(call.Body)
		if err != nil {
			return nil, err
		}
		body = buf
	}
	key := ""
	if call.Method == http.MethodPost || call.Method == http.MethodPatch {
		key = firstNonEmpty(call.IdempotencyKey, NewIdempotencyKey()) // one key for every try of this call
	}
	tries, sleep := max(c.tries, 1), c.sleep
	if sleep == nil { // a Client made without New
		sleep = sleepCtx
	}
	for try := 1; ; try++ {
		var rd io.Reader
		if body != nil {
			rd = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, call.Method, u.String(), rd)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.key)
		req.Header.Set("Accept", firstNonEmpty(call.Accept, "application/json"))
		if c.userAgent != "" {
			req.Header.Set("User-Agent", c.userAgent)
		}
		if call.Body != nil {
			req.Header.Set("Content-Type", contentType)
		}
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		resp, err := hc.Do(req)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", call.Method, call.Path, err)
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 400 {
			return resp, nil
		}
		p := readProblem(resp, call)
		if try < tries && again(call.Method, key != "", p) {
			if wait := retryWait(p.RetryAfter, try); wait <= c.maxWait && sleep(ctx, wait) == nil {
				continue
			}
		}
		return nil, p
	}
}

// readProblem reads a reply that is not 2xx or 3xx into a *Problem, and closes it.
func readProblem(resp *http.Response, call Call) *Problem {
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	p := &Problem{Status: resp.StatusCode, Method: call.Method, Path: call.Path}
	if json.Unmarshal(data, p) != nil || (p.Code == "" && p.Detail == "" && p.Title == "") {
		p.Detail = strings.TrimSpace(string(data))
		if len(p.Detail) > 500 {
			p.Detail = p.Detail[:500] + "..."
		}
	}
	p.Status = resp.StatusCode
	p.RetryAfter = resp.Header.Get("Retry-After")
	if p.RequestID == "" {
		p.RequestID = resp.Header.Get("Request-Id")
	}
	return p
}

// JSON sends a call and decodes the reply into out (when out is not nil).
func (c *Client) JSON(ctx context.Context, call Call, out any) error {
	r, err := c.Do(ctx, call)
	if err != nil {
		return err
	}
	if out == nil || len(bytes.TrimSpace(r.Body)) == 0 {
		return nil
	}
	if err := json.Unmarshal(r.Body, out); err != nil {
		return fmt.Errorf("%s %s: the reply is not the JSON expected: %w", call.Method, call.Path, err)
	}
	return nil
}

// Get reads one object as generic JSON.
func (c *Client) Get(ctx context.Context, path string, query url.Values) (map[string]any, error) {
	var out map[string]any
	err := c.JSON(ctx, Call{Method: http.MethodGet, Path: path, Query: query}, &out)
	return out, err
}

// DownloadLink asks a download call (logs archive, a file of a game server or persistent server) for its link. The API answers
// a JSON request with {"url", "expires_at"} and anything else with a redirect; both are understood.
func (c *Client) DownloadLink(ctx context.Context, path string, query url.Values) (string, error) {
	reply, err := c.Do(ctx, Call{Method: http.MethodGet, Path: path, Query: query, Accept: "application/json"})
	if err != nil {
		return "", err
	}
	if reply.Location != "" {
		return reply.Location, nil
	}
	var link struct {
		URL string `json:"url"`
	}
	if json.Unmarshal(reply.Body, &link) != nil || link.URL == "" {
		return "", fmt.Errorf("GET %s: the API sent no download link", path)
	}
	return link.URL, nil
}

// NewIdempotencyKey returns a random key.
func NewIdempotencyKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return "idem-" + hex.EncodeToString(b)
}

// PathEscape escapes one path segment (an ID, slug, name or region).
func PathEscape(s string) string { return url.PathEscape(s) }

// Itoa is strconv.Itoa, for query values.
func Itoa(n int) string { return strconv.Itoa(n) }

func itoa(n int) string { return strconv.Itoa(n) }
