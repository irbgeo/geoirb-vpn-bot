package bot

import (
	"context"
	"fmt"
	"log"
	"time"

	tgbot "github.com/irbgeo/go-tgbot"
)

// adminBroadcastAsk waits for the broadcast text.
func (s *router) adminBroadcastAsk(ctx context.Context, a adminAction) error {
	pendingInput := pendingInput{
		ChatID: a.ChatID,
		Kind:   pendingBroadcast,
	}
	s.dialogs.set(pendingInput)
	outMessage := outMessage{
		ChatID:   a.ChatID,
		Text:     askBroadcastText,
		Keyboard: cancelKeyboard(),
	}
	return s.send.Send(ctx, outMessage)
}

// adminMaintenance is one toggle button: it previews "maintenance
// started", or "maintenance is over" while it is on. The state flips only
// when the admin presses "send" (the usual broadcast confirm).
func (s *router) adminMaintenance(ctx context.Context, a adminAction) error {
	p := pendingInput{
		ChatID: a.ChatID,
		Text:   maintenanceText,
		Maint:  maintStart,
	}
	if s.maint.on() {
		p.Text, p.Maint = maintenanceEndText, maintEnd
	}
	return s.adminBroadcastPreview(ctx, p)
}

// adminBroadcastPreview shows the text and how many users get it, and
// waits for "send" or "cancel". p is the admin chat, the text and what
// sending does to the maintenance state.
func (s *router) adminBroadcastPreview(ctx context.Context, p pendingInput) error {
	ids, err := s.ops.BroadcastRecipients(ctx)
	if err != nil {
		errorReport := errorReport{
			ChatID: p.ChatID,
			Err:    err,
		}
		return s.reportError(ctx, errorReport)
	}
	p.Kind = readyBroadcast
	token := s.dialogs.preview(p)
	preview := broadcastView{
		Recipients: len(ids),
		Text:       p.Text,
	}
	outMessage := outMessage{
		ChatID:   p.ChatID,
		Text:     broadcastPreviewText(preview),
		Keyboard: sendCancelKeyboard(tgbot.Button("📣 Отправить", cbAdminBcOK+":"+token)),
	}
	return s.send.Send(ctx, outMessage)
}

// adminBroadcast sends the confirmed preview in the background, so the
// admin's chat is not blocked for the minute a big broadcast takes. Only
// the button under the newest preview sends it (takePreview); a
// maintenance change someone already made is not sent. When it can't start
// (another mass send runs, recipients fail) the preview stays, so "send"
// can be pressed again.
func (s *router) adminBroadcast(ctx context.Context, a adminAction) error {
	dialogTake := dialogTake{
		ChatID: a.ChatID,
		Kind:   readyBroadcast,
		Token:  a.Arg,
	}
	p, ok, err := s.takePreview(ctx, dialogTake)
	if !ok {
		return err
	}
	if p.Maint != maintKeep && s.maint.on() == (p.Maint == maintStart) {
		outMessage := outMessage{
			ChatID: a.ChatID,
			Text:   maintAlreadyText(p.Maint == maintStart),
		}
		return s.send.Send(ctx, outMessage)
	}
	massSend := massSend{
		AdminChat: a.ChatID,
		Started:   broadcastStartedText,
		Before: func() error {
			if p.Maint == maintKeep {
				return nil
			}
			// flip before sending: the admin's next /menu shows the new button
			err := s.maint.set(p.Maint == maintStart)
			if err != nil {
				return fmt.Errorf("bot: maintenance flag: %w", err)
			}
			return nil
		},
		Deliver: func(ctx context.Context, id int64) error {
			outMessage := outMessage{
				ChatID: id,
				Text:   p.Text,
			}
			return s.send.Send(ctx, outMessage)
		},
		Report: broadcastReportText,
	}
	started, err := s.startMassSend(ctx, massSend)
	if !started {
		s.dialogs.set(p) // keep the preview: "send" works again later
	}
	return err
}

// takePreview takes the preview the pressed "send" button belongs to. ok
// is false when there is nothing to send: a second press finds nothing
// (silent), a preview older than pendingTTL or a button under an older
// preview is explained to the admin.
func (s *router) takePreview(ctx context.Context, in dialogTake) (p pendingInput, ok bool, err error) {
	p, res := s.dialogs.take(in)
	outMessage := outMessage{
		ChatID: in.ChatID,
	}
	switch res {
	case takeOK:
		return p, true, nil
	case takeExpired:
		outMessage.Text = previewExpiredText
	case takeStale:
		outMessage.Text = oldPreviewText
	default:
		return p, false, nil
	}
	return p, false, s.send.Send(ctx, outMessage)
}

// startMassSend takes the one background slot, fetches the recipients,
// runs m.Before and starts m.Deliver for each recipient. started is false
// when it did not start: another mass send is running (the admin is told)
// or a step failed (the error is reported in the admin chat and returned).
func (s *router) startMassSend(ctx context.Context, m massSend) (started bool, err error) {
	if !s.jobs.reserve() {
		outMessage := outMessage{
			ChatID: m.AdminChat,
			Text:   massSendBusyText,
		}
		return false, s.send.Send(ctx, outMessage)
	}
	ids, err := s.ops.BroadcastRecipients(ctx)
	if err == nil && m.Before != nil {
		err = m.Before()
	}
	if err != nil {
		s.jobs.release()
		errorReport := errorReport{
			ChatID: m.AdminChat,
			Err:    err,
		}
		return false, s.reportError(ctx, errorReport)
	}
	outMessage := outMessage{
		ChatID: m.AdminChat,
		Text:   m.Started,
	}
	err = s.send.Send(ctx, outMessage)
	broadcastJob := broadcastJob{
		AdminChat:  m.AdminChat,
		Recipients: ids,
		Deliver:    m.Deliver,
		Report:     m.Report,
	}
	s.jobs.run(func(life context.Context) {
		s.runBroadcast(life, broadcastJob)
	})
	return true, err
}

// runBroadcast runs job.Deliver for each recipient with a pause, then
// reports how many got it. On shutdown (ctx done) it stops and reports
// what went out and how many users were not tried.
func (s *router) runBroadcast(ctx context.Context, job broadcastJob) {
	res := broadcastResult{}
send:
	for i, id := range job.Recipients {
		if i > 0 {
			select {
			case <-ctx.Done(): // shutting down: stop and report what went out
				break send
			case <-time.After(s.pause):
			}
		}
		err := job.Deliver(ctx, id)
		if err != nil {
			log.Printf("bot: broadcast to %d: %v", id, err)
			res.Failed++
		} else {
			res.Sent++
		}
	}
	res.Skipped = len(job.Recipients) - res.Sent - res.Failed
	outMessage := outMessage{
		ChatID: job.AdminChat,
		Text:   job.Report(res),
	}
	err := s.send.Send(context.WithoutCancel(ctx), outMessage)
	if err != nil {
		log.Printf("bot: broadcast report: %v", err)
	}
}
