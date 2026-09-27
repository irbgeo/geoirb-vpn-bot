package bot

import (
	"context"
	"log"
	"time"
)

// adminBroadcastAsk waits for the broadcast text.
func (r *Router) adminBroadcastAsk(ctx context.Context, a adminAction) error {
	r.setPending(
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

// setPending records what an admin's next text message will be.
func (r *Router) setPending(p pendingInput) {
	if p.At.IsZero() {
		p.At = time.Now()
	}
	r.mu.Lock()
	r.pending[p.ChatID] = p
	r.mu.Unlock()
}

// dropPending forgets what the bot waited for from this chat.
func (r *Router) dropPending(chatID int64) {
	r.mu.Lock()
	delete(r.pending, chatID)
	r.mu.Unlock()
}

// adminMaintenance previews a ready broadcast: maintenance started or
// over. Sending it is the usual broadcast confirm.
func (r *Router) adminMaintenance(ctx context.Context, a adminAction) error {
	text := maintenanceText
	if a.Name == actMaintEnd {
		text = maintenanceEndText
	}
	return r.adminBroadcastPreview(
		ctx,
		OutMessage{
			ChatID: a.ChatID,
			Text:   text,
		},
	)
}

// adminBroadcastPreview shows the text and how many users get it, and
// waits for "send" or "cancel". m is the admin chat and the text.
func (r *Router) adminBroadcastPreview(ctx context.Context, m OutMessage) error {
	ids, err := r.svc.BroadcastRecipients(ctx)
	if err != nil {
		return err
	}
	r.setPending(
		pendingInput{
			ChatID: m.ChatID,
			Kind:   readyBroadcast,
			Text:   m.Text,
		},
	)
	preview := broadcastView{
		Recipients: len(ids),
		Text:       m.Text,
	}
	return r.send.Send(
		ctx,
		OutMessage{
			ChatID:   m.ChatID,
			Text:     broadcastPreviewText(preview),
			Keyboard: broadcastKeyboard(),
		},
	)
}

// adminBroadcast starts sending the confirmed text in the background, so
// the admin's chat is not blocked for the minute a big broadcast takes.
// A second press finds nothing pending and sends nothing.
func (r *Router) adminBroadcast(ctx context.Context, a adminAction) error {
	r.mu.Lock()
	p, ok := r.pending[a.ChatID]
	if ok && p.Kind == readyBroadcast {
		delete(r.pending, a.ChatID)
	}
	r.mu.Unlock()
	if !ok || p.Kind != readyBroadcast {
		return nil
	}
	ids, err := r.svc.BroadcastRecipients(ctx)
	if err != nil {
		return r.reportError(ctx, a.failed(err))
	}
	err = r.send.Send(
		ctx,
		OutMessage{
			ChatID: a.ChatID,
			Text:   broadcastStartedText,
		},
	)
	r.jobs.Go(func() {
		r.runBroadcast(
			r.life,
			broadcastJob{
				AdminChat:  a.ChatID,
				Recipients: ids,
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
	})
	return err
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
