package client

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/survivalservers/studio-go/chunks"
)

// A Windows build sent as only the chunks the studio's store does not hold yet: the client cuts every file into the
// store's own chunks (package chunks), sends the build's file list, asks which chunks the studio lacks, and sends only
// those. A new build of a game that changed a few files sends a few chunks instead of the whole zip.

const (
	maxChunkList   = 256 << 20 // the API's limit on a file list
	chunksPerAsk   = 5000      // the most chunks one POST /v0/builds/{id}/chunks names
	chunkLinkGrace = 30 * time.Second
)

// ErrChunksNotTaken is returned (wrapped) by UploadChunks for a folder when the API does not take builds sent as
// chunks: zip the folder and upload the zip.
var ErrChunksNotTaken = errors.New("this API does not take builds sent as chunks")

// listLine is one line of a chunk build's file list: a file, or a folder with no files in it.
type listLine struct {
	Path       string     `json:"path"`
	Folder     bool       `json:"folder,omitempty"`
	Size       int64      `json:"size"`
	SHA256     string     `json:"sha256,omitempty"`
	Chunks     [][2]any   `json:"chunks"`
	Executable bool       `json:"executable,omitempty"`
	Modified   *time.Time `json:"modified,omitempty"`
}

// chunkRef is a chunk's name and size.
type chunkRef struct {
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}

// buildEntry is one file or empty folder of the build being sent.
type buildEntry struct {
	path   string // with / between folders, from the build's top
	folder bool
	size   int64
	mode   fs.FileMode
	mod    time.Time
	open   func() (io.ReadCloser, error)
}

// buildFiles lists a zip's entries or a folder's files, folders first in each folder's order as stored: a zip's
// order, or a folder's names in order.
func buildFiles(path string) (entries []buildEntry, closer func(), err error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	if st.IsDir() {
		entries, err := folderFiles(path)
		return entries, func() {}, err
	}
	if !strings.EqualFold(filepath.Ext(path), ".zip") {
		return nil, nil, fmt.Errorf("%s is not a .zip file or a folder", path)
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, nil, err
	}
	for _, f := range zr.File {
		name := f.Name
		if f.Mode()&fs.ModeSymlink != 0 {
			zr.Close()
			return nil, nil, fmt.Errorf("%q in the zip is a link; a build holds no links", name)
		}
		if strings.HasSuffix(name, "/") {
			entries = append(entries, buildEntry{path: strings.TrimSuffix(name, "/"), folder: true})
			continue
		}
		f := f
		entries = append(entries, buildEntry{path: name, size: int64(f.UncompressedSize64), mode: f.Mode(), mod: f.Modified,
			open: func() (io.ReadCloser, error) { return f.Open() }})
	}
	return entries, func() { zr.Close() }, nil
}

// folderFiles lists a folder's files and empty folders, with / between folders.
func folderFiles(root string) ([]buildEntry, error) {
	var entries []buildEntry
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s is a link; a build holds no links", rel)
		}
		if d.IsDir() {
			empty, err := emptyFolder(p)
			if err != nil {
				return err
			}
			if empty {
				entries = append(entries, buildEntry{path: rel, folder: true})
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a plain file", rel)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		entries = append(entries, buildEntry{path: rel, size: info.Size(), mode: info.Mode(), mod: info.ModTime(),
			open: func() (io.ReadCloser, error) { return os.Open(p) }})
		return nil
	})
	return entries, err
}

func emptyFolder(p string) (bool, error) {
	f, err := os.Open(p)
	if err != nil {
		return false, err
	}
	defer f.Close()
	_, err = f.Readdirnames(1)
	if errors.Is(err, io.EOF) {
		return true, nil
	}
	return false, err
}

// eachChunk cuts one file into the store's chunks and calls fn with each (its bytes are valid until fn returns).
func eachChunk(e buildEntry, fn func(sum [32]byte, data []byte) error) error {
	rc, err := e.open()
	if err != nil {
		return fmt.Errorf("%s: %w", e.path, err)
	}
	defer rc.Close()
	c := chunks.New(rc)
	for {
		data, err := c.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%s: %w", e.path, err)
		}
		if err := fn(sha256.Sum256(data), data); err != nil {
			return err
		}
	}
}

// chunkPlan is the build's file list and what it names.
type chunkPlan struct {
	list     []byte
	files    int
	bytes    int64
	order    []chunkRef     // every distinct chunk, in the order the files first name them
	inFile   map[string]int // a distinct chunk's first file (an index into entries)
	needFile map[int]bool   // the files that hold a chunk still to send
}

// planChunks reads every file once: its SHA-256, its chunks, and its line of the file list.
func planChunks(entries []buildEntry) (*chunkPlan, error) {
	p := &chunkPlan{inFile: map[string]int{}, needFile: map[int]bool{}}
	var list bytes.Buffer
	enc := json.NewEncoder(&list)
	for i, e := range entries {
		if e.folder {
			if err := enc.Encode(listLine{Path: e.path, Folder: true, Chunks: [][2]any{}}); err != nil {
				return nil, err
			}
			continue
		}
		whole := sha256.New()
		line := listLine{Path: e.path, Chunks: [][2]any{}, Executable: e.mode&0o111 != 0}
		if !e.mod.IsZero() && !e.mod.Before(time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)) {
			mod := e.mod.UTC()
			line.Modified = &mod
		}
		err := eachChunk(e, func(sum [32]byte, data []byte) error {
			whole.Write(data)
			name := hex.EncodeToString(sum[:])
			line.Chunks = append(line.Chunks, [2]any{name, len(data)})
			line.Size += int64(len(data))
			if _, ok := p.inFile[name]; !ok {
				p.inFile[name] = i
				p.order = append(p.order, chunkRef{SHA256: name, Bytes: int64(len(data))})
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		line.SHA256 = hex.EncodeToString(whole.Sum(nil))
		if err := enc.Encode(line); err != nil {
			return nil, err
		}
		p.files++
		p.bytes += line.Size
		if list.Len() > maxChunkList {
			return nil, fmt.Errorf("the build's file list is over %d MiB: upload it as a zip", maxChunkList>>20)
		}
	}
	if p.files == 0 {
		return nil, errors.New("the build holds no files")
	}
	p.list = list.Bytes()
	return p, nil
}

// chunkBuild is what the API answers when a chunk build is made or read.
type chunkBuild struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	Upload *struct {
		List *struct {
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"list"`
	} `json:"upload"`
}

// chunkLink is one chunk's storage link.
type chunkLink struct {
	SHA256    string            `json:"sha256"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers"`
	ExpiresAt time.Time         `json:"expires_at"`
}

// chunksNotTaken says whether the API refused the build because it does not take chunk builds: an older API answers
// an unknown field with bad_request.
func chunksNotTaken(err error) bool {
	var p *Problem
	return errors.As(err, &p) && (p.Status == http.StatusBadRequest || p.Code == "not_available_yet")
}

// UploadChunks uploads a Windows server zip, or a folder, as a new build of app, sending only the chunks the
// studio's store does not hold yet: every file is cut into the store's own chunks, the build's file list goes up,
// the API names the chunks the studio lacks, and only those are sent (opt.Parallel at once), each with its own link.
// A new build that changed a few files sends a few chunks. When the API does not take builds sent as chunks, a zip
// is uploaded whole with UploadZip instead (opt.ResumeDir applies to that only); a folder then fails with
// ErrChunksNotTaken. opt.Progress and opt.OnPart count chunks sent. It returns the build as the API last sent it.
func (c *Client) UploadChunks(ctx context.Context, app, path string, opt UploadOptions) (map[string]any, error) {
	entries, closeFiles, err := buildFiles(path)
	if err != nil && strings.EqualFold(filepath.Ext(path), ".zip") && errors.Is(err, zip.ErrFormat) {
		// A file that cannot be read as a zip goes up whole, as before: processing says what is wrong with it.
		return c.UploadZip(ctx, app, path, opt)
	}
	if err != nil {
		return nil, err
	}
	defer closeFiles()
	plan, err := planChunks(entries)
	if err != nil {
		return nil, err
	}
	listSum := sha256.Sum256(plan.list)
	reply, err := c.Do(ctx, Call{Method: http.MethodPost, Path: "/v0/apps/" + PathEscape(app) + "/builds", IdempotencyKey: opt.IdempotencyKey, Body: map[string]any{
		"os": "windows",
		"source": map[string]any{"type": "upload", "filename": strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)) + ".files.jsonl",
			"bytes": len(plan.list), "sha256": hex.EncodeToString(listSum[:]), "chunks": map[string]any{"files": plan.files, "bytes": plan.bytes}},
	}})
	if err != nil {
		if chunksNotTaken(err) {
			if strings.EqualFold(filepath.Ext(path), ".zip") {
				return c.UploadZip(ctx, app, path, opt)
			}
			return nil, fmt.Errorf("%w: zip %s and upload the zip (%v)", ErrChunksNotTaken, path, err)
		}
		return nil, err
	}
	var b chunkBuild
	if err := json.Unmarshal(reply.Body, &b); err != nil {
		return nil, fmt.Errorf("the build reply is not the JSON expected: %w", err)
	}
	if reply.Header.Get("Idempotent-Replayed") == "true" {
		reply, err = c.Do(ctx, Call{Method: http.MethodGet, Path: "/v0/builds/" + PathEscape(b.ID)})
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(reply.Body, &b); err != nil {
			return nil, fmt.Errorf("the build reply is not the JSON expected: %w", err)
		}
	}
	if b.Status != "awaiting_upload" {
		var done map[string]any
		err := json.Unmarshal(reply.Body, &done)
		return done, err
	}
	if b.Upload == nil || b.Upload.List == nil || b.Upload.List.URL == "" {
		return nil, fmt.Errorf("the API sent no link for build %s's file list", b.ID)
	}
	storage := opt.Storage
	if storage == nil {
		storage = &http.Client{Timeout: 15 * time.Minute}
	}
	if err := putChunk(ctx, storage, b.Upload.List.URL, b.Upload.List.Headers, plan.list); err != nil {
		return nil, fmt.Errorf("build %s, its file list: %w", b.ID, err)
	}
	if err := c.sendChunks(ctx, storage, b.ID, entries, plan, opt); err != nil {
		return nil, err
	}
	var done map[string]any
	if err := c.JSON(ctx, Call{Method: http.MethodPost, Path: "/v0/builds/" + PathEscape(b.ID) + "/complete", Body: map[string]any{}}, &done); err != nil {
		return nil, err
	}
	return done, nil
}

// askChunks asks which of these chunks the studio lacks and returns a link for each one to send now.
func (c *Client) askChunks(ctx context.Context, build string, refs []chunkRef) ([]chunkLink, error) {
	var out struct {
		Links []chunkLink `json:"links"`
	}
	err := c.JSON(ctx, Call{Method: http.MethodPost, Path: "/v0/builds/" + PathEscape(build) + "/chunks", Body: map[string]any{"chunks": refs}}, &out)
	return out.Links, err
}

// sendChunks asks for the links of every chunk the build names, a page at a time, then reads the files that hold one
// to send again and sends each such chunk once, opt.Parallel at once. A link that has run out is asked for again.
func (c *Client) sendChunks(ctx context.Context, storage *http.Client, build string, entries []buildEntry, plan *chunkPlan, opt UploadOptions) error {
	links := map[string]chunkLink{}
	need := map[string]bool{} // the chunks to send; read by the file reader, never changed once the workers start
	var needBytes int64
	for start := 0; start < len(plan.order); start += chunksPerAsk {
		page := plan.order[start:min(start+chunksPerAsk, len(plan.order))]
		got, err := c.askChunks(ctx, build, page)
		if err != nil {
			return fmt.Errorf("build %s: %w", build, err)
		}
		for _, l := range got {
			links[l.SHA256] = l
		}
	}
	for _, ref := range plan.order {
		if _, ok := links[ref.SHA256]; ok {
			need[ref.SHA256] = true
			plan.needFile[plan.inFile[ref.SHA256]] = true
			needBytes += ref.Bytes
		}
	}
	total := len(links)
	if total == 0 {
		return nil
	}
	parallel := opt.Parallel
	if parallel <= 0 {
		parallel = 3
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type job struct {
		ref  chunkRef
		data []byte
	}
	jobs := make(chan job, parallel)
	var mu sync.Mutex // guards links, done, sent, firstErr and the callbacks
	var firstErr error
	done, sent := 0, int64(0)
	began := time.Now()
	fail := func(err error) {
		mu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		mu.Unlock()
		cancel()
	}
	linkFor := func(ref chunkRef, refused bool) (chunkLink, error) {
		mu.Lock()
		l := links[ref.SHA256]
		mu.Unlock()
		if !refused && time.Until(l.ExpiresAt) > chunkLinkGrace {
			return l, nil
		}
		// A link is one a build at a time while it lives: wait until it has run out, then ask for a fresh one.
		if wait := time.Until(l.ExpiresAt) + time.Second; wait > 0 {
			select {
			case <-ctx.Done():
				return chunkLink{}, ctx.Err()
			case <-time.After(wait):
			}
		}
		got, err := c.askChunks(ctx, build, []chunkRef{ref})
		if err != nil {
			return chunkLink{}, err
		}
		if len(got) != 1 {
			return chunkLink{}, fmt.Errorf("chunk %s: the API sent no fresh link for it", ref.SHA256)
		}
		mu.Lock()
		links[ref.SHA256] = got[0]
		mu.Unlock()
		return got[0], nil
	}
	var wg sync.WaitGroup
	for range parallel {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				var err error
				for try := 0; try < 2; try++ {
					var l chunkLink
					if l, err = linkFor(j.ref, try > 0); err != nil {
						break
					}
					if err = putChunk(ctx, storage, l.URL, l.Headers, j.data); !errors.Is(err, errLinkRefused) {
						break
					}
				}
				if err != nil {
					if ctx.Err() == nil {
						fail(fmt.Errorf("build %s, chunk %s: %w", build, j.ref.SHA256, err))
					}
					return
				}
				mu.Lock()
				done++
				sent += j.ref.Bytes
				if opt.Progress != nil {
					opt.Progress(done, total)
				}
				if opt.OnPart != nil {
					opt.OnPart(UploadProgress{Chunks: true, Parts: done, TotalParts: total, Bytes: sent, TotalBytes: needBytes, Elapsed: time.Since(began)})
				}
				mu.Unlock()
			}
		}()
	}
	queued := map[string]bool{}
	var readErr error
	for i, e := range entries {
		if !plan.needFile[i] || ctx.Err() != nil {
			continue
		}
		readErr = eachChunk(e, func(sum [32]byte, data []byte) error {
			name := hex.EncodeToString(sum[:])
			if !need[name] || queued[name] {
				return nil
			}
			queued[name] = true
			select {
			case jobs <- job{ref: chunkRef{SHA256: name, Bytes: int64(len(data))}, data: bytes.Clone(data)}:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		if readErr != nil {
			break
		}
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	if readErr != nil {
		return readErr
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(queued) != total {
		return fmt.Errorf("build %s: %d of %d chunks to send were found again in the files (did a file change during the upload?)", build, len(queued), total)
	}
	return nil
}

// putChunk sends bytes to a storage link with the headers the API gave, trying up to three times; errLinkRefused when
// storage turns the link away.
func putChunk(ctx context.Context, storage *http.Client, url string, headers map[string]string, data []byte) error {
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(data))
		if err != nil {
			return err
		}
		req.ContentLength = int64(len(data))
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := storage.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			last = err
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		resp.Body.Close()
		if resp.StatusCode/100 == 2 {
			return nil
		}
		last = fmt.Errorf("storage answered %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		if resp.StatusCode == http.StatusForbidden {
			return fmt.Errorf("%w (%w)", last, errLinkRefused)
		}
		if resp.StatusCode == http.StatusBadRequest {
			return last // the bytes are not the ones the link was signed for: another try sends the same bytes
		}
	}
	return last
}
