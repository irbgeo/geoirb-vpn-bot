package store

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/irbgeo/geoirb-vpn-bot/internal/config"
	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

// store wraps a MongoDB connection and exposes repositories.
type store struct {
	client    *mongo.Client
	Users     *userRepo
	Peers     *peerRepo
	Payments  *paymentRepo
	Feedback  *feedbackRepo
	Companies *companyRepo
}

// Compile-time checks that the repos satisfy the service ports.
var (
	_ service.UserRepository     = (*userRepo)(nil)
	_ service.PeerRepository     = (*peerRepo)(nil)
	_ service.PaymentRepository  = (*paymentRepo)(nil)
	_ service.FeedbackRepository = (*feedbackRepo)(nil)
	_ service.CompanyRepository  = (*companyRepo)(nil)
)

// Connect dials MongoDB (MongoURI, database MongoDB), verifies the
// connection, creates indexes and builds the repositories. SecretKey
// (32 bytes) encrypts peer private keys and PSKs at rest.
func Connect(
	ctx context.Context,
	cfg *config.Config,
) (*store, error) {
	box, err := newSealer(cfg.SecretKey)
	if err != nil {
		return nil, err
	}
	clientOptions := options.Client().ApplyURI(cfg.MongoURI)
	client, err := mongo.Connect(ctx, clientOptions)
	if err != nil {
		return nil, fmt.Errorf("store: connect: %w", err)
	}
	err = client.Ping(ctx, nil)
	if err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	db := client.Database(cfg.MongoDB)
	usersColl := db.Collection("users")
	users := newUserRepo(usersColl)
	peersColl := db.Collection("peers")
	peers := newPeerRepo(
		peersColl,
		box,
	)
	paymentsColl := db.Collection("payments")
	payments := newPaymentRepo(paymentsColl)
	feedbackColl := db.Collection("feedback")
	feedbacks := newFeedbackRepo(feedbackColl)
	companiesColl := db.Collection("companies")
	companies := newCompanyRepo(companiesColl)
	s := &store{
		client:    client,
		Users:     users,
		Peers:     peers,
		Payments:  payments,
		Feedback:  feedbacks,
		Companies: companies,
	}
	err = s.ensureIndexes(ctx)
	if err != nil {
		_ = client.Disconnect(context.Background())
		return nil, err
	}
	err = s.Peers.checkKey(ctx)
	if err != nil {
		_ = client.Disconnect(context.Background())
		return nil, err
	}
	return s, nil
}

// Disconnect closes the MongoDB connection.
func (s *store) Disconnect(ctx context.Context) error {
	return s.client.Disconnect(ctx)
}

// ensureIndexes creates the indexes correctness depends on (one IP per
// server, one company per invite) and the lookup indexes of every key
// press and maintenance run: anyone can add user rows with /start, so a
// scan would grow with them.
func (s *store) ensureIndexes(ctx context.Context) error {
	for _, ix := range []indexSpec{
		{Coll: s.Peers.coll, Model: peerServerIPIndex()},
		{Coll: s.Peers.coll, Model: ascIndex("user_id")},
		{Coll: s.Users.coll, Model: ascIndex("role")},
		{Coll: s.Users.coll, Model: ascIndex("company_id")},
		{Coll: s.Payments.coll, Model: ascIndex("user_id")},
		{Coll: s.Payments.coll, Model: ascIndex("created_at")},
		{Coll: s.Companies.coll, Model: inviteIndex()},
	} {
		_, err := ix.Coll.Indexes().CreateOne(ctx, ix.Model)
		if err != nil {
			return fmt.Errorf("store: create index on %s: %w", ix.Coll.Name(), err)
		}
	}
	return nil
}
