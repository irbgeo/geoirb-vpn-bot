package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// MaxUnlimitedKeys is how many keys a RoleUnlimited user may create.
const MaxUnlimitedKeys = 3

// MaxUserKeys is how many keys a RoleUser may have: the trial key plus
// bought ones, each with its own term.
const MaxUserKeys = 3

// MaxTrialsPerHour: free trials started per hour by all users together, so
// a mass of new accounts can't fill the subnet in one go.
const MaxTrialsPerHour = 30

// MaxKeyNameLen is the longest key name a user may give, in letters: it
// shows on buttons and in the Amnezia app.
const MaxKeyNameLen = 32

var (
	// ErrKeyLimit: the user already has User.KeyLimit keys.
	ErrKeyLimit = errors.New("service: key limit reached")
	// ErrHasKey: a plain user already has a key; more keys are bought.
	ErrHasKey = errors.New("service: user already has a key")
	// ErrTrialUsed: a plain user without a key already had the trial; they
	// need to pay.
	ErrTrialUsed = errors.New("service: trial already used")
	// ErrTrialBusy: MaxTrialsPerHour trials started within the last hour;
	// the user tries later (their trial is not used up).
	ErrTrialBusy = errors.New("service: too many new trials right now")
	// ErrBadKeyName: the name is too long or has line breaks / control
	// characters.
	ErrBadKeyName = errors.New("service: bad key name")
)

// Register creates the user on first /start (as RoleUser) or refreshes the
// username. The role is never changed here: it is set by hand in the DB.
func (s *service) Register(ctx context.Context, in RegisterInput) (*User, error) {
	user := &User{
		ID:        in.ID,
		Username:  in.Username,
		Role:      RoleUser,
		CreatedAt: time.Now(),
	}
	return s.users.Register(ctx, user)
}

// Users returns one page of users grouped by role (users, unlimited,
// admins), newest first inside a role, and the total count; q.Role keeps
// only one role.
func (s *service) Users(ctx context.Context, q UsersQuery) ([]*User, int64, error) {
	return s.users.List(ctx, q)
}

// Admins returns every user with RoleAdmin (they get service alerts).
func (s *service) Admins(ctx context.Context) ([]*User, error) {
	return s.users.ByRole(ctx, RoleAdmin)
}

// CreateKey creates the user's own key, by role:
//   - RoleUser: the first key starts the free trial (TrialDays), once;
//     more keys are bought (Invoice with BuyNewKey).
//   - RoleUnlimited: up to MaxUnlimitedKeys never-expiring keys; disabled
//     keys count too (they keep their slot and IP).
//   - RoleVIP: the same, up to User.KeyLimit (users.max_keys).
//   - RoleAdmin: never-expiring keys, no limit.
//   - RoleMember: up to the company's KeysPerMember keys, ending with it.
//
// Counting and issuing run under one lock, so two quick taps can't create
// an extra key.
func (s *service) CreateKey(ctx context.Context, in CreateKeyInput) (*Peer, error) {
	userID := in.UserID
	name, err := cleanKeyName(in.Name)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Under the lock, like the key count: a user read before it could be
	// stale (role, trial) by the time this call gets its turn.
	u, err := s.User(ctx, userID)
	if err != nil {
		return nil, err
	}
	have, err := s.peers.ByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if u.Role == RoleMember {
		memberKeyInput := memberKeyInput{
			User: u,
			Name: name,
			Have: have,
		}
		p, err := s.issueMemberKey(ctx, memberKeyInput)
		return p.public(), err
	}
	keyQuota := keyQuota{
		User: u,
		Keys: len(have),
	}
	err = canCreate(keyQuota)
	if err != nil {
		return nil, err
	}
	if u.Role == RoleUnlimited || u.Role == RoleVIP || u.Role == RoleAdmin {
		if name == "" {
			userKeys := userKeys{
				User: u,
				Keys: have,
			}
			name = numberedKeyName(userKeys)
		}
		issueInput := IssueInput{
			UserID: userID,
			Name:   name,
		}
		p, err := s.issue(ctx, issueInput)
		return p.public(), err
	}
	trialInput := trialInput{
		User: u,
		Name: name,
	}
	p, err := s.startTrial(ctx, trialInput)
	return p.public(), err
}

// CheckCreateKey says whether CreateKey would give the user a key now
// (nil) or which error it would return, without issuing anything: the bot
// asks before walking the user through installing an app.
func (s *service) CheckCreateKey(ctx context.Context, userID int64) error {
	u, err := s.User(ctx, userID)
	if err != nil {
		return err
	}
	have, err := s.peers.ByUser(ctx, userID)
	if err != nil {
		return err
	}
	keyQuota := keyQuota{
		User: u,
		Keys: len(have),
	}
	if u.Role == RoleMember && hasBlocked(have) {
		return ErrBlocked
	}
	if u.Role == RoleMember {
		_, err = s.checkMemberKey(ctx, keyQuota)
		return err
	}
	return canCreate(keyQuota)
}

// User returns the user, or ErrNotFound.
func (s *service) User(ctx context.Context, id int64) (*User, error) {
	u, err := s.users.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, ErrNotFound
	}
	return u, nil
}

// cleanKeyName trims the name and squeezes inner spaces; "" stays "" (the
// old naming). A name over MaxKeyNameLen letters or with control
// characters (line breaks, tabs) is ErrBadKeyName.
func cleanKeyName(name string) (string, error) {
	if strings.IndexFunc(name, func(r rune) bool { return r != ' ' && unicode.IsControl(r) }) >= 0 {
		return "", ErrBadKeyName
	}
	name = strings.Join(strings.Fields(name), " ")
	if utf8.RuneCountInString(name) > MaxKeyNameLen {
		return "", ErrBadKeyName
	}
	return name, nil
}

// canCreate holds CreateKey's rules: admin without a limit, unlimited and
// vip up to User.KeyLimit; a plain user one key, and the trial only once.
func canCreate(q keyQuota) error {
	if q.User.Role == RoleAdmin {
		return nil
	}
	if q.User.Role == RoleUnlimited || q.User.Role == RoleVIP {
		if q.Keys >= q.User.KeyLimit() {
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

// displayName is the username, or the Telegram ID when there is none.
func displayName(u *User) string {
	if u.Username != "" {
		return u.Username
	}
	return strconv.FormatInt(u.ID, 10)
}

// numberedKeyName is "tg:<name> #n" with the smallest n no key of the
// user has yet.
func numberedKeyName(in userKeys) string {
	base := "tg:" + displayName(in.User)
	names := make([]string, 0, len(in.Keys))
	for _, h := range in.Keys {
		names = append(names, h.Name)
	}
	keyNumberInput := keyNumberInput{
		Names: names,
		Base:  base,
	}
	n := nextKeyNumber(keyNumberInput)
	return fmt.Sprintf("%s #%d", base, n)
}

// nextKeyNumber returns the smallest n >= 1 such that "base #n" is not in names.
func nextKeyNumber(in keyNumberInput) int {
	used := make(map[string]bool, len(in.Names))
	for _, n := range in.Names {
		used[n] = true
	}
	n := 1
	for used[fmt.Sprintf("%s #%d", in.Base, n)] {
		n++
	}
	return n
}

// startTrial issues a plain user's first (and only) key for TrialDays and marks the
// trial as used. The caller holds s.mu and has checked canCreate.
func (s *service) startTrial(ctx context.Context, in trialInput) (*Peer, error) {
	recent := s.trials[:0]
	for _, t := range s.trials {
		if time.Since(t) < time.Hour {
			recent = append(recent, t)
		}
	}
	s.trials = recent
	if len(s.trials) >= MaxTrialsPerHour {
		return nil, ErrTrialBusy
	}
	u := in.User
	issueInput := IssueInput{
		UserID: u.ID,
		Name:   in.Name,
		Days:   s.cfg.TrialDays,
	}
	p, err := s.issue(ctx, issueInput)
	if err != nil {
		return nil, err
	}
	s.trials = append(s.trials, time.Now())
	// Best effort: the key exists, so ErrHasKey blocks a second trial, and
	// DeleteOwnKey marks it again before the key goes (closeTrial).
	s.markTrialUsed(ctx, u.ID)
	return p, nil
}
