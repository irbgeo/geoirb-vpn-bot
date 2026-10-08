package store

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/mongo"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

// feedbackRepo stores users' reviews and suggestions in MongoDB.
type feedbackRepo struct {
	coll *mongo.Collection
}

// List returns one page, newest first, and the total count.
func (s *feedbackRepo) List(ctx context.Context, p service.Page) ([]*service.Feedback, int64, error) {
	total, err := s.coll.CountDocuments(ctx, matchAll())
	if err != nil {
		return nil, 0, fmt.Errorf("store: count feedback: %w", err)
	}
	cur, err := s.coll.Find(ctx, matchAll(), pageNewestFirst(p))
	if err != nil {
		return nil, 0, fmt.Errorf("store: list feedback: %w", err)
	}
	var docs []feedback
	err = cur.All(ctx, &docs)
	if err != nil {
		return nil, 0, fmt.Errorf("store: decode feedback: %w", err)
	}
	out := make([]*service.Feedback, len(docs))
	for i := range docs {
		out[i] = docs[i].toService()
	}
	return out, total, nil
}

// Add saves one review or suggestion.
func (s *feedbackRepo) Add(ctx context.Context, f *service.Feedback) error {
	_, err := s.coll.InsertOne(ctx, feedbackToStore(f))
	if err != nil {
		return fmt.Errorf("store: add feedback: %w", err)
	}
	return nil
}

// newFeedbackRepo builds a feedbackRepo on coll.
func newFeedbackRepo(
	coll *mongo.Collection,
) *feedbackRepo {
	return &feedbackRepo{
		coll: coll,
	}
}
