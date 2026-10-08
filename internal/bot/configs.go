package bot

import "context"

// adminConfigsAsk shows how many users get the "update your config"
// notice and waits for "send" or "cancel".
func (s *router) adminConfigsAsk(ctx context.Context, a adminAction) error {
	ids, err := s.ops.BroadcastRecipients(ctx)
	if err != nil {
		return s.reportError(ctx, a.failed(err))
	}
	outMessage := outMessage{
		ChatID:   a.ChatID,
		Text:     configsAskText(len(ids)),
		Keyboard: configsKeyboard(),
	}
	return s.send.Send(ctx, outMessage)
}

// adminConfigs tells every user with an enabled key to get a fresh config
// from "My access", in the background like a broadcast (the same one-at-a-
// time slot, so a double press doesn't send everything twice).
func (s *router) adminConfigs(ctx context.Context, a adminAction) error {
	massSend := massSend{
		AdminChat: a.ChatID,
		Started:   configsStartedText,
		Deliver:   s.sendConfigsNotice,
		Report:    configsReportText,
	}
	_, err := s.startMassSend(ctx, massSend)
	return err
}

// sendConfigsNotice tells one user to get a fresh config themselves, with
// a button to "My access": nothing is sent unasked, and the user takes it
// when they are ready to re-add it in the app.
func (s *router) sendConfigsNotice(ctx context.Context, userID int64) error {
	outMessage := outMessage{
		ChatID:   userID,
		Text:     configsNoticeText,
		Keyboard: myAccessKeyboard(),
	}
	return s.send.Send(ctx, outMessage)
}
