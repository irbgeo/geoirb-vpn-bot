package store

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

// Store wraps a MongoDB connection and exposes repositories.
type Store struct {
	client   *mongo.Client
	db       *mongo.Database
	Users    *UserRepo
	Peers    *PeerRepo
	Payments *PaymentRepo
	Feedback *FeedbackRepo
}

// Compile-time checks that the repos satisfy the service ports.
var (
	_ service.UserRepository     = (*UserRepo)(nil)
	_ service.PeerRepository     = (*PeerRepo)(nil)
	_ service.PaymentRepository  = (*PaymentRepo)(nil)
	_ service.FeedbackRepository = (*FeedbackRepo)(nil)
)

// Connect dials MongoDB, verifies the connection, creates indexes and
// builds the repositories.
func Connect(
	ctx context.Context,
	in ConnectInput,
) (*Store, error) {
	box, err := newSealer(in.SecretKey)
	if err != nil {
		return nil, err
	}
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(in.URI))
	if err != nil {
		return nil, fmt.Errorf("store: connect: %w", err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	db := client.Database(in.DBName)
	s := &Store{
		client: client,
		db:     db,
		Users: &UserRepo{
			coll: db.Collection("users"),
		},
		Peers: &PeerRepo{
			coll: db.Collection("peers"),
			box:  box,
		},
		Payments: &PaymentRepo{
			coll: db.Collection("payments"),
		},
		Feedback: &FeedbackRepo{
			coll: db.Collection("feedback"),
		},
	}
	if err := s.ensureIndexes(ctx); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, err
	}
	if err := s.Peers.checkKey(ctx); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, err
	}
	return s, nil
}

// Disconnect closes the MongoDB connection.
func (s *Store) Disconnect(ctx context.Context) error {
	return s.client.Disconnect(ctx)
}

// ensureIndexes creates the indexes correctness depends on. Payments need
// none: _id is the charge ID, which already makes them unique.
// ponytail: no lookup indexes — at most 254 peers per server and few
// payments; add user_id / created_at indexes if listing gets slow.
func (s *Store) ensureIndexes(ctx context.Context) error {
	_, err := s.Peers.coll.Indexes().CreateOne(ctx, peerServerIPIndex())
	if err != nil {
		return fmt.Errorf("store: create peers index: %w", err)
	}
	return nil
}
