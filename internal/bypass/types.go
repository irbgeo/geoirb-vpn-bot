package bypass

import (
	"net/http"
	"time"
)

// File is one downloaded list, ready to send as a document.
type File struct {
	Name string // last part of the URL, e.g. "amnezia.json"
	Data []byte
}

// Input configures New.
type Input struct {
	URLs   []string
	TTL    time.Duration    // how long a download is reused
	Client *http.Client     // nil = 15s timeout client
	Now    func() time.Time // nil = time.Now
}
