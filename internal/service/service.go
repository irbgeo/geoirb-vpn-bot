package service

import (
	"errors"
	"sync"
)

// ErrNotFound: the key is not in the bot's database. Peers created by hand
// in the Amnezia app are never in it, so the bot can't touch them.
var ErrNotFound = errors.New("service: key not found")

// ErrExpired: the key's term ended; it can come back only by Extend.
var ErrExpired = errors.New("service: key term ended, extend it")

// ErrIPTaken: a disabled key can't go back on the server because its IP
// is taken there by another peer (e.g. one made in the Amnezia app).
var ErrIPTaken = errors.New("service: the key's IP is taken on the server by another peer")

// ErrBlocked: an admin disabled this key; buying can't turn it back on.
var ErrBlocked = errors.New("service: key disabled by an admin")

// ErrUnreadable: the key has no readable secrets (see Peer.hasSecrets), so
// it can't go back on the server.
var ErrUnreadable = errors.New("service: key secrets are unreadable")

// ErrNoPrivateKey: the key was imported from the Amnezia app, so only the
// device it was made on has its private key; the bot can't build a config.
var ErrNoPrivateKey = errors.New("service: key has no private key (imported)")

// service is the bot's business logic. It knows nothing about Telegram.
type service struct {
	users    UserRepository
	peers    PeerRepository
	payments PaymentRepository
	feedback FeedbackRepository
	vpn      VPN
	cfg      Settings
	// ponytail: one lock for every key change (issue, extend, disable…), so a
	// payment and an admin action on the same key can't overwrite each other.
	// Per-key locks if this ever becomes a bottleneck.
	mu sync.Mutex
}

// New creates a service.
func New(
	d *Deps,
) *service {
	return &service{
		users:    d.Users,
		peers:    d.Peers,
		payments: d.Payments,
		feedback: d.Feedback,
		vpn:      d.VPN,
		cfg:      d.Settings,
	}
}
