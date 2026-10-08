package worker

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
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
		return nil, errors.New("docker down")
	}
	return &service.Maintenance{}, nil
}

func (s *fakeJob) DeliverMaintenance(context.Context, *service.Maintenance) {
	s.delivered.Add(1)
}

func TestRunStartsAtOnceThenTicks(t *testing.T) {
	job := &fakeJob{}
	w := New(
		&Input{
			Job:      job,
			Delivery: job,
			Every:    10 * time.Millisecond,
		},
	)
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Millisecond)
	defer cancel()

	w.Run(ctx)

	require.GreaterOrEqual(t, job.runs.Load(), int32(3), "at start and on every tick")
	require.Equal(t, job.runs.Load(), job.delivered.Load())
}

func TestRunKeepsGoingAfterErrors(t *testing.T) {
	job := &fakeJob{
		fail: true,
	}
	w := New(
		&Input{
			Job:      job,
			Delivery: job,
			Every:    10 * time.Millisecond,
		},
	)
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
	defer cancel()

	w.Run(ctx)

	require.GreaterOrEqual(t, job.runs.Load(), int32(2))
	require.Zero(t, job.delivered.Load(), "nothing to deliver after a failed run")
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
	job := &slowJob{
		started: make(chan struct{}),
	}
	w := New(
		&Input{
			Job:      job,
			Delivery: job,
			Every:    time.Hour,
		},
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
		&Input{
			Job:      job,
			Delivery: job,
			Every:    time.Hour,
		},
	)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w.Run(ctx)

	require.True(t, job.hasDeadline.Load(), "a hung docker can't hold a run past the stop timeout")
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
	job := &limitJob{}
	w := New(
		&Input{
			Job:      job,
			Delivery: job,
			Every:    time.Hour,
		},
	)
	w.maintainLimit = 20 * time.Millisecond

	w.once(context.Background())
	require.NoError(t, job.deliverErr, "a slow maintenance does not leave the notices a dead context")
}
