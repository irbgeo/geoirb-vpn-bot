package service

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAddFeedbackSavesIt(t *testing.T) {
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
}

func TestAddFeedbackRejectsEmptyAndTooLong(t *testing.T) {
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
}
