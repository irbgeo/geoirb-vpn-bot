package bot

import "context"

// adminConfigsAsk shows how many users get the "update your config"
// notice and waits for "send" or "cancel".
func (r *Router) adminConfigsAsk(ctx context.Context, a adminAction) error {
	ids, err := r.ops.BroadcastRecipients(ctx)
	if err != nil {
		return r.reportError(ctx, a.failed(err))
	}
	return r.send.Send(
		ctx,
		OutMessage{
			ChatID:   a.ChatID,
			Text:     configsAskText(len(ids)),
			Keyboard: configsKeyboard(),
		},
	)
}

// adminConfigs tells every user with an enabled key to get a fresh config
// from "My access", in the background like a broadcast (the same one-at-a-
// time slot, so a double press doesn't send everything twice).
func (r *Router) adminConfigs(ctx context.Context, a adminAction) error {
	_, err := r.startMassSend(
		ctx,
		massSend{
			AdminChat: a.ChatID,
			Started:   configsStartedText,
			Deliver:   r.sendConfigsNotice,
			Report:    configsReportText,
		},
	)
	return err
}

// sendConfigsNotice tells one user to get a fresh config themselves, with
// a button to "My access": nothing is sent unasked, and the user takes it
// when they are ready to re-add it in the app.
func (r *Router) sendConfigsNotice(ctx context.Context, userID int64) error {
	return r.send.Send(
		ctx,
		OutMessage{
			ChatID:   userID,
			Text:     configsNoticeText,
			Keyboard: myAccessKeyboard(),
		},
	)
}
