package tunnel

import (
	"context"
	"time"
)

// Net is what the watcher needs from the host.
type Net interface {
	// LastHandshake of the exit peer; zero = never.
	LastHandshake(ctx context.Context) (time.Time, error)
	// RouteBot puts the bot's own traffic through the tunnel (true) or direct (false).
	RouteBot(ctx context.Context, viaTunnel bool) error
}

// State of the tunnel as last seen.
type State int

// Tunnel states; Unknown until the first good check.
const (
	Unknown State = iota
	Up
	Down
)
