// Package worker runs the periodic job: key expiry, reminders and the
// subnet check.
package worker

import (
	"context"
	"log"
	"time"
)

// maintainLimit and deliverLimit bound one pass. Each step gets its own
// limit, so slow docker can't leave the notices a dead context (their
// "sent" marks are already saved, so they would be lost). Together they
// stay under the service's TimeoutStopSec (60 s).
const (
	maintainLimit = 25 * time.Second
	deliverLimit  = 25 * time.Second
)

// worker runs Job every Every and hands the result to Delivery.
type worker struct {
	job           Job
	delivery      Delivery
	every         time.Duration
	maintainLimit time.Duration
	deliverLimit  time.Duration
}

// New creates a worker.
func New(
	job Job,
	delivery Delivery,
	every time.Duration,
) *worker {
	return &worker{
		job:           job,
		delivery:      delivery,
		every:         every,
		maintainLimit: maintainLimit,
		deliverLimit:  deliverLimit,
	}
}

// Run works once at start, then on every tick, until ctx is done. A failed
// run is logged and the next tick tries again. A run in progress is not
// cancelled with ctx: cut half-way it could take a key off the server but
// leave it enabled in the DB. Run returns once that run is finished.
func (s *worker) Run(ctx context.Context) {
	t := time.NewTicker(s.every)
	defer t.Stop()
	for {
		s.once(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// once runs one pass. It is not cancelled with ctx (see Run), but each
// step has a time limit, so a hung docker can't hold it past systemd's
// stop timeout.
func (s *worker) once(ctx context.Context) {
	base := context.WithoutCancel(ctx)
	mctx, cancel := context.WithTimeout(base, s.maintainLimit)
	m, err := s.job.Maintain(mctx)
	cancel()
	if err != nil {
		log.Printf("worker: %v", err)
		return
	}
	dctx, cancel := context.WithTimeout(base, s.deliverLimit)
	defer cancel()
	s.delivery.DeliverMaintenance(dctx, m)
}
