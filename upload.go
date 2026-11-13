package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// UploadedPart is one finished part of a zip upload.
type UploadedPart struct {
	Number int    `json:"number"`
	ETag   string `json:"etag"`
}

type uploadPlan struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Upload *struct {
		PartBytes int64 `json:"part_bytes"`
		Parts     []struct {
			Number int    `json:"number"`
			URL    string `json:"url"`
		} `json:"parts"`
	} `json:"upload"`
}

// ZipInfo is what the API needs to know about a zip before it hands out upload links.
type ZipInfo struct {
	Filename string
	Bytes    int64
	SHA256   string
}

// InspectZip reads a local zip once for its size and SHA-256.
func InspectZip(path string) (ZipInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return ZipInfo{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return ZipInfo{}, err
	}
	if st.IsDir() {
		return ZipInfo{}, fmt.Errorf("%s is a folder; zip the build first (the zip's top level is the build's folder)", path)
	}
	if !strings.EqualFold(filepath.Ext(path), ".zip") {
		return ZipInfo{}, fmt.Errorf("%s is not a .zip file", path)
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ZipInfo{}, err
	}
	return ZipInfo{Filename: filepath.Base(path), Bytes: st.Size(), SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}

// UploadOptions are the optional parts of UploadZip.
type UploadOptions struct {
	// Storage sends the parts (and never the API key); nil means a client with a 15-minute timeout per part.
	Storage *http.Client
	// Progress hears about each finished part (parts sent by an earlier run included), one call at a time.
	Progress func(done, total int)
	// OnPart hears the same moments with sizes and time, for a speed and a time left; one call at a time.
	OnPart func(UploadProgress)
	// IdempotencyKey makes a repeated upload of the same zip (a re-run CI job) find the first build instead of
	// making a second; a build already uploaded is returned as it is.
	IdempotencyKey string
	// Parallel is how many parts are sent at once; 0 means 3, as the panel's uploader does.
	Parallel int
	// ResumeDir keeps a small record of each upload's finished parts there, so a re-run of the same zip to the same
	// app carries on with the parts left instead of starting again (with or without an idempotency key). The record
	// holds the build's id and each part's number and ETag, nothing secret, and goes once the upload is complete.
	// "" keeps none.
	ResumeDir string
}

// UploadProgress is where an upload stands after a finished part.
type UploadProgress struct {
	Parts, TotalParts int
	// Bytes is what storage holds so far, Resumed the part of it an earlier run sent, TotalBytes the zip's size.
	Bytes, Resumed, TotalBytes int64
	// Elapsed is the time since this run started sending parts.
	Elapsed time.Duration
}

// UploadZip uploads a Windows server zip as a new build of app: the API hands out one storage link per part, each
// part is sent with its exact length (storage refuses a streamed upload), and the parts' ETags complete the
// build. Parts go a few at once; a link that has run out (links last an hour) is replaced by reading the build
// again, so an upload may take as long as it needs. It returns the build as the API last sent it (status
// processing, then ready or failed: wait for it with GET /v0/builds/{id}).
func (c *Client) UploadZip(ctx context.Context, app, path string, opt UploadOptions) (map[string]any, error) {
	info, err := InspectZip(path)
	if err != nil {
		return nil, err
	}
	rec := openUploadRecord(opt.ResumeDir, c.Base(), app, info)
	plan, build, err := c.resumePlan(ctx, rec)
	if err != nil {
		return nil, err
	}
	if build != nil {
		return build, nil
	}
	if plan == nil {
		var reply *Reply
		plan, reply, err = c.startUpload(ctx, app, info, opt.IdempotencyKey)
		if err != nil {
			return nil, err
		}
		if plan.Status != "" && plan.Status != "awaiting_upload" {
			var build map[string]any
			err := json.Unmarshal(reply.Body, &build)
			return build, err
		}
	}
	if plan.Upload == nil || len(plan.Upload.Parts) == 0 || plan.Upload.PartBytes <= 0 {
		return nil, fmt.Errorf("the API sent no upload links for build %s (status %s)", plan.ID, plan.Status)
	}
	rec.begin(plan.ID, plan.Upload.PartBytes)
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	parts, err := c.sendParts(ctx, f, info, plan, rec, opt)
	if err != nil {
		if rec.kept() {
			return nil, fmt.Errorf("%w (the parts already sent are kept: run the same upload again to carry on)", err)
		}
		return nil, err
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].Number < parts[j].Number })
	var done map[string]any
	if err := c.JSON(ctx, Call{Method: http.MethodPost, Path: "/v0/builds/" + PathEscape(plan.ID) + "/complete", Body: map[string]any{"parts": parts}}, &done); err != nil {
		return nil, err
	}
	rec.remove()
	return done, nil
}

// startUpload makes the build; a repeated idempotency key finds the first one, read again for its state and fresh
// links.
func (c *Client) startUpload(ctx context.Context, app string, info ZipInfo, key string) (*uploadPlan, *Reply, error) {
	reply, err := c.Do(ctx, Call{Method: http.MethodPost, Path: "/v0/apps/" + PathEscape(app) + "/builds", IdempotencyKey: key, Body: map[string]any{
		"os":     "windows",
		"source": map[string]any{"type": "upload", "filename": info.Filename, "bytes": info.Bytes, "sha256": info.SHA256},
	}})
	if err != nil {
		return nil, nil, err
	}
	var plan uploadPlan
	if err := json.Unmarshal(reply.Body, &plan); err != nil {
		return nil, nil, fmt.Errorf("the build reply is not the JSON expected: %w", err)
	}
	if reply.Header.Get("Idempotent-Replayed") == "true" {
		return c.readPlan(ctx, plan.ID)
	}
	return &plan, reply, nil
}

// readPlan reads a build for its state and, while it waits for its upload, fresh links.
func (c *Client) readPlan(ctx context.Context, id string) (*uploadPlan, *Reply, error) {
	reply, err := c.Do(ctx, Call{Method: http.MethodGet, Path: "/v0/builds/" + PathEscape(id)})
	if err != nil {
		return nil, nil, err
	}
	var plan uploadPlan
	if err := json.Unmarshal(reply.Body, &plan); err != nil {
		return nil, nil, fmt.Errorf("the build reply is not the JSON expected: %w", err)
	}
	return &plan, reply, nil
}

// resumePlan picks up the build an earlier run of this upload left: still waiting, its plan; uploaded since, the
// build as it is. A build that is gone or failed is forgotten and a new one is made.
func (c *Client) resumePlan(ctx context.Context, rec *uploadRecord) (*uploadPlan, map[string]any, error) {
	if rec.Build == "" {
		return nil, nil, nil
	}
	plan, reply, err := c.readPlan(ctx, rec.Build)
	var p *Problem
	switch {
	case errors.As(err, &p) && p.Status == http.StatusNotFound:
		rec.forget()
		return nil, nil, nil
	case err != nil:
		return nil, nil, err
	}
	switch plan.Status {
	case "awaiting_upload":
		return plan, nil, nil
	case "processing", "ready":
		var build map[string]any
		if err := json.Unmarshal(reply.Body, &build); err != nil {
			return nil, nil, err
		}
		rec.remove()
		return nil, build, nil
	default:
		rec.forget()
		return nil, nil, nil
	}
}

// errLinkRefused is storage turning a part's link away: it has run out (links last an hour) or was refused.
var errLinkRefused = errors.New("the upload link has expired or was refused")

// sendParts sends every part the record does not already hold, opt.Parallel at once, and returns all the parts'
// ETags. A refused link is replaced once by reading the build again; refused again, the upload stops.
func (c *Client) sendParts(ctx context.Context, f *os.File, info ZipInfo, plan *uploadPlan, rec *uploadRecord, opt UploadOptions) ([]UploadedPart, error) {
	storage := opt.Storage
	if storage == nil {
		storage = &http.Client{Timeout: 15 * time.Minute}
	}
	parallel := opt.Parallel
	if parallel <= 0 {
		parallel = 3
	}
	partBytes, total := plan.Upload.PartBytes, len(plan.Upload.Parts)
	size := func(n int) int64 { return min(partBytes, info.Bytes-int64(n-1)*partBytes) }

	var mu sync.Mutex // guards links, gen, parts, sent, rec and the callbacks
	links := map[int]string{}
	for _, p := range plan.Upload.Parts {
		links[p.Number] = p.URL
	}
	gen := 0
	parts := make([]UploadedPart, 0, total)
	var resumed int64
	for _, p := range rec.Parts {
		if _, ok := links[p.Number]; ok && p.ETag != "" {
			parts = append(parts, p)
			resumed += size(p.Number)
		}
	}
	sent := resumed
	have := map[int]bool{}
	for _, p := range parts {
		have[p.Number] = true
	}
	// refresh replaces every link with a fresh one, once for all the parts that saw the links of generation g fail.
	refresh := func(g int) error {
		mu.Lock()
		defer mu.Unlock()
		if gen != g {
			return nil
		}
		fresh, _, err := c.readPlan(ctx, plan.ID)
		if err != nil {
			return err
		}
		if fresh.Status != "awaiting_upload" || fresh.Upload == nil {
			return fmt.Errorf("build %s is %s now, no longer waiting for its upload", plan.ID, fresh.Status)
		}
		for _, p := range fresh.Upload.Parts {
			links[p.Number] = p.URL
		}
		gen++
		return nil
	}
	link := func(n int) (string, int) {
		mu.Lock()
		defer mu.Unlock()
		return links[n], gen
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int)
	var wg sync.WaitGroup
	var firstErr error
	fail := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		mu.Unlock()
		cancel()
	}
	start := time.Now()
	for range parallel {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := range jobs {
				offset, length := int64(n-1)*partBytes, size(n)
				if offset < 0 || length <= 0 {
					fail(fmt.Errorf("upload part %d is outside the file (%d bytes, parts of %d)", n, info.Bytes, partBytes))
					return
				}
				var etag string
				var err error
				for try := 0; ; try++ {
					url, g := link(n)
					etag, err = putPart(ctx, storage, url, f, offset, length)
					if !errors.Is(err, errLinkRefused) || try == 1 {
						break
					}
					if err = refresh(g); err != nil {
						break
					}
				}
				if err != nil {
					if ctx.Err() != nil && !errors.Is(err, errLinkRefused) {
						return // another part failed first
					}
					fail(fmt.Errorf("build %s, part %d of %d: %w", plan.ID, n, total, err))
					return
				}
				mu.Lock()
				parts = append(parts, UploadedPart{Number: n, ETag: etag})
				sent += length
				rec.add(UploadedPart{Number: n, ETag: etag})
				if opt.Progress != nil {
					opt.Progress(len(parts), total)
				}
				if opt.OnPart != nil {
					opt.OnPart(UploadProgress{Parts: len(parts), TotalParts: total, Bytes: sent, Resumed: resumed, TotalBytes: info.Bytes, Elapsed: time.Since(start)})
				}
				mu.Unlock()
			}
		}()
	}
	for _, p := range plan.Upload.Parts {
		n := p.Number
		if have[n] {
			continue
		}
		select {
		case jobs <- n:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return parts, nil
}

// putPart sends one part, trying up to three times; it returns the ETag storage answered with, or errLinkRefused
// when storage turns the link away (a fresh link is the cure, not another try).
func putPart(ctx context.Context, storage *http.Client, url string, f *os.File, offset, length int64) (string, error) {
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, io.NewSectionReader(f, offset, length))
		if err != nil {
			return "", err
		}
		req.ContentLength = length
		resp, err := storage.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			last = err
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			last = fmt.Errorf("storage answered %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
			if resp.StatusCode == http.StatusForbidden {
				return "", fmt.Errorf("%w (%w)", last, errLinkRefused)
			}
			continue
		}
		etag := resp.Header.Get("ETag")
		if etag == "" {
			return "", fmt.Errorf("storage sent no ETag")
		}
		return etag, nil
	}
	return "", last
}

// uploadRecord is what ResumeDir keeps for one upload: the build and the parts storage already holds.
type uploadRecord struct {
	path string // "" keeps nothing

	Build     string         `json:"build"`
	SHA256    string         `json:"sha256"`
	PartBytes int64          `json:"part_bytes"`
	Parts     []UploadedPart `json:"parts"`
}

// openUploadRecord reads the record for this zip, this app and this API address, if dir has one; records left for
// more than 30 days are dropped on the way.
func openUploadRecord(dir, base, app string, info ZipInfo) *uploadRecord {
	if dir == "" {
		return &uploadRecord{}
	}
	sum := sha256.Sum256([]byte(base + "\n" + app + "\n" + info.SHA256))
	rec := &uploadRecord{path: filepath.Join(dir, hex.EncodeToString(sum[:16])+".json"), SHA256: info.SHA256}
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if fi, err := e.Info(); err == nil && !e.IsDir() && strings.HasSuffix(e.Name(), ".json") && time.Since(fi.ModTime()) > 30*24*time.Hour {
				_ = os.Remove(filepath.Join(dir, e.Name()))
			}
		}
	}
	data, err := os.ReadFile(rec.path)
	if err != nil {
		return rec
	}
	var kept uploadRecord
	if json.Unmarshal(data, &kept) != nil || kept.SHA256 != info.SHA256 {
		return rec
	}
	kept.path = rec.path
	return &kept
}

func (r *uploadRecord) kept() bool { return r.path != "" && r.Build != "" }

// begin names the build the parts go to; parts kept for another build or another part size no longer count.
func (r *uploadRecord) begin(build string, partBytes int64) {
	if r.Build != build || r.PartBytes != partBytes {
		r.Parts = nil
	}
	r.Build, r.PartBytes = build, partBytes
	r.save()
}

func (r *uploadRecord) add(p UploadedPart) {
	r.Parts = append(r.Parts, p)
	r.save()
}

func (r *uploadRecord) forget() {
	r.Build, r.PartBytes, r.Parts = "", 0, nil // SHA256 stays: it names the zip
	r.remove()
}

// save writes the record whole (a new file renamed into place), so a run cut off mid-write leaves the last one.
// A record that cannot be written costs only the resume, never the upload.
func (r *uploadRecord) save() {
	if r.path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return
	}
	data, err := json.Marshal(r)
	if err != nil {
		return
	}
	tmp := r.path + ".new"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, r.path)
}

func (r *uploadRecord) remove() {
	if r.path != "" {
		_ = os.Remove(r.path)
	}
}
