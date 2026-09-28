package service

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"
)

// MaxFeedbackLen is the longest review or suggestion, in letters.
const MaxFeedbackLen = 2000

// ErrBadFeedback: the text is empty or longer than MaxFeedbackLen.
var ErrBadFeedback = errors.New("service: feedback is empty or too long")

// Feedbacks returns one page of reviews and suggestions, newest first,
// and the total count.
func (s *Service) Feedbacks(ctx context.Context, p Page) ([]*Feedback, int64, error) {
	return s.feedback.List(ctx, p)
}

// AddFeedback saves a user's review or suggestion, trimmed.
func (s *Service) AddFeedback(ctx context.Context, in FeedbackInput) error {
	text := strings.TrimSpace(in.Text)
	if text == "" || utf8.RuneCountInString(text) > MaxFeedbackLen {
		return ErrBadFeedback
	}
	return s.feedback.Add(
		ctx,
		&Feedback{
			UserID:    in.UserID,
			Username:  in.Username,
			Text:      text,
			CreatedAt: s.now(),
		},
	)
}
