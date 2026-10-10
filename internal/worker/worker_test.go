package worker

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

type fakeJob struct {
	runs      atomic.Int32
	delivered atomic.Int32
	fail      bool
}

func (s *fakeJob) Maintain(context.Context) (*service.Maintenance, error) {
	s.runs.Add(1)
	if s.fail {
		return nil, errors.New("awg down")
	}
	return &service.Maintenance{}, nil
}

func (s *fakeJob) DeliverMaintenance(context.Context, *service.Maintenance) {
	s.delivered.Add(1)
}

func TestRunStartsAtOnceThenTicks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		job := &fakeJob{}
		w := New(
			job,
			job,
			time.Minute,
		)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute+time.Second)
		defer cancel()

		w.Run(ctx)

		require.Equal(t, int32(4), job.runs.Load(), "at start and on every tick")
		require.Equal(t, job.runs.Load(), job.delivered.Load())
	})
}

func TestRunKeepsGoingAfterErrors(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		job := &fakeJob{
			fail: true,
		}
		w := New(
			job,
			job,
			time.Minute,
		)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute+time.Second)
		defer cancel()

		w.Run(ctx)

		require.Equal(t, int32(3), job.runs.Load())
		require.Zero(t, job.delivered.Load(), "nothing to deliver after a failed run")
	})
}

type slowJob struct {
	fakeJob
	started chan struct{}
	done    atomic.Bool
}

func (s *slowJob) Maintain(ctx context.Context) (*service.Maintenance, error) {
	close(s.started)
	time.Sleep(50 * time.Millisecond)
	if ctx.Err() != nil {
		return nil, ctx.Err() // a run cut in the middle
	}
	s.done.Store(true)
	return &service.Maintenance{}, nil
}

func TestRunFinishesTheCurrentRunOnShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		job := &slowJob{
			started: make(chan struct{}),
		}
		w := New(
			job,
			job,
			time.Hour,
		)
		ctx, cancel := context.WithCancel(context.Background())
		finished := make(chan struct{})
		go func() {
			w.Run(ctx)
			close(finished)
		}()
		<-job.started
		cancel()
		<-finished

		require.True(t, job.done.Load(), "the run was not cut half-way")
	})
}

type deadlineJob struct {
	fakeJob
	hasDeadline atomic.Bool
}

func (s *deadlineJob) Maintain(ctx context.Context) (*service.Maintenance, error) {
	_, ok := ctx.Deadline()
	s.hasDeadline.Store(ok)
	return &service.Maintenance{}, nil
}

func TestRunHasATimeLimit(t *testing.T) {
	job := &deadlineJob{}
	w := New(
		job,
		job,
		time.Hour,
	)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w.Run(ctx)

	require.True(t, job.hasDeadline.Load(), "a hung awg command can't hold a run past the stop timeout")
}

// limitJob uses its whole time limit, then still returns what it found.
type limitJob struct {
	deliverErr error
}

func (s *limitJob) Maintain(ctx context.Context) (*service.Maintenance, error) {
	<-ctx.Done()
	return &service.Maintenance{}, nil
}

func (s *limitJob) DeliverMaintenance(ctx context.Context, _ *service.Maintenance) {
	s.deliverErr = ctx.Err()
}

func TestDeliveryGetsItsOwnTimeLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		job := &limitJob{}
		w := New(
			job,
			job,
			time.Hour,
		)
		start := time.Now()

		w.once(context.Background())
		require.Equal(t, maintainLimit, time.Since(start), "maintenance used its whole limit")
		require.NoError(t, job.deliverErr, "a slow maintenance does not leave the notices a dead context")
	})
}
