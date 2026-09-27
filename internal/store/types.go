package store

import (
	"time"

	"github.com/irbgeo/geoirb-vpn-bot/internal/service"
)

// This file is the storage boundary: MongoDB document types (with bson
// tags) and converters to/from the tag-free types in internal/service.

// ConnectInput bundles Connect's arguments beyond ctx.
type ConnectInput struct {
	URI    string
	DBName string
	// SecretKey (32 bytes) encrypts peer private keys and PSKs at rest.
	SecretKey []byte
}

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
	CreatedAt time.Time `bson:"created_at"`
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

func (d *user) toService() *service.User {
	return &service.User{
		ID:        d.ID,
		Username:  d.Username,
		Role:      roleOrUser(d.Role),
		TrialUsed: d.TrialUsed,
		KeysCount: d.KeysCount,
		CreatedAt: d.CreatedAt,
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

func (d *peer) toService() *service.Peer {
	return &service.Peer{
		PublicKey:  d.PublicKey,
		ServerID:   d.ServerID,
		UserID:     d.UserID,
		Name:       d.Name,
		IP:         d.IP,
		PrivateKey: d.PrivateKey,
		PSK:        d.PSK,
		Enabled:    d.Enabled,
		ExpiresAt:  timeVal(d.ExpiresAt),
		Reminded3d: d.Reminded3d,
		Reminded1d: d.Reminded1d,
		Blocked:    d.Blocked,
		CreatedAt:  d.CreatedAt,
	}
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

func (d *payment) toService() *service.Payment {
	return &service.Payment{
		ChargeID:   d.ChargeID,
		UserID:     d.UserID,
		PeerKey:    d.PeerKey,
		Stars:      d.Stars,
		Days:       d.Days,
		Applied:    d.Applied,
		CreatedAt:  d.CreatedAt,
		RefundedAt: timeVal(d.RefundedAt),
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
