package service

import "sync"

// logOnce remembers which (key, error kind) pairs were already logged, so a
// key that fails the same way every minute fills the log once. It is never
// cleared: a restart logs each problem again. The zero value is ready.
type logOnce struct {
	mu   sync.Mutex
	seen map[string]bool
}

// first reports true only the first time it sees this key and kind.
func (s *logOnce) first(publicKey string, kind string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen == nil {
		s.seen = make(map[string]bool)
	}
	id := publicKey + "|" + kind
	if s.seen[id] {
		return false
	}
	s.seen[id] = true
	return true
}
