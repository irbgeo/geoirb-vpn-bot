// Package worker runs the periodic job: key expiry, reminders and the
// subnet check.
package worker

import (
	"context"
	"log"
	"time"
)

// runLimit bounds one pass: under the service's TimeoutStopSec (60 s).
const runLimit = 45 * time.Second

// Worker runs Job every Every and hands the result to Delivery.
type Worker struct {
	job      Job
	delivery Delivery
	every    time.Duration
}

// New creates a Worker.
func New(
	in *Input,
) *Worker {
	return &Worker{
		job:      in.Job,
		delivery: in.Delivery,
		every:    in.Every,
	}
}

// Run works once at start, then on every tick, until ctx is done. A failed
// run is logged and the next tick tries again. A run in progress is not
// cancelled with ctx: cut half-way it could take a key off the server but
// leave it enabled in the DB. Run returns once that run is finished.
func (w *Worker) Run(ctx context.Context) {
	t := time.NewTicker(w.every)
	defer t.Stop()
	for {
		w.once(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// once runs one pass. It is not cancelled with ctx (see Run), but it has a
// time limit, so a hung docker can't hold it past systemd's stop timeout.
func (w *Worker) once(ctx context.Context) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), runLimit)
	defer cancel()
	m, err := w.job.Maintain(ctx)
	if err != nil {
		log.Printf("worker: %v", err)
		return
	}
	w.delivery.DeliverMaintenance(ctx, m)
}
