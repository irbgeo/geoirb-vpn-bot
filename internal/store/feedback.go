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

// Add saves one review or suggestion.
func (r *FeedbackRepo) Add(ctx context.Context, f *service.Feedback) error {
	if _, err := r.coll.InsertOne(ctx, feedbackToStore(f)); err != nil {
		return fmt.Errorf("store: add feedback: %w", err)
	}
	return nil
}
