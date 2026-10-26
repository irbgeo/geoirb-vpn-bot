package store

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/mongo"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

// companyRepo stores companies in MongoDB.
type companyRepo struct {
	coll *mongo.Collection
}

// Add stores a new company.
func (s *companyRepo) Add(ctx context.Context, c *service.Company) error {
	doc := companyToStore(c)
	_, err := s.coll.InsertOne(ctx, doc)
	if err != nil {
		return fmt.Errorf("store: add company %s: %w", c.ID, err)
	}
	return nil
}

// Get returns the company, or (nil, nil) when there is none.
func (s *companyRepo) Get(ctx context.Context, id string) (*service.Company, error) {
	var d company
	err := s.coll.FindOne(ctx, byID(id)).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: get company %s: %w", id, err)
	}
	c := d.toService()
	return c, nil
}

// ByInvite returns the company of an invite code, or (nil, nil).
func (s *companyRepo) ByInvite(ctx context.Context, invite string) (*service.Company, error) {
	var d company
	err := s.coll.FindOne(ctx, byInvite(invite)).Decode(&d)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: company by invite: %w", err)
	}
	c := d.toService()
	return c, nil
}

// Save replaces a stored company.
func (s *companyRepo) Save(ctx context.Context, c *service.Company) error {
	doc := companyToStore(c)
	_, err := s.coll.ReplaceOne(ctx, byID(c.ID), doc)
	if err != nil {
		return fmt.Errorf("store: save company %s: %w", c.ID, err)
	}
	return nil
}

// List returns every company, oldest first.
func (s *companyRepo) List(ctx context.Context) ([]*service.Company, error) {
	cur, err := s.coll.Find(ctx, matchAll(), oldestFirst())
	if err != nil {
		return nil, fmt.Errorf("store: list companies: %w", err)
	}
	var docs []company
	err = cur.All(ctx, &docs)
	if err != nil {
		return nil, fmt.Errorf("store: decode companies: %w", err)
	}
	out := make([]*service.Company, len(docs))
	for i := range docs {
		out[i] = docs[i].toService()
	}
	return out, nil
}

// newCompanyRepo builds a companyRepo on coll.
func newCompanyRepo(
	coll *mongo.Collection,
) *companyRepo {
	return &companyRepo{
		coll: coll,
	}
}
