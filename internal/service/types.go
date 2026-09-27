package service

import "time"

// Role controls what a user may do. It is set by hand in the DB
// (users.role); the bot never changes it.
type Role string

const (
	RoleUser      Role = "user"      // trial + paid access
	RoleUnlimited Role = "unlimited" // up to MaxUnlimitedKeys keys, never expire, free
	RoleAdmin     Role = "admin"     // admin panel
)

// User is a Telegram user of the bot.
type User struct {
	ID        int64 // Telegram user ID
	Username  string
	Role      Role
	TrialUsed bool
	CreatedAt time.Time
}

// RegisterInput is who pressed /start.
type RegisterInput struct {
	ID       int64
	Username string
}

// Peer is one VPN key issued by the bot.
type Peer struct {
	PublicKey  string
	ServerID   string
	UserID     int64 // 0 for keys an admin issued without a Telegram user
	Name       string
	IP         string // client address inside the tunnel, e.g. 10.8.1.10
	PrivateKey string
	PSK        string
	Enabled    bool
	ExpiresAt  time.Time // zero = never expires
	// Reminded3d / Reminded1d: the "3 days left" / "1 day left" message
	// for the current ExpiresAt was sent. Reset on every extension.
	Reminded3d bool
	Reminded1d bool
	CreatedAt  time.Time
}

// Payment is one successful Telegram Stars payment.
type Payment struct {
	ChargeID string // telegram_payment_charge_id, unique
	UserID   int64
	PeerKey  string
	Stars    int
	Days     int
	// Applied is set once the paid days were added to the peer, so a
	// crash between "saved" and "extended" is finished on the retry.
	Applied    bool
	CreatedAt  time.Time
	RefundedAt time.Time // zero = not refunded
}

// Page selects a slice of a list.
type Page struct {
	Skip  int64
	Limit int64
}

// Deps is everything New needs.
type Deps struct {
	Users    UserRepository
	Peers    PeerRepository
	Payments PaymentRepository
	VPN      VPN
	Settings Settings
	Now      func() time.Time // nil = time.Now
}

// Settings are the per-server values the service needs.
type Settings struct {
	ServerID     string // e.g. "geoirb-vpn"
	EndpointHost string // domain or IP put into client configs
	DNS          string // e.g. "1.1.1.1, 1.0.0.1"
	TrialDays    int    // length of the free trial a RoleUser gets with their first key
	Tariffs      []Tariff
}

// Tariff is one thing a RoleUser can buy: Days of access for Stars.
type Tariff struct {
	Days  int
	Stars int
}

// PurchaseInput is what a user wants to buy. PublicKey picks the key to
// extend; empty = their key (or a new one if they have none).
type PurchaseInput struct {
	UserID    int64
	Days      int
	PublicKey string
}

// Invoice is a checked purchase, ready to become a Telegram invoice.
type Invoice struct {
	Days    int
	Stars   int
	Payload string // goes into the invoice and comes back with the payment
}

// PaymentInput is a payment Telegram reports (pre-checkout or done).
type PaymentInput struct {
	ChargeID string // telegram_payment_charge_id; empty at pre-checkout
	PayerID  int64
	Payload  string
	Stars    int
}

// PayResult is what a payment bought.
type PayResult struct {
	Peer   *Peer
	Days   int  // days bought
	NewKey bool // true: a new key was issued (send its config)
	Repeat bool // true: this charge was already applied, nothing changed
}

// IssueInput describes a new key.
type IssueInput struct {
	UserID int64  // 0 = no Telegram user (admin-issued)
	Name   string // shown in the Amnezia app, e.g. "tg:alice"
	Days   int    // 0 = never expires
}

// ExtendInput adds days to a key.
type ExtendInput struct {
	PublicKey string
	Days      int
}

// KeyInfo is a key with its live state from the server.
type KeyInfo struct {
	Peer          *Peer
	Online        bool      // handshake within the last few minutes
	LastHandshake time.Time // zero = never connected (or not on the server)
	Sent          int64     // bytes the client uploaded (server rx)
	Received      int64     // bytes the client downloaded (server tx)
}

// UserKey names one key of one user.
type UserKey struct {
	UserID    int64
	PublicKey string
}

// KeyConfig is a key with its rendered .conf file.
type KeyConfig struct {
	Peer *Peer
	Conf string
}

// Maintenance is what one Maintain run did and found.
type Maintenance struct {
	MadeForever []*Peer // owner is unlimited/admin now: end date dropped, re-enabled
	Expired     []*Peer // just disabled: their term ended
	Remind3d    []*Peer // less than 3 days left
	Remind1d    []*Peer // less than 1 day left
	SubnetUsed  int     // client IPs taken (peers + reserved for disabled keys)
	SubnetTotal int     // client IPs the subnet holds
}

// Stats is the admin overview of this server.
type Stats struct {
	Users       int64
	Active      int // enabled keys
	Disabled    int
	Expiring7d  int // enabled keys ending within 7 days
	Online      int // handshake within onlineWindow
	SubnetUsed  int
	SubnetTotal int
	Revenue30d  int // Stars of applied, not refunded payments, last 30 days
	Payments30d int
	TopTraffic  []KeyInfo // keys with the most traffic, most first
}

// ReconcileReport lists differences between the database and the server.
// Nothing is changed automatically.
type ReconcileReport struct {
	// MissingOnServer: enabled in the DB but not in the server config.
	MissingOnServer []*Peer
	// DisabledButOnServer: disabled in the DB but still in the server config.
	DisabledButOnServer []*Peer
	// Manual: peers on the server the bot doesn't know (created in the app).
	Manual int
}

// OK reports whether the database and the server agree.
func (r *ReconcileReport) OK() bool {
	return len(r.MissingOnServer) == 0 && len(r.DisabledButOnServer) == 0
}
