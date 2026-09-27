package bypass

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// listServer serves /a.json and /b.json; body can be swapped per test.
func listServer(t *testing.T, body *atomic.Value, hits *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		b := body.Load().(string)
		if b == "500" {
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(b))
	}))
	t.Cleanup(srv.Close)
	return srv
}

type clock struct {
	t time.Time
}

func (c *clock) now() time.Time {
	return c.t
}

func newFetcher(srv *httptest.Server, c *clock) *Fetcher {
	return New(
		&Input{
			URLs: []string{
				srv.URL + "/lists/a.json",
				srv.URL + "/lists/b.json",
			},
			TTL: time.Hour,
			Now: c.now,
		},
	)
}

const list = `[{"hostname": "sberbank.ru", "ip": ""}]`

func TestFilesDownloadsAllNamedByURL(t *testing.T) {
	var body atomic.Value
	var hits atomic.Int32
	body.Store(list)
	f := newFetcher(listServer(t, &body, &hits), &clock{
		t: time.Now(),
	})

	files, err := f.Files(context.Background())
	require.NoError(t, err)
	require.Len(t, files, 2)
	require.Equal(t, "a.json", files[0].Name)
	require.Equal(t, "b.json", files[1].Name)
	require.Equal(t, list, string(files[0].Data))
}

func TestFilesCachedForTTL(t *testing.T) {
	var body atomic.Value
	var hits atomic.Int32
	body.Store(list)
	c := &clock{
		t: time.Now(),
	}
	f := newFetcher(listServer(t, &body, &hits), c)
	ctx := context.Background()

	_, err := f.Files(ctx)
	require.NoError(t, err)
	_, err = f.Files(ctx)
	require.NoError(t, err)
	require.Equal(t, int32(2), hits.Load(), "second call served from cache")

	c.t = c.t.Add(2 * time.Hour)
	_, err = f.Files(ctx)
	require.NoError(t, err)
	require.Equal(t, int32(4), hits.Load(), "refetched after TTL")
}

func TestFilesFallsBackToLastGoodCopy(t *testing.T) {
	var body atomic.Value
	var hits atomic.Int32
	body.Store(list)
	c := &clock{
		t: time.Now(),
	}
	f := newFetcher(listServer(t, &body, &hits), c)
	ctx := context.Background()
	_, err := f.Files(ctx)
	require.NoError(t, err)

	body.Store("500")
	c.t = c.t.Add(2 * time.Hour)
	files, err := f.Files(ctx)
	require.NoError(t, err, "GitHub down: the last good copy is used")
	require.Equal(t, list, string(files[0].Data))
}

func TestFilesRejectsBadContent(t *testing.T) {
	for name, b := range map[string]string{
		"http error": "500",
		"html page":  "<html>rate limited</html>",
		"not a list": `{"hostname": "x"}`,
		"too big":    "[" + strings.Repeat(" ", maxSize) + "]",
	} {
		var body atomic.Value
		var hits atomic.Int32
		body.Store(b)
		f := newFetcher(listServer(t, &body, &hits), &clock{
			t: time.Now(),
		})

		_, err := f.Files(context.Background())
		require.Error(t, err, name)
	}
}

func TestFilesWaitAfterAFailureBeforeTryingAgain(t *testing.T) {
	var body atomic.Value
	var hits atomic.Int32
	body.Store("500")
	c := &clock{
		t: time.Now(),
	}
	f := newFetcher(listServer(t, &body, &hits), c)
	ctx := context.Background()

	_, err := f.Files(ctx)
	require.Error(t, err)
	tried := hits.Load()
	_, err = f.Files(ctx)
	require.Error(t, err)
	require.Equal(t, tried, hits.Load(), "GitHub is down: no new download for a while")

	body.Store(list)
	c.t = c.t.Add(10 * time.Minute)
	files, err := f.Files(ctx)
	require.NoError(t, err, "tries again later")
	require.Len(t, files, 2)
}
