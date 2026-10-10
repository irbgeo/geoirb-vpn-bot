package tunnel

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeNet struct {
	handshake time.Time
	err       error
	routeErr  error
	routes    []bool // every RouteBot call
}

func (s *fakeNet) LastHandshake(context.Context) (time.Time, error) {
	return s.handshake, s.err
}

func (s *fakeNet) RouteBot(_ context.Context, viaTunnel bool) error {
	s.routes = append(s.routes, viaTunnel)
	return s.routeErr
}

func TestCheckFollowsTheHandshake(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		n := &fakeNet{handshake: time.Now()}
		w := New(n, 3*time.Minute)

		st, changed, err := w.Check(ctx)
		require.NoError(t, err)
		require.Equal(t, Up, st)
		require.True(t, changed, "the first state is a change")
		require.Equal(t, []bool{true}, n.routes)

		st, changed, err = w.Check(ctx)
		require.NoError(t, err)
		require.Equal(t, Up, st)
		require.False(t, changed)
		wantRoutes := []bool{
			true,
			true,
		}
		require.Equal(t, wantRoutes, n.routes, "same state: the rules are put back if someone removed them")

		time.Sleep(4 * time.Minute)
		st, changed, err = w.Check(ctx)
		require.NoError(t, err)
		require.Equal(t, Down, st)
		require.True(t, changed)
		wantRoutes = []bool{
			true,
			true,
			false,
		}
		require.Equal(t, wantRoutes, n.routes)

		st, changed, err = w.Check(ctx)
		require.NoError(t, err)
		require.Equal(t, Down, st)
		require.False(t, changed)
		wantRoutes = []bool{
			true,
			true,
			false,
			false,
		}
		require.Equal(t, wantRoutes, n.routes, "still down: still direct")

		n.handshake = time.Now()
		st, changed, err = w.Check(ctx)
		require.NoError(t, err)
		require.Equal(t, Up, st)
		require.True(t, changed)
		wantRoutes = []bool{
			true,
			true,
			false,
			false,
			true,
		}
		require.Equal(t, wantRoutes, n.routes)
	})
}

func TestCheckErrorKeepsTheState(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		n := &fakeNet{handshake: time.Now()}
		w := New(n, 3*time.Minute)
		_, _, err := w.Check(ctx)
		require.NoError(t, err)

		n.err = errors.New("awg broke")
		n.handshake = time.Time{}
		st, changed, err := w.Check(ctx)
		require.ErrorIs(t, err, n.err)
		require.Equal(t, Up, st, "last known state")
		require.False(t, changed)
		require.Len(t, n.routes, 1, "no RouteBot on an error")
	})
}

func TestCheckRouteErrorKeepsTheState(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		n := &fakeNet{
			handshake: time.Now(),
			routeErr:  errors.New("ip broke"),
		}
		w := New(n, 3*time.Minute)

		st, changed, err := w.Check(ctx)
		require.ErrorIs(t, err, n.routeErr)
		require.Equal(t, Unknown, st, "the change did not happen")
		require.False(t, changed)

		n.routeErr = nil
		st, changed, err = w.Check(ctx)
		require.NoError(t, err)
		require.Equal(t, Up, st)
		require.True(t, changed, "the alert comes once the route is set")

		n.routeErr = errors.New("ip broke again")
		st, changed, err = w.Check(ctx)
		require.ErrorIs(t, err, n.routeErr)
		require.Equal(t, Up, st)
		require.False(t, changed)
	})
}

func TestCheckNeverHandshakedIsUnknownRightAfterStart(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		n := &fakeNet{}
		w := New(n, 3*time.Minute)

		st, changed, err := w.Check(ctx)
		require.NoError(t, err)
		require.Equal(t, Unknown, st, "just booted: the tunnel may not have shaken hands yet")
		require.False(t, changed)
		require.Empty(t, n.routes)

		time.Sleep(2 * time.Minute)
		st, changed, err = w.Check(ctx)
		require.NoError(t, err)
		require.Equal(t, Unknown, st)
		require.False(t, changed)
		require.Empty(t, n.routes)

		time.Sleep(time.Minute)
		st, changed, err = w.Check(ctx)
		require.NoError(t, err)
		require.Equal(t, Down, st)
		require.True(t, changed)
		require.Equal(t, []bool{false}, n.routes, "a stale bot rule is removed")
	})
}

func TestCheckOldHandshakeIsDownAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := &fakeNet{handshake: time.Now().Add(-time.Hour)}
		w := New(n, 3*time.Minute)

		st, changed, err := w.Check(context.Background())
		require.NoError(t, err)
		require.Equal(t, Down, st)
		require.True(t, changed)
		require.Equal(t, []bool{false}, n.routes, "a stale bot rule is removed at start")
	})
}

func TestCheckInterfaceGoneIsDown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		n := &fakeNet{handshake: time.Now()}
		w := New(n, 3*time.Minute)
		_, _, err := w.Check(ctx)
		require.NoError(t, err)

		time.Sleep(3 * time.Minute)
		n.handshake = time.Time{} // hostNet answers "never" when awg-exit is gone
		st, changed, err := w.Check(ctx)
		require.NoError(t, err)
		require.Equal(t, Down, st)
		require.True(t, changed, "admins are told")
		wantRoutes := []bool{
			true,
			false,
		}
		require.Equal(t, wantRoutes, n.routes, "the bot leaves the dead route")
	})
}
