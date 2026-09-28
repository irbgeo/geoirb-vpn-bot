package bot

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

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

func TestNewFeedbackIsSentToAdmins(t *testing.T) {
	svc := &fakeService{
		admins: []*service.User{
			{
				ID: 1,
			},
		},
	}
	r, s := newRouter(svc)
	ctx := context.Background()
	require.NoError(t, r.Handle(ctx, press(cbFeedback)))
	require.NoError(t, r.Handle(ctx, startUpdate("Добавьте тариф на неделю")))

	alerts := sentTo(s, 1)
	require.Len(t, alerts, 1)
	require.Contains(t, alerts[0].Text, "@alice")
	require.Contains(t, alerts[0].Text, "Добавьте тариф на неделю")
}

func TestAdminFeedbackList(t *testing.T) {
	svc := adminService()
	for i := range 12 {
		svc.feedbackList = append(
			svc.feedbackList,
			&service.Feedback{
				UserID:    int64(100 + i),
				Username:  fmt.Sprintf("u%d", i),
				Text:      strings.Repeat("я", 400),
				CreatedAt: time.Date(2026, 9, 28, 7, 0, 0, 0, time.UTC),
			},
		)
	}
	r, s := newRouter(svc)
	ctx := context.Background()

	require.NoError(t, r.Handle(ctx, press("a:fb:0")))
	page := s.edits[0]
	require.Contains(t, page.Text, "всего 12")
	require.Contains(t, page.Text, "@u0")
	require.Contains(t, page.Text, "28.09.2026 10:00 по Москве")
	require.LessOrEqual(t, len([]rune(page.Text)), 4096, "a page fits one Telegram message")
	require.Contains(t, page.Text, "…", "long feedback is cut in the list")
	require.True(t, containsButton(page.Keyboard, "a:fb:1"), "next page")
	require.True(t, hasMenuButton(page.Keyboard))

	require.NoError(t, r.Handle(ctx, press("a:fb:1")))
	require.Contains(t, s.edits[1].Text, "@u10")
	require.True(t, containsButton(s.edits[1].Keyboard, "a:fb:0"), "previous page")
}

func TestAdminMenuHasFeedback(t *testing.T) {
	r, s := newRouter(adminService())
	require.NoError(t, r.Handle(context.Background(), startUpdate("/menu")))
	require.True(t, containsButton(s.sent[0].Keyboard, "a:fb:0"))
}

func TestFeedbackListIsForAdminsOnly(t *testing.T) {
	r, s := newRouter(&fakeService{})
	require.NoError(t, r.Handle(context.Background(), press("a:fb:0")))
	require.Empty(t, s.edits)
	require.Empty(t, s.sent)
}
