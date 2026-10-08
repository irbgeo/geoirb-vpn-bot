package service

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/stretchr/testify/require"
)

func TestAddFeedbackSavesIt(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()

		require.NoError(
			t,
			e.svc.AddFeedback(
				context.Background(),
				FeedbackInput{
					UserID:   42,
					Username: "alice",
					Text:     "  Добавьте тариф на неделю  ",
				},
			),
		)
		require.Equal(
			t,
			[]Feedback{
				{
					UserID:    42,
					Username:  "alice",
					Text:      "Добавьте тариф на неделю",
					CreatedAt: now,
				},
			},
			e.feedback.saved,
		)
	})
}

func TestAddFeedbackRejectsEmptyAndTooLong(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		for _, text := range []string{
			"   ",
			strings.Repeat("я", MaxFeedbackLen+1),
		} {
			e := newEnv()
			err := e.svc.AddFeedback(
				context.Background(),
				FeedbackInput{
					UserID: 42,
					Text:   text,
				},
			)
			require.ErrorIs(t, err, ErrBadFeedback)
			require.Empty(t, e.feedback.saved)
		}
	})
}

func TestFeedbacksPage(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()
		for _, text := range []string{
			"первый",
			"второй",
		} {
			require.NoError(
				t,
				e.svc.AddFeedback(
					context.Background(),
					FeedbackInput{
						UserID: 42,
						Text:   text,
					},
				),
			)
		}

		list, total, err := e.svc.Feedbacks(
			context.Background(),
			Page{
				Limit: 10,
			},
		)
		require.NoError(t, err)
		require.Equal(t, int64(2), total)
		require.Equal(t, "второй", list[0].Text, "newest first")
	})
}

func TestAddFeedbackTakesTheLongestText(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := newEnv()

		require.NoError(
			t,
			e.svc.AddFeedback(
				context.Background(),
				FeedbackInput{
					UserID: 42,
					Text:   strings.Repeat("я", MaxFeedbackLen),
				},
			),
		)
		require.Len(t, e.feedback.saved, 1)
	})
}
