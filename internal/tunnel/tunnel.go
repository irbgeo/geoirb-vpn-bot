// Package tunnel watches the tunnel to the exit server and keeps the bot's
// own traffic on it only while it works.
package tunnel

import (
	"context"
	"time"
)

// watcher remembers the last state; one goroutine calls Check.
type watcher struct {
	net    Net
	maxAge time.Duration
	state  State
}

// New creates a watcher: a handshake older than maxAge means down.
func New(
	net Net,
	maxAge time.Duration,
) *watcher {
	return &watcher{
		net:    net,
		maxAge: maxAge,
	}
}

// Check reads the handshake, fixes the bot route on a change and reports it.
// changed is false when the state is the same as last time (no alert).
// On an error the state stays as it was and the next Check tries again.
func (s *watcher) Check(ctx context.Context) (st State, changed bool, err error) {
	last, err := s.net.LastHandshake(ctx)
	if err != nil {
		return s.state, false, err
	}
	st = Down
	if !last.IsZero() && time.Since(last) <= s.maxAge {
		st = Up
	}
	if st == s.state {
		return st, false, nil
	}
	err = s.net.RouteBot(ctx, st == Up)
	if err != nil {
		return s.state, false, err
	}
	s.state = st
	return st, true, nil
}
