package store

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/mongo"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

// userRepo stores users in MongoDB.
type userRepo struct {
	coll *mongo.Collection
}

// Get returns the user by Telegram ID, or (nil, nil) if not found.
func (s *userRepo) Get(ctx context.Context, id int64) (*service.User, error) {
	var d user
	err := s.coll.FindOne(ctx, byID(id)).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: get user %d: %w", id, err)
	}
	return d.toService(), nil
}

// Register stores a new user as given, or only updates the username of an
// existing one: role, trial mark and any field set by hand are kept (an
// upsert, so a role set in between Get and Save can't be written over).
func (s *userRepo) Register(ctx context.Context, u *service.User) (*service.User, error) {
	var d user
	err := s.coll.FindOneAndUpdate(ctx, byID(u.ID), registerUpdate(userToStore(u)), upsertReturnAfter()).Decode(&d)
	if err != nil {
		return nil, fmt.Errorf("store: register user %d: %w", u.ID, err)
	}
	return d.toService(), nil
}

// AddKeys changes a user's keys count; a user without a row is left alone.
func (s *userRepo) AddKeys(ctx context.Context, d service.KeysDelta) error {
	_, err := s.coll.UpdateOne(ctx, byID(d.UserID), incKeysCount(d.Delta))
	if err != nil {
		return fmt.Errorf("store: keys count of %d: %w", d.UserID, err)
	}
	return nil
}

// SetKeyCounts sets every user's keys count from counts; users not in it
// get 0.
func (s *userRepo) SetKeyCounts(ctx context.Context, counts map[int64]int) error {
	ids := make([]int64, 0, len(counts))
	for id, n := range counts {
		ids = append(ids, id)
		_, err := s.coll.UpdateOne(ctx, byID(id), setKeysCount(n))
		if err != nil {
			return fmt.Errorf("store: set keys count of %d: %w", id, err)
		}
	}
	_, err := s.coll.UpdateMany(ctx, countedExcept(ids), setKeysCount(0))
	if err != nil {
		return fmt.Errorf("store: reset keys counts: %w", err)
	}
	return nil
}

// SetTrialUsed marks the free trial as used, touching nothing else.
func (s *userRepo) SetTrialUsed(ctx context.Context, id int64) error {
	_, err := s.coll.UpdateOne(ctx, byID(id), setTrialUsed())
	if err != nil {
		return fmt.Errorf("store: set trial used for %d: %w", id, err)
	}
	return nil
}

// ByRole returns every user with this role.
func (s *userRepo) ByRole(ctx context.Context, role service.Role) ([]*service.User, error) {
	cur, err := s.coll.Find(ctx, byRole(role))
	if err != nil {
		return nil, fmt.Errorf("store: users by role: %w", err)
	}
	var docs []user
	err = cur.All(ctx, &docs)
	if err != nil {
		return nil, fmt.Errorf("store: decode users: %w", err)
	}
	return usersToService(docs), nil
}

// List returns one page of users grouped by role (users, unlimited,
// admins), newest first inside a role, and the total count.
func (s *userRepo) List(ctx context.Context, p service.Page) ([]*service.User, int64, error) {
	total, err := s.coll.CountDocuments(ctx, matchAll())
	if err != nil {
		return nil, 0, fmt.Errorf("store: count users: %w", err)
	}
	cur, err := s.coll.Aggregate(ctx, usersByRolePage(p))
	if err != nil {
		return nil, 0, fmt.Errorf("store: list users: %w", err)
	}
	var docs []user
	err = cur.All(ctx, &docs)
	if err != nil {
		return nil, 0, fmt.Errorf("store: decode users: %w", err)
	}
	return usersToService(docs), total, nil
}

// newUserRepo builds a userRepo on coll.
func newUserRepo(
	coll *mongo.Collection,
) *userRepo {
	return &userRepo{
		coll: coll,
	}
}

func usersToService(docs []user) []*service.User {
	out := make([]*service.User, len(docs))
	for i := range docs {
		out[i] = docs[i].toService()
	}
	return out
}
