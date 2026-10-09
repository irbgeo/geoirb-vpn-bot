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
	routes    []bool // every RouteBot call
}

func (s *fakeNet) LastHandshake(context.Context) (time.Time, error) {
	return s.handshake, s.err
}

func (s *fakeNet) RouteBot(_ context.Context, viaTunnel bool) error {
	s.routes = append(s.routes, viaTunnel)
	return nil
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
		require.Len(t, n.routes, 1, "same state: route untouched")

		time.Sleep(4 * time.Minute)
		st, changed, err = w.Check(ctx)
		require.NoError(t, err)
		require.Equal(t, Down, st)
		require.True(t, changed)
		require.Equal(t, []bool{true, false}, n.routes)

		n.handshake = time.Now()
		st, changed, err = w.Check(ctx)
		require.NoError(t, err)
		require.Equal(t, Up, st)
		require.True(t, changed)
		require.Equal(t, []bool{true, false, true}, n.routes)
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

func TestCheckNeverHandshakedIsDown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		n := &fakeNet{}
		w := New(n, 3*time.Minute)

		st, changed, err := w.Check(context.Background())
		require.NoError(t, err)
		require.Equal(t, Down, st)
		require.True(t, changed)
		require.Equal(t, []bool{false}, n.routes, "a stale bot rule is removed at start")
	})
}
