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
	RoleVIP       Role = "vip"       // like unlimited, but the key limit is users.max_keys
	RoleMember    Role = "member"    // a company's leader or employee: free keys until the company's paid date
	RoleAdmin     Role = "admin"     // admin panel; any number of forever keys
)

// User is a Telegram user of the bot.
type User struct {
	ID        int64 // Telegram user ID
	Username  string
	Role      Role
	TrialUsed bool
	KeysCount int // keys of this user in the peers collection, kept in step
	// MaxKeys is a RoleVIP user's key limit, set by hand in the DB
	// (users.max_keys); 0 = MaxUnlimitedKeys. Other roles ignore it.
	MaxKeys int
	// CompanyID: the company of a RoleMember user ("" = none). Set by the bot
	// (create, join, remove), unlike the role of other users.
	CompanyID string
	CreatedAt time.Time
}

// Company is a client that pays for its staff's access. Its members
// (RoleMember) get free keys that end at PaidUntil.
type Company struct {
	ID string
	// Invite is the code of the invite link t.me/<bot>?start=c_<Invite>;
	// the leader or an admin can replace it (RotateInvite).
	Invite        string
	Name          string
	LeaderID      int64 // a member too; sees the staff and removes them
	PaidUntil     time.Time
	MaxMembers    int // employees; the leader takes no seat
	KeysPerMember int
	// Reminded3d / Reminded1d: the leader and the admins were told the
	// paid date is near. Reset on every extension.
	Reminded3d bool
	Reminded1d bool
	CreatedAt  time.Time
}

// CompanyInfo is a company and its members, the leader among them.
type CompanyInfo struct {
	Company *Company
	Members []*User
}

// Employees is how many seats are taken (the leader takes none).
func (s *CompanyInfo) Employees() int {
	n := 0
	for _, u := range s.Members {
		if u.ID != s.Company.LeaderID {
			n++
		}
	}
	return n
}

// CreateCompanyInput is an admin making a user the leader of a new company.
type CreateCompanyInput struct {
	LeaderID int64
	Name     string
	Months   int // paid term from now
}

// JoinCompanyInput is a user opening a company's invite link.
type JoinCompanyInput struct {
	UserID int64
	Invite string
}

// RotateInviteInput is the leader (or an admin) replacing the invite link.
type RotateInviteInput struct {
	ActorID   int64
	CompanyID string
}

// ExtendCompanyInput adds paid months to a company.
type ExtendCompanyInput struct {
	CompanyID string
	Months    int
}

// RemoveMemberInput is the leader (or an admin, ActorID) removing an
// employee from the company.
type RemoveMemberInput struct {
	ActorID   int64
	CompanyID string
	UserID    int64
}

// Membership is a user's role and company as the bot sets them; saving it
// also marks the trial used (company staff never get one).
type Membership struct {
	UserID    int64
	Role      Role
	CompanyID string // "" = no company
}

// CompanyKey is a company key that just ended, with the company's name for
// the notice.
type CompanyKey struct {
	Peer    *Peer
	Company string
}

// CompanyRemind tells the leader and the admins that the paid date is near.
type CompanyRemind struct {
	Company *Company
	Days    int // 3 or 1
}

// KeyLimit is how many keys the user may have; RoleAdmin has no limit
// (canCreate never asks).
func (s *User) KeyLimit() int {
	switch {
	case s.Role == RoleVIP && s.MaxKeys > 0:
		return s.MaxKeys
	case s.Role == RoleVIP, s.Role == RoleUnlimited:
		return MaxUnlimitedKeys
	}
	return MaxUserKeys
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
// refunded (At). UserID and Stars are written only when a refund finds no
// record and creates it.
type PaymentMark struct {
	ChargeID string
	PeerKey  string
	At       time.Time
	UserID   int64
	Stars    int
}

// RefundInput is a charge whose Stars went back. UserID and Stars describe
// it in case it has no record yet (Pay could not save it).
type RefundInput struct {
	ChargeID string
	UserID   int64
	Stars    int
}

// Page selects a slice of a list.
type Page struct {
	Skip  int64
	Limit int64
}

// Deps is everything New needs.
type Deps struct {
	Users     UserRepository
	Peers     PeerRepository
	Payments  PaymentRepository
	Feedback  FeedbackRepository
	Companies CompanyRepository
	VPN       VPN
	Settings  Settings
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
// extend; empty = their key (or a new one if they have none); BuyNewKey =
// one more key.
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
	Repeat bool // true: this charge was already applied, nothing changed; no other field is set

	// NeedsReview: the charge has a record that is neither applied nor
	// refunded; nothing was done, an admin must check the key.
	NeedsReview bool
}

// IssueInput describes a new key.
type IssueInput struct {
	UserID int64  // 0 = no Telegram user (admin-issued)
	Name   string // shown in the Amnezia app, e.g. "tg:alice"
	Days   int    // 0 = never expires
	// Until, when set, is the end date (a company key); Days is ignored.
	Until time.Time
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
	// CompanyExpired: company keys just disabled (the company's date
	// passed); they get no per-key reminders.
	CompanyExpired []CompanyKey
	CompanyRemind  []CompanyRemind // a company's paid date is near
	SubnetUsed     int             // client IPs taken (peers + reserved for disabled keys)
	SubnetTotal    int             // client IPs the subnet holds
	Online         int             // peers with a handshake within onlineWindow; -1 = unknown
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

// resetReminders starts the reminders of a new term: none is sent yet,
// except those whose window the term already starts inside (a 3-day trial
// must not get "3 days left" the minute it is issued).
func (s *Peer) resetReminders() {
	left := time.Until(s.ExpiresAt)
	timed := !s.ExpiresAt.IsZero()
	s.Reminded3d = timed && left <= remind3d
	s.Reminded1d = timed && left <= remind1d
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

// maintainStep is one key of a Maintain run and the report it adds to.
type maintainStep struct {
	Peer   *Peer
	Report *Maintenance
	// Company of the key's owner; nil = not a company key.
	Company *Company
}

// companyReminders is Maintain's pass over the companies' paid dates.
type companyReminders struct {
	Companies []*Company
	Report    *Maintenance
}

// companyManager is someone changing a company: they must be an admin or
// its leader (canManage).
type companyManager struct {
	ActorID int64
	Company *Company
}

// companyEntry is a user entering a company (create or join).
type companyEntry struct {
	User    *User
	Company *Company
}

// keysUntil is a user whose keys all get one end date (redateKeys).
type keysUntil struct {
	UserID int64
	Until  time.Time
}

// memberKeyInput is a company member's request for a key.
type memberKeyInput struct {
	User *User
	Name string
	Have []*Peer // the keys they already have
}

// userKeys is a user and their keys, for numberedKeyName.
type userKeys struct {
	User *User
	Keys []*Peer
}

// keyNumberInput is the names taken and the base name for nextKeyNumber.
type keyNumberInput struct {
	Names []string
	Base  string
}
