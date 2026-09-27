// Package bypass downloads split-tunneling lists (sites and networks that
// must NOT go through the VPN) in the Amnezia app's import format.
package bypass

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"path"
	"sync"
	"time"
)

// maxSize caps one list; the full RU IP list is ~0.7 MB.
const maxSize = 2 << 20

// retryAfter: after a failed refresh, wait this long before downloading
// again, so a GitHub outage doesn't cost every key delivery 15 s per list.
const retryAfter = 5 * time.Minute

// Fetcher downloads the lists and caches them for TTL. If a refresh fails
// it keeps serving the last good copy.
type Fetcher struct {
	urls   []string
	ttl    time.Duration
	client *http.Client
	now    func() time.Time

	mu        sync.Mutex
	cached    []File
	fetchedAt time.Time
	failedAt  time.Time // last failed refresh: no new try for retryAfter
	lastErr   error
}

// New creates a Fetcher.
func New(
	in *Input,
) *Fetcher {
	client := in.Client
	if client == nil {
		client = &http.Client{
			Timeout: 15 * time.Second,
		}
	}
	now := in.Now
	if now == nil {
		now = time.Now
	}
	return &Fetcher{
		urls:   in.URLs,
		ttl:    in.TTL,
		client: client,
		now:    now,
	}
}

// Files returns every list, fresh or from the cache.
//
// The download runs outside the lock, so callers never queue behind it;
// two callers may refresh at the same moment, which is harmless.
func (f *Fetcher) Files(ctx context.Context) ([]File, error) {
	f.mu.Lock()
	now := f.now()
	cached, fresh := f.cached, f.cached != nil && now.Sub(f.fetchedAt) < f.ttl
	backingOff, lastErr := !f.failedAt.IsZero() && now.Sub(f.failedAt) < retryAfter, f.lastErr
	f.mu.Unlock()
	switch {
	case fresh:
		return cached, nil
	case backingOff && cached != nil:
		return cached, nil
	case backingOff:
		return nil, lastErr
	}

	files, err := f.fetchAll(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	if err != nil {
		f.failedAt, f.lastErr = f.now(), err
		if cached != nil {
			log.Printf("bypass: refresh failed, using the last good copy: %v", err)
			return cached, nil
		}
		return nil, err
	}
	f.cached, f.fetchedAt, f.failedAt, f.lastErr = files, f.now(), time.Time{}, nil
	return files, nil
}

func (f *Fetcher) fetchAll(ctx context.Context) ([]File, error) {
	files := make([]File, 0, len(f.urls))
	for _, u := range f.urls {
		data, err := f.fetch(ctx, u)
		if err != nil {
			return nil, fmt.Errorf("bypass: %s: %w", u, err)
		}
		files = append(
			files,
			File{
				Name: path.Base(u),
				Data: data,
			},
		)
	}
	return files, nil
}

// fetch downloads one list and checks it is a JSON array, so an error or
// rate-limit page is never sent to users as a list.
func (f *Fetcher) fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, err
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close() //nolint:errcheck // read-only body, nothing to recover
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSize+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxSize {
		return nil, fmt.Errorf("bigger than %d bytes", maxSize)
	}
	var list []json.RawMessage
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("not a JSON list: %w", err)
	}
	return data, nil
}
