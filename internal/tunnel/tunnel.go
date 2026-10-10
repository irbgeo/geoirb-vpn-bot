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
// right after boot the tunnel may not have shaken hands yet. In that window
// the bot route is left as it is, so a rule left by the last run stays until
// the window ends (removing it would send the bot direct after every deploy).
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

// Check reads the handshake and sets the bot route for the state it finds,
// every time: someone else (a systemd-networkd restart) may have removed the
// rules while the state stayed the same. The one exception is the start
// window (see New): no handshake yet changes neither state nor route.
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
	err = s.net.RouteBot(ctx, st == Up)
	if err != nil {
		return s.state, false, err
	}
	changed = st != s.state
	s.state = st
	return st, changed, nil
}
