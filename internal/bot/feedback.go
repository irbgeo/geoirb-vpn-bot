package bot

import (
	"context"
	"errors"
	"strings"

	tgbot "github.com/irbgeo/go-tgbot"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

// feedbackPerHour: how many reviews one user may send in an hour. Each one
// is saved and sent to every admin, so without a limit any account could
// flood both.
const feedbackPerHour = 5

// askFeedback waits for one message with a review or suggestion; the
// "◀️ Меню" button cancels.
func (s *router) askFeedback(ctx context.Context, cq *tgbot.CallbackQuery) error {
	pendingInput := pendingInput{
		ChatID: cq.ChatID(),
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
// A message without text, a bad text and a save that failed keep the
// question open, so the user can just send it again; a saved review (or
// one over the hourly limit) ends it.
func (s *router) feedbackText(ctx context.Context, m *tgbot.Message) error {
	if strings.TrimSpace(m.Text) == "" { // a sticker or a photo
		outMessage := outMessage{
			ChatID:   m.Chat.ID,
			Text:     needFeedbackTextText,
			Keyboard: menuKeyboard(),
		}
		return s.send.Send(ctx, outMessage)
	}
	if !s.feedbackLimit.allow(m.From.ID) {
		s.dialogs.drop(m.Chat.ID)
		outMessage := outMessage{
			ChatID:   m.Chat.ID,
			Text:     feedbackLimitText,
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
