package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

// PaymentRepo stores Stars payments in MongoDB. The document _id is the
// telegram_payment_charge_id, so a payment can be recorded only once.
type PaymentRepo struct {
	coll *mongo.Collection
}

// Add records a new payment. It returns false (and no error) when this
// charge ID is already stored: Telegram may deliver the same update twice.
func (r *PaymentRepo) Add(ctx context.Context, p *service.Payment) (bool, error) {
	_, err := r.coll.InsertOne(ctx, paymentToStore(p))
	if mongo.IsDuplicateKeyError(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("store: add payment: %w", err)
	}
	return true, nil
}

// Get returns the payment by charge ID, or (nil, nil) if not found.
func (r *PaymentRepo) Get(ctx context.Context, chargeID string) (*service.Payment, error) {
	var d payment
	err := r.coll.FindOne(ctx, byID(chargeID)).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: get payment: %w", err)
	}
	return d.toService(), nil
}

// Save replaces an existing payment (to mark it applied or refunded).
func (r *PaymentRepo) Save(ctx context.Context, p *service.Payment) error {
	res, err := r.coll.ReplaceOne(ctx, byID(p.ChargeID), paymentToStore(p))
	if err != nil {
		return fmt.Errorf("store: save payment: %w", err)
	}
	if res.MatchedCount == 0 {
		return fmt.Errorf("store: save payment: charge %s not found", p.ChargeID)
	}
	return nil
}

// ByUser returns the user's payments, newest first.
func (r *PaymentRepo) ByUser(ctx context.Context, userID int64) ([]*service.Payment, error) {
	return r.find(ctx, byUserID(userID))
}

// Since returns payments made at or after t, newest first.
func (r *PaymentRepo) Since(ctx context.Context, t time.Time) ([]*service.Payment, error) {
	return r.find(ctx, createdSince(t))
}

func (r *PaymentRepo) find(ctx context.Context, filter bson.M) ([]*service.Payment, error) {
	cur, err := r.coll.Find(ctx, filter, newestFirst())
	if err != nil {
		return nil, fmt.Errorf("store: find payments: %w", err)
	}
	var docs []payment
	if err := cur.All(ctx, &docs); err != nil {
		return nil, fmt.Errorf("store: decode payments: %w", err)
	}
	out := make([]*service.Payment, len(docs))
	for i := range docs {
		out[i] = docs[i].toService()
	}
	return out, nil
}
