package service

import (
	"net/netip"
	"time"
)

// Role controls what a user may do. It is set by hand in the DB
// (users.role); the bot never changes it.
type Role string

const (
	RoleUser      Role = "user"      // trial + paid access
	RoleUnlimited Role = "unlimited" // up to MaxUnlimitedKeys keys, never expire, free
	RoleAdmin     Role = "admin"     // admin panel; any number of forever keys
)

// User is a Telegram user of the bot.
type User struct {
	ID        int64 // Telegram user ID
	Username  string
	Role      Role
	TrialUsed bool
	KeysCount int // keys of this user in the peers collection, kept in step
	CreatedAt time.Time
}

// CreateKeyInput is a user's request for their own key. Name is what the
// user called it ("iPhone"); "" = the old "tg:<user> #N" / "tg:<user>".
type CreateKeyInput struct {
	UserID int64
	Name   string
}

// PeerSwap is a key and its reissued replacement (same IP, new secrets).
type PeerSwap struct {
	Old *Peer
	New *Peer
}

// trialInput is the first key of a plain user.
type trialInput struct {
	User *User
	Name string
}

// KeysDelta changes a user's KeysCount (+1 issued, -1 deleted).
type KeysDelta struct {
	UserID int64
	Delta  int
}

// keyQuota is a user and how many keys they have (canCreate).
type keyQuota struct {
	User *User
	Keys int
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
	// Blocked: an admin disabled the key. Unlike a key disabled by expiry,
	// the user can't buy it back on; admin Enable or Extend lift it.
	Blocked   bool
	CreatedAt time.Time
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

// Feedback is a review or suggestion a user sent from the bot.
type Feedback struct {
	UserID    int64
	Username  string
	Text      string
	CreatedAt time.Time
}

// FeedbackInput is a user's review or suggestion to save.
type FeedbackInput struct {
	UserID   int64
	Username string
	Text     string
}

// PaymentMark is one change to a payment record: applied (to PeerKey) or
// refunded (At).
type PaymentMark struct {
	ChargeID string
	PeerKey  string
	At       time.Time
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
	Feedback FeedbackRepository
	VPN      VPN
	Settings Settings
}

// Settings are the per-server values the service needs.
type Settings struct {
	ServerID     string // e.g. "geoirb-vpn"
	EndpointHost string // domain or IP put into client configs
	DNS          string // e.g. "1.1.1.1, 1.0.0.1"
	MTU          int    // client MTU; 0 = not written
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

	// NeedsReview: the charge has a record that is neither applied nor
	// refunded; nothing was done, an admin must check the key.
	NeedsReview bool
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

	StatsUnavailable bool // the VPN stats call failed: the live fields are empty
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
	Online      int     // peers with a handshake within onlineWindow; -1 = unknown
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

// VPNKeys is a fresh key set from the VPN server.
type VPNKeys struct {
	Private string
	Public  string
	PSK     string
}

// VPNPeer is a key as the VPN server sees it.
type VPNPeer struct {
	PublicKey string
	PSK       string
	IP        string
	// Name and CreatedAt are shown in the Amnezia app's client list.
	Name      string
	CreatedAt time.Time
}

// AddPeerInput is a new key for VPN.AddPeer. Peer.IP is picked by the VPN.
type AddPeerInput struct {
	Peer *VPNPeer
	// Reserved are IPs taken besides the server's peers (disabled keys).
	Reserved []netip.Addr
	// Save stores the key with its new IP before the server changes; if it
	// fails, the server is not touched.
	Save func(ip string) error
}

// ReplacePeerInput is a key swapped for a new one on the same IP.
type ReplacePeerInput struct {
	Old *VPNPeer
	New *VPNPeer
}

// PeerStat is the live state of one peer. Sent and Received are from the
// client's side.
type PeerStat struct {
	PublicKey string
	// LastHandshake is zero when the peer never connected.
	LastHandshake time.Time
	Sent          int64
	Received      int64
}

// ClientSpec is what a client config is made of.
type ClientSpec struct {
	IP           string
	PrivateKey   string
	PSK          string
	DNS          string // e.g. "1.1.1.1, 1.0.0.1"
	MTU          int    // 0 = not written
	EndpointHost string // domain or IP of the server
}

// OK reports whether the database and the server agree.
func (s *ReconcileReport) OK() bool {
	return len(s.MissingOnServer) == 0 && len(s.DisabledButOnServer) == 0
}

// hasSecrets: every working key has a PSK. A key without one (its stored
// secrets could not be read) still expires and is counted, but can't be
// put back on the server.
func (s *Peer) hasSecrets() bool {
	return s.PSK != ""
}

// dead: the key is off the server and has no secrets to go back with, so
// paid days could never be used on it.
func (s *Peer) dead() bool {
	return !s.Enabled && !s.hasSecrets()
}

// vpnPeer is the key as the VPN server needs it.
func (s *Peer) vpnPeer() *VPNPeer {
	return &VPNPeer{
		PublicKey: s.PublicKey,
		PSK:       s.PSK,
		IP:        s.IP,
		Name:      s.Name,
		CreatedAt: s.CreatedAt,
	}
}

// publicAll is public for a list.
func publicAll(ps []*Peer) []*Peer {
	if ps == nil {
		return nil
	}
	out := make([]*Peer, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.public())
	}
	return out
}

// public is a copy without the private key and PSK: they never leave the
// service, only a rendered config does. nil stays nil.
func (s *Peer) public() *Peer {
	if s == nil {
		return nil
	}
	c := *s
	c.PrivateKey, c.PSK = "", ""
	return &c
}

// joinInput is the keys and live stats that join pairs up.
type joinInput struct {
	Peers []*Peer
	Stats []PeerStat
}

// logKey names one (key, error kind) pair for logOnce.
type logKey struct {
	PublicKey string
	Kind      string
}

// keyNumberInput is the names taken and the base name for nextKeyNumber.
type keyNumberInput struct {
	Names []string
	Base  string
}
