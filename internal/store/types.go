package store

import (
	"time"

	"go.mongodb.org/mongo-driver/mongo"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

// This file is the storage boundary: MongoDB document types (with bson
// tags) and converters to/from the tag-free types in internal/service.

// sealInput is a value to seal or open, bound to one record by AAD
// (associated data: authenticated, not encrypted).
type sealInput struct {
	Text string
	AAD  string
}

type user struct {
	ID        int64     `bson:"_id"`
	Username  string    `bson:"username"`
	Role      string    `bson:"role"`
	TrialUsed bool      `bson:"trial_used"`
	KeysCount int       `bson:"keys_count"`
	MaxKeys   int       `bson:"max_keys,omitempty"` // set by hand for vip users
	CompanyID string    `bson:"company_id,omitempty"`
	CreatedAt time.Time `bson:"created_at"`
}

// indexSpec is one index of one collection (ensureIndexes).
type indexSpec struct {
	Coll  *mongo.Collection
	Model mongo.IndexModel
}

type company struct {
	ID            string    `bson:"_id"`
	Invite        string    `bson:"invite,omitempty"`
	Name          string    `bson:"name"`
	LeaderID      int64     `bson:"leader_id"`
	PaidUntil     time.Time `bson:"paid_until"`
	MaxMembers    int       `bson:"max_members"`
	KeysPerMember int       `bson:"keys_per_member"`
	Reminded3d    bool      `bson:"reminded_3d"`
	Reminded1d    bool      `bson:"reminded_1d"`
	CreatedAt     time.Time `bson:"created_at"`
}

func companyToStore(c *service.Company) *company {
	return &company{
		ID:            c.ID,
		Invite:        c.Invite,
		Name:          c.Name,
		LeaderID:      c.LeaderID,
		PaidUntil:     c.PaidUntil,
		MaxMembers:    c.MaxMembers,
		KeysPerMember: c.KeysPerMember,
		Reminded3d:    c.Reminded3d,
		Reminded1d:    c.Reminded1d,
		CreatedAt:     c.CreatedAt,
	}
}

// toService: a company stored before invites were apart from IDs has none;
// its ID was its link, so that stays its invite.
func (s *company) toService() *service.Company {
	invite := s.Invite
	if invite == "" {
		invite = s.ID
	}
	return &service.Company{
		ID:            s.ID,
		Invite:        invite,
		Name:          s.Name,
		LeaderID:      s.LeaderID,
		PaidUntil:     s.PaidUntil.UTC(),
		MaxMembers:    s.MaxMembers,
		KeysPerMember: s.KeysPerMember,
		Reminded3d:    s.Reminded3d,
		Reminded1d:    s.Reminded1d,
		CreatedAt:     s.CreatedAt.UTC(),
	}
}

func userToStore(u *service.User) *user {
	return &user{
		ID:        u.ID,
		Username:  u.Username,
		Role:      string(u.Role),
		TrialUsed: u.TrialUsed,
		KeysCount: u.KeysCount,
		CreatedAt: u.CreatedAt,
	}
}

func (s *user) toService() *service.User {
	return &service.User{
		ID:        s.ID,
		Username:  s.Username,
		Role:      roleOrUser(s.Role),
		TrialUsed: s.TrialUsed,
		KeysCount: s.KeysCount,
		MaxKeys:   s.MaxKeys,
		CompanyID: s.CompanyID,
		CreatedAt: s.CreatedAt,
	}
}

type peer struct {
	PublicKey  string     `bson:"_id"`
	ServerID   string     `bson:"server_id"`
	UserID     int64      `bson:"user_id"`
	Name       string     `bson:"name"`
	IP         string     `bson:"ip"`
	PrivateKey string     `bson:"private_key"`
	PSK        string     `bson:"psk"`
	Enabled    bool       `bson:"enabled"`
	ExpiresAt  *time.Time `bson:"expires_at"` // null = never expires
	Reminded3d bool       `bson:"reminded_3d"`
	Reminded1d bool       `bson:"reminded_1d"`
	Blocked    bool       `bson:"blocked"`
	CreatedAt  time.Time  `bson:"created_at"`
}

func peerToStore(p *service.Peer) *peer {
	return &peer{
		PublicKey:  p.PublicKey,
		ServerID:   p.ServerID,
		UserID:     p.UserID,
		Name:       p.Name,
		IP:         p.IP,
		PrivateKey: p.PrivateKey,
		PSK:        p.PSK,
		Enabled:    p.Enabled,
		ExpiresAt:  timePtr(p.ExpiresAt),
		Reminded3d: p.Reminded3d,
		Reminded1d: p.Reminded1d,
		Blocked:    p.Blocked,
		CreatedAt:  p.CreatedAt,
	}
}

func (s *peer) toService() *service.Peer {
	return &service.Peer{
		PublicKey:  s.PublicKey,
		ServerID:   s.ServerID,
		UserID:     s.UserID,
		Name:       s.Name,
		IP:         s.IP,
		PrivateKey: s.PrivateKey,
		PSK:        s.PSK,
		Enabled:    s.Enabled,
		ExpiresAt:  timeVal(s.ExpiresAt),
		Reminded3d: s.Reminded3d,
		Reminded1d: s.Reminded1d,
		Blocked:    s.Blocked,
		CreatedAt:  s.CreatedAt,
	}
}

// peerSwap is the row to remove and the document that takes its place.
type peerSwap struct {
	OldKey string
	New    *peer
}

type payment struct {
	ChargeID   string     `bson:"_id"`
	UserID     int64      `bson:"user_id"`
	PeerKey    string     `bson:"peer_key"`
	Stars      int        `bson:"stars"`
	Days       int        `bson:"days"`
	Applied    bool       `bson:"applied"`
	CreatedAt  time.Time  `bson:"created_at"`
	RefundedAt *time.Time `bson:"refunded_at"` // null = not refunded
}

func paymentToStore(p *service.Payment) *payment {
	return &payment{
		ChargeID:   p.ChargeID,
		UserID:     p.UserID,
		PeerKey:    p.PeerKey,
		Stars:      p.Stars,
		Days:       p.Days,
		Applied:    p.Applied,
		CreatedAt:  p.CreatedAt,
		RefundedAt: timePtr(p.RefundedAt),
	}
}

func (s *payment) toService() *service.Payment {
	return &service.Payment{
		ChargeID:   s.ChargeID,
		UserID:     s.UserID,
		PeerKey:    s.PeerKey,
		Stars:      s.Stars,
		Days:       s.Days,
		Applied:    s.Applied,
		CreatedAt:  s.CreatedAt,
		RefundedAt: timeVal(s.RefundedAt),
	}
}

// roleOrUser: a user added without a role (e.g. by hand) is a plain user.
func roleOrUser(r string) service.Role {
	if r == "" {
		return service.RoleUser
	}
	return service.Role(r)
}

// timePtr stores a zero time as null.
func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

func timeVal(t *time.Time) time.Time {
	if t == nil {
		return time.Time{}
	}
	return *t
}

type feedback struct {
	UserID    int64     `bson:"user_id"`
	Username  string    `bson:"username"`
	Text      string    `bson:"text"`
	CreatedAt time.Time `bson:"created_at"`
}

func feedbackToStore(f *service.Feedback) *feedback {
	return &feedback{
		UserID:    f.UserID,
		Username:  f.Username,
		Text:      f.Text,
		CreatedAt: f.CreatedAt,
	}
}

func (s *feedback) toService() *service.Feedback {
	return &service.Feedback{
		UserID:    s.UserID,
		Username:  s.Username,
		Text:      s.Text,
		CreatedAt: s.CreatedAt,
	}
}
