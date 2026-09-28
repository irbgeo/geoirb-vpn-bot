package bot

import (
	"context"
	"testing"

	tgbot "github.com/irbgeo/go-tgbot"
	"github.com/stretchr/testify/require"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

func TestFeedbackButtonAsksThenSaves(t *testing.T) {
	svc := &fakeService{}
	r, s := newRouter(svc)
	ctx := context.Background()

	require.NoError(t, r.Handle(ctx, press(cbFeedback)))
	ask := s.sent[0]
	require.Contains(t, ask.Text, "одним сообщением")
	require.True(t, hasMenuButton(ask.Keyboard), "the menu button cancels")

	require.NoError(t, r.Handle(ctx, startUpdate("Добавьте тариф на неделю")))
	require.Equal(
		t,
		[]service.FeedbackInput{
			{
				UserID:   42,
				Username: "alice",
				Text:     "Добавьте тариф на неделю",
			},
		},
		svc.feedback,
	)
	require.Contains(t, s.sent[len(s.sent)-1].Text, "Спасибо")

	require.NoError(t, r.Handle(ctx, startUpdate("ещё одно")))
	require.Len(t, svc.feedback, 1, "one press, one message")
}

func TestFeedbackWithoutTextAsksAgain(t *testing.T) {
	svc := &fakeService{}
	r, s := newRouter(svc)
	ctx := context.Background()
	require.NoError(t, r.Handle(ctx, press(cbFeedback)))

	require.NoError(t, r.Handle(ctx, startUpdate(""))) // a sticker
	require.Empty(t, svc.feedback)
	require.Contains(t, s.sent[len(s.sent)-1].Text, "текст")

	require.NoError(t, r.Handle(ctx, startUpdate("ок")))
	require.Len(t, svc.feedback, 1, "still waiting after the sticker")
}

func TestFeedbackTooLongIsExplained(t *testing.T) {
	svc := &fakeService{
		feedbackErr: service.ErrBadFeedback,
	}
	r, s := newRouter(svc)
	ctx := context.Background()
	require.NoError(t, r.Handle(ctx, press(cbFeedback)))

	require.NoError(t, r.Handle(ctx, startUpdate("очень длинный текст")))
	require.Contains(t, s.sent[len(s.sent)-1].Text, "до 2000")
}

func TestFeedbackCancelledByMenu(t *testing.T) {
	svc := &fakeService{}
	r, _ := newRouter(svc)
	ctx := context.Background()
	require.NoError(t, r.Handle(ctx, press(cbFeedback)))
	require.NoError(t, r.Handle(ctx, press(cbMenu)))

	require.NoError(t, r.Handle(ctx, startUpdate("передумал")))
	require.Empty(t, svc.feedback)
}

func TestFeedbackIsInEveryonesMenu(t *testing.T) {
	r, s := newRouter(&fakeService{})
	require.NoError(t, r.Handle(context.Background(), startUpdate("/menu")))
	require.True(t, containsButton(s.sent[0].Keyboard, cbFeedback))
}

// containsButton reports whether the keyboard has a button with this data.
func containsButton(kb *tgbot.InlineKeyboardMarkup, data string) bool {
	for _, row := range kb.InlineKeyboard {
		for _, b := range row {
			if b.CallbackData == data {
				return true
			}
		}
	}
	return false
}
