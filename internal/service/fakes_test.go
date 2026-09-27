package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/irbgeo/geoirb-vpn-bot/internal/vpn/amnezia"
)

// --- repositories: maps, copy on get/save so tests can't alias state ---

type fakeUsers struct {
	m map[int64]User
}

func (f *fakeUsers) Get(_ context.Context, id int64) (*User, error) {
	u, ok := f.m[id]
	if !ok {
		return nil, nil
	}
	return &u, nil
}

// Register mirrors the store's upsert: a new user gets u as given; an
// existing one only gets the new username (role and the rest are kept).
func (f *fakeUsers) Register(_ context.Context, u *User) (*User, error) {
	got, ok := f.m[u.ID]
	if !ok {
		got = *u
	}
	got.Username = u.Username
	f.m[u.ID] = got
	return &got, nil
}

func (f *fakeUsers) SetTrialUsed(_ context.Context, id int64) error {
	u := f.m[id]
	u.TrialUsed = true
	f.m[id] = u
	return nil
}

func (f *fakeUsers) ByRole(_ context.Context, r Role) ([]*User, error) {
	var out []*User
	for _, u := range f.m {
		if u.Role == r {
			out = append(out, &u)
		}
	}
	return out, nil
}

func (f *fakeUsers) List(_ context.Context, p Page) ([]*User, int64, error) {
	all := make([]*User, 0, len(f.m))
	for _, u := range f.m {
		all = append(all, &u)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
	end := min(p.Skip+p.Limit, int64(len(all)))
	return all[min(p.Skip, end):end], int64(len(all)), nil
}

type fakePeers struct {
	m       map[string]Peer
	saveErr error
	getErr  error
}

func (f *fakePeers) Get(_ context.Context, key string) (*Peer, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	p, ok := f.m[key]
	if !ok {
		return nil, nil
	}
	return &p, nil
}

func (f *fakePeers) Save(_ context.Context, p *Peer) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	f.m[p.PublicKey] = *p
	return nil
}

func (f *fakePeers) Delete(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err // like a real DB call on a cancelled context
	}
	delete(f.m, key)
	return nil
}

func (f *fakePeers) ByUser(_ context.Context, userID int64) ([]*Peer, error) {
	var out []*Peer
	for _, p := range f.m {
		if p.UserID == userID {
			out = append(out, &p)
		}
	}
	return out, nil
}

func (f *fakePeers) ServerIPs(ctx context.Context, serverID string) ([]string, error) {
	ps, _ := f.ByServer(ctx, serverID)
	ips := make([]string, 0, len(ps))
	for _, p := range ps {
		ips = append(ips, p.IP)
	}
	return ips, nil
}

func (f *fakePeers) ByServer(_ context.Context, serverID string) ([]*Peer, error) {
	var out []*Peer
	for _, p := range f.m {
		if p.ServerID == serverID {
			out = append(out, &p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].IP < out[j].IP })
	return out, nil
}

type fakePayments struct {
	m         map[string]Payment
	saveErr   error
	saveFails int // fail this many Saves, then work
}

func (f *fakePayments) Add(_ context.Context, p *Payment) (bool, error) {
	if _, ok := f.m[p.ChargeID]; ok {
		return false, nil
	}
	f.m[p.ChargeID] = *p
	return true, nil
}

func (f *fakePayments) Get(_ context.Context, id string) (*Payment, error) {
	p, ok := f.m[id]
	if !ok {
		return nil, nil
	}
	return &p, nil
}

func (f *fakePayments) Save(_ context.Context, p *Payment) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	if f.saveFails > 0 {
		f.saveFails--
		return errBoom
	}
	f.m[p.ChargeID] = *p
	return nil
}

func (f *fakePayments) ByUser(_ context.Context, userID int64) ([]*Payment, error) {
	var out []*Payment
	for _, p := range f.m {
		if p.UserID == userID {
			out = append(out, &p)
		}
	}
	return out, nil
}

func (f *fakePayments) Since(_ context.Context, t time.Time) ([]*Payment, error) {
	var out []*Payment
	for _, p := range f.m {
		if !p.CreatedAt.Before(t) {
			out = append(out, &p)
		}
	}
	return out, nil
}

// --- VPN server: config text in memory ---

const fakeServerConf = `[Interface]
PrivateKey = SERVERPRIV=
Address = 10.8.1.0/24
ListenPort = 443
Jc = 6
H1 = 1
# I1 = <r 2>
[Peer]
PublicKey = MANUAL1=
PresharedKey = MPSK=
AllowedIPs = 10.8.1.1/32
`

type fakeVPN struct {
	updates int // Update calls
	// appliedErr: Update applies the change, then fails anyway (a docker
	// timeout after the command already ran in the container).
	appliedErr error
	readErr    error  // fails ReadConf called directly (not through Update)
	onUpdate   func() // runs inside Update, before the sync error
	stats      []amnezia.PeerStat
	conf       string
	table      map[string]string // public key -> client name
	keys       int
	syncErr    error
	tableErr   error
}

func newFakeVPN() *fakeVPN {
	return &fakeVPN{
		conf:  fakeServerConf,
		table: map[string]string{},
	}
}

func (f *fakeVPN) GenKeys(context.Context) (amnezia.Keys, error) {
	f.keys++
	return amnezia.Keys{
		Private: fmt.Sprintf("PRIV%d=", f.keys),
		Public:  fmt.Sprintf("PUB%d=", f.keys),
		PSK:     fmt.Sprintf("PSK%d=", f.keys),
	}, nil
}

func (f *fakeVPN) ServerPublicKey(context.Context) (string, error) {
	return "SERVERPUB=", nil
}

func (f *fakeVPN) ReadConf(context.Context) (*amnezia.ServerConf, error) {
	if f.readErr != nil {
		return nil, f.readErr
	}
	return f.parse()
}

func (f *fakeVPN) parse() (*amnezia.ServerConf, error) {
	return amnezia.ParseServerConf(f.conf)
}

// Update mirrors amnezia.Server.Update: fn error or a sync error leave the
// stored config untouched.
func (f *fakeVPN) Update(_ context.Context, fn func(*amnezia.ServerConf) error) error {
	f.updates++
	c, err := f.parse()
	if err != nil {
		return err
	}
	if err := fn(c); err != nil {
		return err
	}
	if f.onUpdate != nil {
		f.onUpdate()
	}
	if f.syncErr != nil {
		return f.syncErr
	}
	f.conf = c.String()
	if f.appliedErr != nil {
		err := f.appliedErr
		f.appliedErr = nil // only the first call
		return err
	}
	return nil
}

func (f *fakeVPN) Stats(context.Context) ([]amnezia.PeerStat, error) {
	return f.stats, nil
}

func (f *fakeVPN) SetClient(_ context.Context, e amnezia.ClientEntry) error {
	if f.tableErr != nil {
		return f.tableErr
	}
	f.table[e.PublicKey] = e.Name
	return nil
}

func (f *fakeVPN) RemoveClient(_ context.Context, key string) error {
	delete(f.table, key)
	return nil
}

// hasPeer reports whether the server config holds this public key.
func (f *fakeVPN) hasPeer(key string) bool {
	c, err := amnezia.ParseServerConf(f.conf)
	if err != nil {
		panic(err)
	}
	return c.FindPeer(key) != nil
}

var errBoom = errors.New("boom")
