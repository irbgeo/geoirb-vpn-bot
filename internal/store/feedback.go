package store

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/mongo"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

// FeedbackRepo stores users' reviews and suggestions in MongoDB.
type FeedbackRepo struct {
	coll *mongo.Collection
}

// List returns one page, newest first, and the total count.
func (s *FeedbackRepo) List(ctx context.Context, p service.Page) ([]*service.Feedback, int64, error) {
	total, err := s.coll.CountDocuments(ctx, matchAll())
	if err != nil {
		return nil, 0, fmt.Errorf("store: count feedback: %w", err)
	}
	cur, err := s.coll.Find(ctx, matchAll(), pageNewestFirst(p))
	if err != nil {
		return nil, 0, fmt.Errorf("store: list feedback: %w", err)
	}
	var docs []feedback
	if err := cur.All(ctx, &docs); err != nil {
		return nil, 0, fmt.Errorf("store: decode feedback: %w", err)
	}
	out := make([]*service.Feedback, len(docs))
	for i := range docs {
		out[i] = docs[i].toService()
	}
	return out, total, nil
}

// Add saves one review or suggestion.
func (s *FeedbackRepo) Add(ctx context.Context, f *service.Feedback) error {
	if _, err := s.coll.InsertOne(ctx, feedbackToStore(f)); err != nil {
		return fmt.Errorf("store: add feedback: %w", err)
	}
	return nil
}
