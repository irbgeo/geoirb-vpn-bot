package service

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
)

// MaxUnlimitedKeys is how many keys a RoleUnlimited user may create.
const MaxUnlimitedKeys = 3

var (
	// ErrKeyLimit: an unlimited user already has MaxUnlimitedKeys keys.
	ErrKeyLimit = errors.New("service: key limit reached")
	// ErrHasKey: a plain user already has their key.
	ErrHasKey = errors.New("service: user already has a key")
	// ErrTrialUsed: a plain user without a key already had the trial; they
	// need to pay.
	ErrTrialUsed = errors.New("service: trial already used")
)

// Register creates the user on first /start (as RoleUser) or refreshes the
// username. The role is never changed here: it is set by hand in the DB.
func (s *Service) Register(ctx context.Context, in RegisterInput) (*User, error) {
	return s.users.Register(
		ctx,
		&User{
			ID:        in.ID,
			Username:  in.Username,
			Role:      RoleUser,
			CreatedAt: s.now(),
		},
	)
}

// User returns the user, or ErrNotFound.
func (s *Service) User(ctx context.Context, id int64) (*User, error) {
	u, err := s.users.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, ErrNotFound
	}
	return u, nil
}

// Users returns one page of users, newest first, and the total count.
func (s *Service) Users(ctx context.Context, p Page) ([]*User, int64, error) {
	return s.users.List(ctx, p)
}

// Admins returns every user with RoleAdmin (they get service alerts).
func (s *Service) Admins(ctx context.Context) ([]*User, error) {
	return s.users.ByRole(ctx, RoleAdmin)
}

// CreateKey creates the user's own key, by role:
//   - RoleUser: the first key starts the free trial (TrialDays), once;
//     one key per user.
//   - RoleUnlimited: up to MaxUnlimitedKeys never-expiring keys; disabled
//     keys count too (they keep their slot and IP).
//   - RoleAdmin: never-expiring keys, no limit.
//
// Counting and issuing run under one lock, so two quick taps can't create
// an extra key.
func (s *Service) CreateKey(ctx context.Context, userID int64) (*Peer, error) {
	u, err := s.User(ctx, userID)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	have, err := s.peers.ByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if err := canCreate(
		keyQuota{
			User: u,
			Keys: len(have),
		},
	); err != nil {
		return nil, err
	}
	if u.Role == RoleUnlimited || u.Role == RoleAdmin {
		return s.issue(
			ctx,
			IssueInput{
				UserID: userID,
				Name:   fmt.Sprintf("tg:%s #%d", displayName(u), len(have)+1),
			},
		)
	}
	return s.startTrial(ctx, u)
}

// CheckCreateKey says whether CreateKey would give the user a key now
// (nil) or which error it would return, without issuing anything: the bot
// asks before walking the user through installing an app.
func (s *Service) CheckCreateKey(ctx context.Context, userID int64) error {
	u, err := s.User(ctx, userID)
	if err != nil {
		return err
	}
	have, err := s.peers.ByUser(ctx, userID)
	if err != nil {
		return err
	}
	return canCreate(
		keyQuota{
			User: u,
			Keys: len(have),
		},
	)
}

// canCreate holds CreateKey's rules: admin without a limit, unlimited up
// to MaxUnlimitedKeys; a plain user one key, and the trial only once.
func canCreate(q keyQuota) error {
	if q.User.Role == RoleAdmin {
		return nil
	}
	if q.User.Role == RoleUnlimited {
		if q.Keys >= MaxUnlimitedKeys {
			return ErrKeyLimit
		}
		return nil
	}
	if q.Keys > 0 {
		return ErrHasKey
	}
	if q.User.TrialUsed {
		return ErrTrialUsed
	}
	return nil
}

// startTrial issues a plain user's first (and only) key for TrialDays and marks the
// trial as used. The caller holds s.mu.
func (s *Service) startTrial(ctx context.Context, u *User) (*Peer, error) {
	if u.TrialUsed {
		return nil, ErrTrialUsed
	}
	p, err := s.issue(
		ctx,
		IssueInput{
			UserID: u.ID,
			Days:   s.cfg.TrialDays,
		},
	)
	if err != nil {
		return nil, err
	}
	if err := s.users.SetTrialUsed(ctx, u.ID); err != nil {
		// The key exists, so ErrHasKey still blocks a second trial key.
		log.Printf("service: mark trial used for %d: %v", u.ID, err)
	}
	return p, nil
}

// displayName is the username, or the Telegram ID when there is none.
func displayName(u *User) string {
	if u.Username != "" {
		return u.Username
	}
	return strconv.FormatInt(u.ID, 10)
}
