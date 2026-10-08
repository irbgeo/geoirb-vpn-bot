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
func (s *router) askFeedback(ctx context.Context, cq *tgbot.CallbackQuery) error {
	pendingInput := pendingInput{
		ChatID: cq.ChatID(),
		UserID: cq.SenderID(),
		Kind:   pendingFeedback,
	}
	s.dialogs.set(pendingInput)
	outMessage := outMessage{
		ChatID:   cq.ChatID(),
		Text:     askFeedbackText,
		Keyboard: menuKeyboard(),
	}
	return s.send.Send(ctx, outMessage)
}

// feedbackText saves the text the user sent after "Отзывы и предложения".
// A message without text or a bad text asks again; anything else ends the
// question. Only the user who asked answers.
func (s *router) feedbackText(ctx context.Context, m *tgbot.Message) error {
	p, _ := s.dialogs.peek(m.Chat.ID)
	if p.UserID != m.From.ID {
		return nil
	}
	if strings.TrimSpace(m.Text) == "" { // a sticker or a photo
		outMessage := outMessage{
			ChatID:   m.Chat.ID,
			Text:     needFeedbackTextText,
			Keyboard: menuKeyboard(),
		}
		return s.send.Send(ctx, outMessage)
	}
	feedbackInput := service.FeedbackInput{
		UserID:   m.From.ID,
		Username: m.From.Username,
		Text:     m.Text,
	}
	err := s.feedback.AddFeedback(ctx, feedbackInput)
	switch {
	case errors.Is(err, service.ErrBadFeedback):
		outMessage := outMessage{
			ChatID:   m.Chat.ID,
			Text:     badFeedbackText,
			Keyboard: menuKeyboard(),
		}
		return s.send.Send(ctx, outMessage)
	case err != nil:
		userError := userError{
			ChatID: m.Chat.ID,
			Err:    err,
			Text:   feedbackFailedText,
		}
		return s.replyError(ctx, userError)
	}
	s.dialogs.drop(m.Chat.ID)
	feedback := service.Feedback{
		UserID:   m.From.ID,
		Username: m.From.Username,
		Text:     strings.TrimSpace(m.Text),
	}
	s.notify.NotifyAdmins(ctx, feedbackAlertText(&feedback))
	outMessage := outMessage{
		ChatID:   m.Chat.ID,
		Text:     feedbackThanksText,
		Keyboard: menuKeyboard(),
	}
	return s.send.Send(ctx, outMessage)
}
