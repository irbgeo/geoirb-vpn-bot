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

// paymentRepo stores Stars payments in MongoDB. The document _id is the
// telegram_payment_charge_id, so a payment can be recorded only once.
type paymentRepo struct {
	coll *mongo.Collection
}

// Add records a new payment. It returns false (and no error) when this
// charge ID is already stored: Telegram may deliver the same update twice.
func (s *paymentRepo) Add(ctx context.Context, p *service.Payment) (bool, error) {
	_, err := s.coll.InsertOne(ctx, paymentToStore(p))
	if mongo.IsDuplicateKeyError(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("store: add payment: %w", err)
	}
	return true, nil
}

// Get returns the payment by charge ID, or (nil, nil) if not found.
func (s *paymentRepo) Get(ctx context.Context, chargeID string) (*service.Payment, error) {
	var d payment
	err := s.coll.FindOne(ctx, byID(chargeID)).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: get payment: %w", err)
	}
	return d.toService(), nil
}

// MarkApplied marks a payment applied to m.PeerKey, touching nothing else.
func (s *paymentRepo) MarkApplied(ctx context.Context, m service.PaymentMark) error {
	res, err := s.coll.UpdateOne(ctx, byID(m.ChargeID), markApplied(m.PeerKey))
	if err != nil {
		return fmt.Errorf("store: mark payment applied: %w", err)
	}
	if res.MatchedCount == 0 {
		return fmt.Errorf("store: mark payment applied: charge %s not found", m.ChargeID)
	}
	return nil
}

// MarkRefunded records the refund time, touching nothing else. A charge
// with no record (refused before it was saved) is not an error.
func (s *paymentRepo) MarkRefunded(ctx context.Context, m service.PaymentMark) error {
	_, err := s.coll.UpdateOne(ctx, byID(m.ChargeID), markRefunded(m.At))
	if err != nil {
		return fmt.Errorf("store: mark payment refunded: %w", err)
	}
	return nil
}

// ByUser returns the user's payments, newest first.
func (s *paymentRepo) ByUser(ctx context.Context, userID int64) ([]*service.Payment, error) {
	return s.find(ctx, byUserID(userID))
}

// Since returns payments made at or after t, newest first.
func (s *paymentRepo) Since(ctx context.Context, t time.Time) ([]*service.Payment, error) {
	return s.find(ctx, createdSince(t))
}

// newPaymentRepo builds a paymentRepo on coll.
func newPaymentRepo(
	coll *mongo.Collection,
) *paymentRepo {
	return &paymentRepo{
		coll: coll,
	}
}

func (s *paymentRepo) find(ctx context.Context, filter bson.M) ([]*service.Payment, error) {
	cur, err := s.coll.Find(ctx, filter, newestFirst())
	if err != nil {
		return nil, fmt.Errorf("store: find payments: %w", err)
	}
	var docs []payment
	err = cur.All(ctx, &docs)
	if err != nil {
		return nil, fmt.Errorf("store: decode payments: %w", err)
	}
	out := make([]*service.Payment, len(docs))
	for i := range docs {
		out[i] = docs[i].toService()
	}
	return out, nil
}
