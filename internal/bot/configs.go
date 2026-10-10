package bot

import (
	"context"

	tgbot "github.com/irbgeo/go-tgbot"
)

// adminConfigsAsk shows how many users get the "update your config"
// notice and waits for "send" or "cancel", like a broadcast preview.
func (s *router) adminConfigsAsk(ctx context.Context, a adminAction) error {
	ids, err := s.ops.BroadcastRecipients(ctx)
	if err != nil {
		return s.reportError(ctx, a.failed(err))
	}
	pendingInput := pendingInput{
		ChatID: a.ChatID,
		Kind:   readyConfigs,
	}
	token := s.dialogs.preview(pendingInput)
	send := tgbot.Button("🔄 Отправить", cbAdminCfgOK+":"+token)
	outMessage := outMessage{
		ChatID:   a.ChatID,
		Text:     configsAskText(len(ids)),
		Keyboard: sendCancelKeyboard(send),
	}
	return s.send.Send(ctx, outMessage)
}

// adminConfigs tells every user with an enabled key to get a fresh config
// from "My access", in the background like a broadcast (the same one-at-a-
// time slot). Only the button under the newest question sends, and once
// (takePreview); when it can't start, the question stays for another press.
func (s *router) adminConfigs(ctx context.Context, a adminAction) error {
	dialogTake := dialogTake{
		ChatID: a.ChatID,
		Kind:   readyConfigs,
		Token:  a.Arg,
	}
	p, ok, err := s.takePreview(ctx, dialogTake)
	if !ok {
		return err
	}
	massSend := massSend{
		AdminChat: a.ChatID,
		Started:   configsStartedText,
		Deliver:   s.sendConfigsNotice,
		Report:    configsReportText,
	}
	started, err := s.startMassSend(ctx, massSend)
	if !started {
		s.dialogs.set(p)
	}
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
