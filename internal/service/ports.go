package service

import (
	"context"
	"net/netip"
	"time"
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
	// Replace puts in.New in place of in.Old (same IP) in one write: there
	// is no moment a caller can stop at with the old record gone and the
	// new one not saved. in.New must have its secrets.
	Replace(ctx context.Context, in PeerSwap) error
	ByUser(ctx context.Context, userID int64) ([]*Peer, error)
	ByServer(ctx context.Context, serverID string) ([]*Peer, error)
	// ServerIPs: the IP of every peer of the server, enabled or not (a
	// disabled key keeps its IP reserved).
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

// FeedbackRepository stores users' reviews and suggestions.
type FeedbackRepository interface {
	Add(ctx context.Context, f *Feedback) error
	// List returns one page, newest first, and the total count.
	List(ctx context.Context, p Page) ([]*Feedback, int64, error)
}

// VPN is the VPN server, in keys and configs (implemented by the value
// amnezia.NewVPN returns). A change either fully happens or is undone
// before the error is returned: the server never keeps half of it. Only
// the key's place in the app's client list may lag (logged, not an error).
type VPN interface {
	// GenKeys makes a fresh key set.
	GenKeys(ctx context.Context) (VPNKeys, error)
	// AddPeer puts a new key on the lowest free IP (see AddPeerInput).
	AddPeer(ctx context.Context, in *AddPeerInput) error
	// PutPeer puts a known key back on its IP; ErrIPTaken if another peer
	// holds it. A key already there is left as it is.
	PutPeer(ctx context.Context, p *VPNPeer) error
	// RemovePeer takes a key off; a key not there is fine.
	RemovePeer(ctx context.Context, p *VPNPeer) error
	// ReplacePeer puts New in place of Old in one step; on failure Old is
	// back.
	ReplacePeer(ctx context.Context, in *ReplacePeerInput) error
	// PeerKeys: the public key of every peer on the server, ours or not.
	PeerKeys(ctx context.Context) ([]string, error)
	// SubnetUsage: client IPs taken (the server's peers plus reserved) and
	// how many there are in all.
	SubnetUsage(ctx context.Context, reserved []netip.Addr) (used, total int, err error)
	// Stats: live handshake and traffic of every peer.
	Stats(ctx context.Context) ([]PeerStat, error)
	// ClientConfig renders a client config file.
	ClientConfig(ctx context.Context, c *ClientSpec) (string, error)
}
