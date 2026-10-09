// Package tunnel watches the tunnel to the exit server and keeps the bot's
// own traffic on it only while it works.
package tunnel

import (
	"context"
	"time"
)

// watcher remembers the last state; one goroutine calls Check.
type watcher struct {
	net     Net
	maxAge  time.Duration
	state   State
	started time.Time
}

// New creates a watcher: a handshake older than maxAge means down. During
// the first maxAge after New, no handshake at all is Unknown, not down:
// right after boot the tunnel may not have shaken hands yet.
func New(
	net Net,
	maxAge time.Duration,
) *watcher {
	return &watcher{
		net:     net,
		maxAge:  maxAge,
		started: time.Now(),
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
	if last.IsZero() && time.Since(s.started) < s.maxAge {
		return s.state, false, nil // just started: no alert, no route change
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
