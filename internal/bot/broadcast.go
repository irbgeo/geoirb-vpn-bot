package bot

import (
	"context"
	"fmt"
	"log"
	"time"
)

// adminBroadcastAsk waits for the broadcast text.
func (r *Router) adminBroadcastAsk(ctx context.Context, a adminAction) error {
	r.dialogs.set(
		pendingInput{
			ChatID: a.ChatID,
			Kind:   pendingBroadcast,
		},
	)
	return r.send.Send(
		ctx,
		OutMessage{
			ChatID:   a.ChatID,
			Text:     askBroadcastText,
			Keyboard: cancelKeyboard(),
		},
	)
}

// adminMaintenance is one toggle button: it previews "maintenance
// started", or "maintenance is over" while it is on. The state flips only
// when the admin presses "send" (the usual broadcast confirm).
func (r *Router) adminMaintenance(ctx context.Context, a adminAction) error {
	p := pendingInput{
		ChatID: a.ChatID,
		Text:   maintenanceText,
		Maint:  maintStart,
	}
	if r.maint.on() {
		p.Text, p.Maint = maintenanceEndText, maintEnd
	}
	return r.adminBroadcastPreview(ctx, p)
}

// adminBroadcastPreview shows the text and how many users get it, and
// waits for "send" or "cancel". p is the admin chat, the text and what
// sending does to the maintenance state.
func (r *Router) adminBroadcastPreview(ctx context.Context, p pendingInput) error {
	ids, err := r.ops.BroadcastRecipients(ctx)
	if err != nil {
		return r.reportError(
			ctx,
			errorReport{
				ChatID: p.ChatID,
				Err:    err,
			},
		)
	}
	p.Kind = readyBroadcast
	p.At = time.Time{} // a fresh preview gets a fresh pendingTTL
	r.dialogs.set(p)
	preview := broadcastView{
		Recipients: len(ids),
		Text:       p.Text,
	}
	return r.send.Send(
		ctx,
		OutMessage{
			ChatID:   p.ChatID,
			Text:     broadcastPreviewText(preview),
			Keyboard: broadcastKeyboard(),
		},
	)
}

// adminBroadcast sends the confirmed preview in the background, so the
// admin's chat is not blocked for the minute a big broadcast takes. A
// second press finds nothing pending and sends nothing; an old preview
// (pendingTTL) or a maintenance change someone already made is not sent.
// When it can't start (another mass send runs, recipients fail) the
// preview stays, so "send" can be pressed again.
func (r *Router) adminBroadcast(ctx context.Context, a adminAction) error {
	p, res := r.dialogs.take(
		dialogTake{
			ChatID: a.ChatID,
			Kind:   readyBroadcast,
		},
	)
	switch res {
	case takeNone:
		return nil
	case takeExpired:
		return r.send.Send(
			ctx,
			OutMessage{
				ChatID: a.ChatID,
				Text:   previewExpiredText,
			},
		)
	}
	if p.Maint != maintKeep && r.maint.on() == (p.Maint == maintStart) {
		return r.send.Send(
			ctx,
			OutMessage{
				ChatID: a.ChatID,
				Text:   maintAlreadyText(p.Maint == maintStart),
			},
		)
	}
	started, err := r.startMassSend(
		ctx,
		massSend{
			AdminChat: a.ChatID,
			Started:   broadcastStartedText,
			Before: func() error {
				if p.Maint == maintKeep {
					return nil
				}
				// flip before sending: the admin's next /menu shows the new button
				if err := r.maint.set(p.Maint == maintStart); err != nil {
					return fmt.Errorf("bot: maintenance flag: %w", err)
				}
				return nil
			},
			Deliver: func(ctx context.Context, id int64) error {
				return r.send.Send(
					ctx,
					OutMessage{
						ChatID: id,
						Text:   p.Text,
					},
				)
			},
			Report: broadcastReportText,
		},
	)
	if !started {
		r.dialogs.set(p) // keep the preview: "send" works again later
	}
	return err
}

// startMassSend takes the one background slot, fetches the recipients,
// runs m.Before and starts m.Deliver for each recipient. started is false
// when it did not start: another mass send is running (the admin is told)
// or a step failed (the error is reported in the admin chat and returned).
func (r *Router) startMassSend(ctx context.Context, m massSend) (started bool, err error) {
	if !r.jobs.reserve() {
		return false, r.send.Send(
			ctx,
			OutMessage{
				ChatID: m.AdminChat,
				Text:   massSendBusyText,
			},
		)
	}
	ids, err := r.ops.BroadcastRecipients(ctx)
	if err == nil && m.Before != nil {
		err = m.Before()
	}
	if err != nil {
		r.jobs.release()
		return false, r.reportError(
			ctx,
			errorReport{
				ChatID: m.AdminChat,
				Err:    err,
			},
		)
	}
	err = r.send.Send(
		ctx,
		OutMessage{
			ChatID: m.AdminChat,
			Text:   m.Started,
		},
	)
	r.jobs.run(func(life context.Context) {
		r.runBroadcast(
			life,
			broadcastJob{
				AdminChat:  m.AdminChat,
				Recipients: ids,
				Deliver:    m.Deliver,
				Report:     m.Report,
			},
		)
	})
	return true, err
}

// runBroadcast runs job.Deliver for each recipient with a pause, then
// reports how many got it. On shutdown (ctx done) it stops and reports
// what went out.
func (r *Router) runBroadcast(ctx context.Context, job broadcastJob) {
	res := broadcastResult{}
send:
	for i, id := range job.Recipients {
		if i > 0 {
			select {
			case <-ctx.Done(): // shutting down: stop and report what went out
				break send
			case <-time.After(r.pause):
			}
		}
		if err := job.Deliver(ctx, id); err != nil {
			log.Printf("bot: broadcast to %d: %v", id, err)
			res.Failed++
		} else {
			res.Sent++
		}
	}
	err := r.send.Send(
		context.WithoutCancel(ctx),
		OutMessage{
			ChatID: job.AdminChat,
			Text:   job.Report(res),
		},
	)
	if err != nil {
		log.Printf("bot: broadcast report: %v", err)
	}
}
