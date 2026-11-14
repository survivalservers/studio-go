package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"
)

// PartLinks reads the part links an upload in parts carries (its "upload": "part_bytes" and "parts", each a "number"
// and a "url"), as the API answers a save upload over 5 GiB or reads one while it waits. ok is false when the upload is
// one PUT to its "url" instead.
func PartLinks(upload map[string]any) (partBytes int64, links map[int]string, ok bool) {
	up, _ := upload["upload"].(map[string]any)
	if up == nil {
		return 0, nil, false
	}
	pb, _ := up["part_bytes"].(float64)
	parts, _ := up["parts"].([]any)
	if pb <= 0 || len(parts) == 0 {
		return 0, nil, false
	}
	links = map[int]string{}
	for _, p := range parts {
		m, _ := p.(map[string]any)
		n, _ := m["number"].(float64)
		u, _ := m["url"].(string)
		if n < 1 || u == "" {
			return 0, nil, false
		}
		links[int(n)] = u
	}
	return int64(pb), links, true
}

// SendParts sends a file in the parts an upload names (a save zip over 5 GiB, as a zip build), each partBytes
// long and the last what is left, parallel at once (3 when 0), and returns every part's number and ETag in number
// order, for the upload's complete call. Part links last an hour: one refused is replaced by fresh, which reads the
// upload again, once for all the parts that saw the same links refused; refused again, the upload stops. progress, when
// set, hears the bytes sent so far.
func SendParts(ctx context.Context, f *os.File, size, partBytes int64, links map[int]string,
	fresh func(context.Context) (map[int]string, error), parallel int, progress func(int64)) ([]UploadedPart, error) {
	if partBytes <= 0 || size <= 0 {
		return nil, fmt.Errorf("parts of %d bytes for a file of %d", partBytes, size)
	}
	total := int((size + partBytes - 1) / partBytes)
	if len(links) != total {
		return nil, fmt.Errorf("the API sent %d part links for %d parts of %d bytes", len(links), total, partBytes)
	}
	if parallel <= 0 {
		parallel = 3
	}
	storage := &http.Client{Timeout: 15 * time.Minute}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var mu sync.Mutex // guards links, gen, out, sent and failed
	gen := 0
	refresh := func(g int) error {
		mu.Lock()
		defer mu.Unlock()
		if gen != g {
			return nil // another part already took fresh links
		}
		if fresh == nil {
			return errLinkRefused
		}
		l, err := fresh(ctx)
		if err != nil {
			return err
		}
		for n, u := range l {
			links[n] = u
		}
		gen++
		return nil
	}
	out := make([]UploadedPart, total)
	var sent int64
	var failed error
	jobs := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < parallel; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := range jobs {
				offset := int64(n-1) * partBytes
				length := min(partBytes, size-offset)
				mu.Lock()
				link, g := links[n], gen
				mu.Unlock()
				etag, err := putPart(ctx, storage, link, f, offset, length)
				if errors.Is(err, errLinkRefused) {
					if err = refresh(g); err == nil {
						mu.Lock()
						link = links[n]
						mu.Unlock()
						etag, err = putPart(ctx, storage, link, f, offset, length)
					}
				}
				mu.Lock()
				if err != nil {
					if failed == nil {
						failed = fmt.Errorf("part %d of %d: %w", n, total, err)
					}
					mu.Unlock()
					cancel()
					continue
				}
				out[n-1] = UploadedPart{Number: n, ETag: etag}
				sent += length
				now := sent
				mu.Unlock()
				if progress != nil {
					progress(now)
				}
			}
		}()
	}
feed:
	for n := 1; n <= total; n++ {
		select {
		case jobs <- n:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	if failed != nil {
		return nil, failed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
