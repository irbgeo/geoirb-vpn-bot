package service

import (
	"context"
	"crypto/rand"
	"errors"
	"log"
	"strings"
	"time"
	"unicode/utf8"
)

// Company defaults; per company they can be changed in the DB.
const (
	CompanySeats         = 20 // employees; the leader takes no seat
	CompanyKeysPerMember = 2
	maxCompanyNameLen    = 64
	inviteCodeLen        = 8
	// inviteAlphabet: lowercase letters and digits without look-alikes
	// (l, o, 0, 1), safe in a t.me start parameter.
	inviteAlphabet = "abcdefghijkmnpqrstuvwxyz23456789"
)

var (
	// ErrNoCompany: no company has this ID (a wrong or old invite link).
	ErrNoCompany = errors.New("service: no such company")
	// ErrCompanyEnded: the company's paid date has passed.
	ErrCompanyEnded = errors.New("service: the company's paid term ended")
	// ErrCompanyFull: every seat of the company is taken.
	ErrCompanyFull = errors.New("service: the company has no free seats")
	// ErrInCompany: the user is already in (another) company.
	ErrInCompany = errors.New("service: the user is already in a company")
	// ErrCompanyRole: admins can't be company members.
	ErrCompanyRole = errors.New("service: admins can't be in a company")
	// ErrNotLeader: only the company's leader (or an admin) may do that.
	ErrNotLeader = errors.New("service: only the company's leader can do that")
	// ErrIsLeader: the leader can't be removed from their company.
	ErrIsLeader = errors.New("service: the leader can't be removed")
	// ErrCompanyPaid: a member does not buy; the company pays.
	ErrCompanyPaid = errors.New("service: the company pays for this user's access")
	// ErrBadCompany: an empty or too long name, or a term under a month.
	ErrBadCompany = errors.New("service: bad company name or term")
	// ErrCompanyKeys: the user has more keys than a seat holds
	// (KeysPerMember); they delete the extra ones first.
	ErrCompanyKeys = errors.New("service: more keys than a company seat holds")
)

// CreateCompany makes a user the leader of a new company paid for
// in.Months from now. The leader becomes a member, and their keys (even
// forever ones) end with the company. Admins and members can't lead one.
func (s *service) CreateCompany(ctx context.Context, in CreateCompanyInput) (*Company, error) {
	name := strings.Join(strings.Fields(in.Name), " ")
	if name == "" || utf8.RuneCountInString(name) > maxCompanyNameLen || in.Months < 1 {
		return nil, ErrBadCompany
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	u, err := s.User(ctx, in.LeaderID)
	if err != nil {
		return nil, err
	}
	err = canJoin(u)
	if err != nil {
		return nil, err
	}
	id, err := inviteCode()
	if err != nil {
		return nil, err
	}
	invite, err := inviteCode()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	c := &Company{
		ID:            id,
		Invite:        invite,
		Name:          name,
		LeaderID:      u.ID,
		PaidUntil:     now.AddDate(0, in.Months, 0),
		MaxMembers:    CompanySeats,
		KeysPerMember: CompanyKeysPerMember,
		CreatedAt:     now,
	}
	err = s.companies.Add(ctx, c)
	if err != nil {
		return nil, err
	}

	companyEntry := companyEntry{
		User:    u,
		Company: c,
	}
	err = s.enterCompany(ctx, companyEntry)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// JoinCompany puts a user into the company of an invite link: they become
// a member (no trial, no buying) and their keys end with the company. The
// same link again is fine; an admin, a member of another company, an
// ended or a full company is refused, and so is a user with an
// admin-blocked key (a company must not lift the block) or with more keys
// than a seat holds.
func (s *service) JoinCompany(ctx context.Context, in JoinCompanyInput) (*Company, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	c, err := s.companyByInvite(ctx, in.Invite)
	if err != nil {
		return nil, err
	}
	u, err := s.User(ctx, in.UserID)
	if err != nil {
		return nil, err
	}
	if u.CompanyID == c.ID {
		return c, nil
	}
	err = canJoin(u)
	if err != nil {
		return nil, err
	}
	if !c.PaidUntil.After(time.Now()) {
		return nil, ErrCompanyEnded
	}
	members, err := s.users.ByCompany(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	info := CompanyInfo{
		Company: c,
		Members: members,
	}
	if info.Employees() >= c.MaxMembers {
		return nil, ErrCompanyFull
	}
	keys, err := s.peers.ByUser(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	if hasBlocked(keys) {
		return nil, ErrBlocked
	}
	if len(keys) > c.KeysPerMember {
		return nil, ErrCompanyKeys
	}

	companyEntry := companyEntry{
		User:    u,
		Company: c,
	}
	err = s.enterCompany(ctx, companyEntry)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// ExtendCompany adds paid months: from the paid date, or from now if it
// has passed. Every member's key gets the new end date; keys that ended
// come back on (an admin block stays).
func (s *service) ExtendCompany(ctx context.Context, in ExtendCompanyInput) (*Company, error) {
	if in.Months < 1 {
		return nil, ErrBadCompany
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	c, err := s.company(ctx, in.CompanyID)
	if err != nil {
		return nil, err
	}
	from := c.PaidUntil
	now := time.Now()
	if from.Before(now) {
		from = now
	}
	c.PaidUntil = from.AddDate(0, in.Months, 0)
	c.Reminded3d, c.Reminded1d = false, false
	err = s.companies.Save(ctx, c)
	if err != nil {
		return nil, err
	}

	members, err := s.users.ByCompany(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	for _, u := range members {
		keysUntil := keysUntil{
			UserID: u.ID,
			Until:  c.PaidUntil,
		}
		err = s.redateKeys(ctx, keysUntil)
		if err != nil {
			return nil, err
		}
	}
	return c, nil
}

// RemoveMember takes an employee out of the company (the leader or an
// admin asks): their keys are deleted, the seat is free, and they are a
// plain user again, without a trial. Admin-blocked keys stay, so the
// block still stops them from buying new ones.
func (s *service) RemoveMember(ctx context.Context, in RemoveMemberInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	c, err := s.company(ctx, in.CompanyID)
	if err != nil {
		return err
	}
	companyManager := companyManager{
		ActorID: in.ActorID,
		Company: c,
	}
	err = s.canManage(ctx, companyManager)
	if err != nil {
		return err
	}
	if in.UserID == c.LeaderID {
		return ErrIsLeader
	}
	u, err := s.User(ctx, in.UserID)
	if err != nil {
		return err
	}
	if u.CompanyID != c.ID {
		return ErrNotFound
	}

	keys, err := s.peers.ByUser(ctx, u.ID)
	if err != nil {
		return err
	}
	for _, p := range keys {
		if p.Blocked {
			continue
		}
		err = s.deletePeer(ctx, p)
		if err != nil {
			return err
		}
	}
	membership := Membership{
		UserID: u.ID,
		Role:   RoleUser,
	}
	return s.users.SetMembership(ctx, membership)
}

// RotateInvite gives the company a new invite code (the leader or an
// admin asks): the old link stops working, people already in stay.
func (s *service) RotateInvite(ctx context.Context, in RotateInviteInput) (*Company, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	c, err := s.company(ctx, in.CompanyID)
	if err != nil {
		return nil, err
	}
	companyManager := companyManager{
		ActorID: in.ActorID,
		Company: c,
	}
	err = s.canManage(ctx, companyManager)
	if err != nil {
		return nil, err
	}
	invite, err := inviteCode()
	if err != nil {
		return nil, err
	}
	c.Invite = invite
	err = s.companies.Save(ctx, c)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// MyCompany is the leader's view of their company; anyone else gets
// ErrNotLeader.
func (s *service) MyCompany(ctx context.Context, userID int64) (*CompanyInfo, error) {
	u, err := s.User(ctx, userID)
	if err != nil {
		return nil, err
	}
	if u.CompanyID == "" {
		return nil, ErrNotLeader
	}
	info, err := s.Company(ctx, u.CompanyID)
	if err != nil {
		return nil, err
	}
	if info.Company.LeaderID != u.ID {
		return nil, ErrNotLeader
	}
	return info, nil
}

// Company returns a company and its members (the admin's card).
func (s *service) Company(ctx context.Context, id string) (*CompanyInfo, error) {
	c, err := s.company(ctx, id)
	if err != nil {
		return nil, err
	}
	members, err := s.users.ByCompany(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	info := &CompanyInfo{
		Company: c,
		Members: members,
	}
	return info, nil
}

// Companies lists every company (the admin's list).
func (s *service) Companies(ctx context.Context) ([]*Company, error) {
	return s.companies.List(ctx)
}

// company loads a company, or ErrNoCompany.
func (s *service) company(ctx context.Context, id string) (*Company, error) {
	if id == "" {
		return nil, ErrNoCompany
	}
	c, err := s.companies.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, ErrNoCompany
	}
	return c, nil
}

// companyByInvite loads the company of an invite code, or ErrNoCompany.
func (s *service) companyByInvite(ctx context.Context, invite string) (*Company, error) {
	if invite == "" {
		return nil, ErrNoCompany
	}
	c, err := s.companies.ByInvite(ctx, invite)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, ErrNoCompany
	}
	return c, nil
}

// canManage: only an admin or the company's leader may change who is in
// it or its link.
func (s *service) canManage(ctx context.Context, in companyManager) error {
	actor, err := s.User(ctx, in.ActorID)
	if err != nil {
		return err
	}
	if actor.Role != RoleAdmin && actor.ID != in.Company.LeaderID {
		return ErrNotLeader
	}
	return nil
}

// hasBlocked: one of the keys was disabled by an admin.
func hasBlocked(keys []*Peer) bool {
	for _, p := range keys {
		if p.Blocked {
			return true
		}
	}
	return false
}

// canJoin: admins stay admins, and a user is in one company at most.
func canJoin(u *User) error {
	if u.Role == RoleAdmin {
		return ErrCompanyRole
	}
	if u.CompanyID != "" {
		return ErrInCompany
	}
	return nil
}

// enterCompany makes the user a member of the company and dates their
// keys to its end. The caller holds s.mu.
func (s *service) enterCompany(ctx context.Context, e companyEntry) error {
	membership := Membership{
		UserID:    e.User.ID,
		Role:      RoleMember,
		CompanyID: e.Company.ID,
	}
	err := s.users.SetMembership(ctx, membership)
	if err != nil {
		return err
	}
	keysUntil := keysUntil{
		UserID: e.User.ID,
		Until:  e.Company.PaidUntil,
	}
	return s.redateKeys(ctx, keysUntil)
}

// redateKeys makes every key of the user end at in.Until; a key that had
// ended comes back on, an admin-blocked one stays off. A key that can't go
// back on (its IP is taken, its secrets unreadable) is logged and skipped;
// any other error stops. The caller holds s.mu.
func (s *service) redateKeys(ctx context.Context, in keysUntil) error {
	keys, err := s.peers.ByUser(ctx, in.UserID)
	if err != nil {
		return err
	}
	for _, p := range keys {
		p.ExpiresAt = in.Until
		p.resetReminders()
		if p.Blocked {
			err = s.peers.Save(ctx, p)
		} else {
			err = s.enableAndSave(ctx, p)
		}
		if err != nil && skipKind(err) != "" {
			log.Printf("service: company key %s: %v", p.IP, err)
			continue
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// issueMemberKey issues a member's key, ending with their company. The
// caller holds s.mu.
func (s *service) issueMemberKey(ctx context.Context, in memberKeyInput) (*Peer, error) {
	if hasBlocked(in.Have) {
		return nil, ErrBlocked
	}
	keyQuota := keyQuota{
		User: in.User,
		Keys: len(in.Have),
	}
	c, err := s.checkMemberKey(ctx, keyQuota)
	if err != nil {
		return nil, err
	}
	name := in.Name
	if name == "" && len(in.Have) > 0 {
		userKeys := userKeys{
			User: in.User,
			Keys: in.Have,
		}
		name = numberedKeyName(userKeys)
	}
	issueInput := IssueInput{
		UserID: in.User.ID,
		Name:   name,
		Until:  c.PaidUntil,
	}
	return s.issue(ctx, issueInput)
}

// checkMemberKey: the member's company is paid and they have a free key
// slot. It returns the company.
func (s *service) checkMemberKey(ctx context.Context, q keyQuota) (*Company, error) {
	c, err := s.company(ctx, q.User.CompanyID)
	if err != nil {
		return nil, err
	}
	if !c.PaidUntil.After(time.Now()) {
		return nil, ErrCompanyEnded
	}
	if q.Keys >= c.KeysPerMember {
		return nil, ErrKeyLimit
	}
	return c, nil
}

// inviteCode is a random company ID, also its invite code.
func inviteCode() (string, error) {
	b := make([]byte, inviteCodeLen)
	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}
	for i := range b {
		b[i] = inviteAlphabet[int(b[i])%len(inviteAlphabet)]
	}
	return string(b), nil
}
