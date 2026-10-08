package bot

import (
	"context"
	"errors"
	"strings"

	tgbot "github.com/irbgeo/go-tgbot"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

// askFeedback waits for one message with a review or suggestion; the
// "◀️ Меню" button cancels.
func (s *Router) askFeedback(ctx context.Context, cq *tgbot.CallbackQuery) error {
	s.dialogs.set(
		pendingInput{
			ChatID: cq.ChatID(),
			UserID: cq.SenderID(),
			Kind:   pendingFeedback,
		},
	)
	return s.send.Send(
		ctx,
		OutMessage{
			ChatID:   cq.ChatID(),
			Text:     askFeedbackText,
			Keyboard: menuKeyboard(),
		},
	)
}

// feedbackText saves the text the user sent after "Отзывы и предложения".
// A message without text or a bad text asks again; anything else ends the
// question. Only the user who asked answers.
func (s *Router) feedbackText(ctx context.Context, m *tgbot.Message) error {
	if p, _ := s.dialogs.peek(m.Chat.ID); p.UserID != m.From.ID {
		return nil
	}
	if strings.TrimSpace(m.Text) == "" { // a sticker or a photo
		return s.send.Send(
			ctx,
			OutMessage{
				ChatID:   m.Chat.ID,
				Text:     needFeedbackTextText,
				Keyboard: menuKeyboard(),
			},
		)
	}
	err := s.feedback.AddFeedback(
		ctx,
		service.FeedbackInput{
			UserID:   m.From.ID,
			Username: m.From.Username,
			Text:     m.Text,
		},
	)
	switch {
	case errors.Is(err, service.ErrBadFeedback):
		return s.send.Send(
			ctx,
			OutMessage{
				ChatID:   m.Chat.ID,
				Text:     badFeedbackText,
				Keyboard: menuKeyboard(),
			},
		)
	case err != nil:
		return s.replyError(
			ctx,
			userError{
				ChatID: m.Chat.ID,
				Err:    err,
				Text:   feedbackFailedText,
			},
		)
	}
	s.dialogs.drop(m.Chat.ID)
	s.notify.NotifyAdmins(
		ctx,
		feedbackAlertText(
			&service.Feedback{
				UserID:   m.From.ID,
				Username: m.From.Username,
				Text:     strings.TrimSpace(m.Text),
			},
		),
	)
	return s.send.Send(
		ctx,
		OutMessage{
			ChatID:   m.Chat.ID,
			Text:     feedbackThanksText,
			Keyboard: menuKeyboard(),
		},
	)
}
