package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"testing/synctest"
	"time"

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
	require.True(t, containsButton(
		buttonQuery{
			Keyboard: s.sent[0].Keyboard,
			Data:     cbFeedback,
		},
	))
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

	alerts := s.sentTo(1)
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
	require.True(t, containsButton(
		buttonQuery{
			Keyboard: page.Keyboard,
			Data:     "a:fb:1",
		},
	), "next page")
	require.True(t, hasMenuButton(page.Keyboard))

	require.NoError(t, r.Handle(ctx, press("a:fb:1")))
	require.Contains(t, s.edits[1].Text, "@u10")
	require.True(t, containsButton(
		buttonQuery{
			Keyboard: s.edits[1].Keyboard,
			Data:     "a:fb:0",
		},
	), "previous page")
}

func TestAdminMenuHasFeedback(t *testing.T) {
	r, s := newRouter(adminService())
	require.NoError(t, r.Handle(context.Background(), startUpdate("/menu")))
	require.True(t, containsButton(
		buttonQuery{
			Keyboard: s.sent[0].Keyboard,
			Data:     "a:fb:0",
		},
	))
}

func TestFeedbackListIsForAdminsOnly(t *testing.T) {
	r, s := newRouter(&fakeService{})
	require.NoError(t, r.Handle(context.Background(), press("a:fb:0")))
	require.Empty(t, s.edits)
	require.Empty(t, s.sent)
}

// containsButton reports whether q.Keyboard has a button with q.Data.
func containsButton(q buttonQuery) bool {
	for _, row := range q.Keyboard.InlineKeyboard {
		for _, b := range row {
			if b.CallbackData == q.Data {
				return true
			}
		}
	}
	return false
}

func TestFeedbackIsLimitedPerUser(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		svc := &fakeService{
			admins: []*service.User{
				{
					ID: 1,
				},
			},
		}
		r, s := newRouter(svc)
		ctx := context.Background()
		send := func(text string) {
			require.NoError(t, r.Handle(ctx, press(cbFeedback)))
			require.NoError(t, r.Handle(ctx, startUpdate(text)))
		}
		for range feedbackPerHour {
			send("отзыв")
		}

		send("ещё один")
		require.Len(t, svc.feedback, feedbackPerHour, "the one over the limit is not saved")
		require.Len(t, s.sentTo(1), feedbackPerHour, "and admins are not told")
		require.Equal(t, feedbackLimitText, s.sent[len(s.sent)-1].Text)
		_, waiting := r.dialogs.peek(42)
		require.False(t, waiting, "the question is closed")

		other := startUpdate("от другого")
		other.Message.From.ID, other.Message.Chat.ID = 43, 43
		r.dialogs.set(
			pendingInput{
				ChatID: 43,
				Kind:   pendingFeedback,
			},
		)
		require.NoError(t, r.Handle(ctx, other))
		require.Len(t, svc.feedback, feedbackPerHour+1, "the limit is per user")

		time.Sleep(time.Hour)
		send("через час")
		require.Len(t, svc.feedback, feedbackPerHour+2, "an hour later it works again")
	})
}

func TestRateLimitForgetsIdleKeys(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		l := newRateLimit[int](
			1,
			time.Minute,
		)
		for k := range rateLimitKeys {
			require.True(t, l.allow(k))
		}
		require.False(t, l.allow(0))

		time.Sleep(time.Minute)
		require.True(t, l.allow(0))
		require.Len(t, l.seen, 1, "the idle keys are gone")
	})
}

func TestFeedbackSaveErrorKeepsTheQuestionOpen(t *testing.T) {
	svc := &fakeService{
		feedbackErr: errors.New("mongo down"),
	}
	r, s := newRouter(svc)
	ctx := context.Background()
	require.NoError(t, r.Handle(ctx, press(cbFeedback)))

	require.ErrorContains(t, r.Handle(ctx, startUpdate("отзыв")), "mongo down", "for the log")
	require.Equal(t, feedbackFailedText, s.sent[len(s.sent)-1].Text)

	svc.feedbackErr = nil
	require.NoError(t, r.Handle(ctx, startUpdate("отзыв")))
	require.Len(t, svc.feedback, 1, "sent again without pressing the button: saved")
}

// Only saved reviews use up the hourly limit: a DB that is down for a few
// tries, or a text that is refused, does not lock the user out.
func TestFailedFeedbackSavesAreNotCounted(t *testing.T) {
	svc := &fakeService{
		feedbackErr: errors.New("mongo down"),
	}
	r, s := newRouter(svc)
	ctx := context.Background()
	require.NoError(t, r.Handle(ctx, press(cbFeedback)))
	for range feedbackPerHour {
		require.Error(t, r.Handle(ctx, startUpdate("отзыв")))
	}
	svc.feedbackErr = service.ErrBadFeedback
	require.NoError(t, r.Handle(ctx, startUpdate("отзыв")))

	svc.feedbackErr = nil
	for range feedbackPerHour {
		require.NoError(t, r.Handle(ctx, startUpdate("отзыв")))
		require.Equal(t, feedbackThanksText, s.sent[len(s.sent)-1].Text)
		require.NoError(t, r.Handle(ctx, press(cbFeedback)))
	}
	require.Len(t, svc.feedback, feedbackPerHour)

	require.NoError(t, r.Handle(ctx, startUpdate("ещё один")))
	require.Equal(t, feedbackLimitText, s.sent[len(s.sent)-1].Text, "the saved ones are counted")
}
