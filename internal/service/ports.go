package service

import (
	"context"
	"time"

	"github.com/irbgeo/geoirb-vpn-bot/internal/vpn/amnezia"
)

// UserRepository stores users. Get returns (nil, nil) when not found.
// Register is an upsert that only ever sets the username on an existing
// user, so a role set by hand is never written over.
type UserRepository interface {
	Get(ctx context.Context, id int64) (*User, error)
	Register(ctx context.Context, u *User) (*User, error)
	SetTrialUsed(ctx context.Context, id int64) error
	// AddKeys changes KeysCount; a user without a row is left alone.
	AddKeys(ctx context.Context, d KeysDelta) error
	// SetKeyCounts sets every user's KeysCount from counts (a user not in
	// counts has no keys).
	SetKeyCounts(ctx context.Context, counts map[int64]int) error
	List(ctx context.Context, p Page) ([]*User, int64, error)
	ByRole(ctx context.Context, r Role) ([]*User, error)
}

// PeerRepository stores issued keys. Get returns (nil, nil) when not found.
type PeerRepository interface {
	Get(ctx context.Context, publicKey string) (*Peer, error)
	Save(ctx context.Context, p *Peer) error
	Delete(ctx context.Context, publicKey string) error
	ByUser(ctx context.Context, userID int64) ([]*Peer, error)
	ByServer(ctx context.Context, serverID string) ([]*Peer, error)
	// ServerIPs: the IP of every peer on the server, also of rows whose
	// secrets can't be read (ByServer skips those).
	ServerIPs(ctx context.Context, serverID string) ([]string, error)
}

// PaymentRepository stores Stars payments. Get returns (nil, nil) when
// not found. Add returns false when the charge ID is already recorded.
type PaymentRepository interface {
	Add(ctx context.Context, p *Payment) (bool, error)
	Get(ctx context.Context, chargeID string) (*Payment, error)
	// MarkApplied / MarkRefunded change only their own fields, so an admin
	// refund and a running Pay can't overwrite each other's mark.
	// MarkRefunded of a charge with no record is not an error.
	MarkApplied(ctx context.Context, m PaymentMark) error
	MarkRefunded(ctx context.Context, m PaymentMark) error
	ByUser(ctx context.Context, userID int64) ([]*Payment, error)
	Since(ctx context.Context, t time.Time) ([]*Payment, error)
}

// VPN is the AmneziaWG server (implemented by *amnezia.Server).
type VPN interface {
	GenKeys(ctx context.Context) (amnezia.Keys, error)
	ServerPublicKey(ctx context.Context) (string, error)
	ReadConf(ctx context.Context) (*amnezia.ServerConf, error)
	Update(ctx context.Context, fn func(*amnezia.ServerConf) error) error
	Stats(ctx context.Context) ([]amnezia.PeerStat, error)
	SetClient(ctx context.Context, e amnezia.ClientEntry) error
	RemoveClient(ctx context.Context, publicKey string) error
}
